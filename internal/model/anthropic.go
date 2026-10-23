package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"icetudio/internal/config"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// anthropicMaxTokens is the required Messages API output cap for IceTudio
// requests; the loop's own budgets bound what IceTudio does with the reply.
const anthropicMaxTokens = 8192

// anthropicClient builds the official SDK client for one request. The
// credential stays out of messages, snapshots and logs.
func (c Client) anthropicClient(source config.Model) (*anthropic.Client, error) {
	key, err := c.apiKey("anthropic")
	if err != nil {
		return nil, err
	}
	opts := []option.RequestOption{option.WithAPIKey(key)}
	if base := source.BaseURL; base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	opts = append(opts, option.WithHTTPClient(c.httpClient()))
	client := anthropic.NewClient(opts...)
	return &client, nil
}

// anthropicRequests maps the unified history to Messages API params. Tool
// results become tool_result blocks grouped in one user turn right after the
// assistant tool_use turn, and the leading system message moves to the
// top-level system parameter.
func anthropicRequests(messages []Message, tools []Tool) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{MaxTokens: anthropicMaxTokens}
	converted := []anthropic.MessageParam{}
	var pendingResults []anthropic.ContentBlockParamUnion
	flush := func() {
		if len(pendingResults) > 0 {
			converted = append(converted, anthropic.NewUserMessage(pendingResults...))
			pendingResults = nil
		}
	}
	for i, message := range messages {
		switch message.Role {
		case "system":
			if i == 0 {
				params.System = []anthropic.TextBlockParam{{Text: message.Content}}
			}
		case "user":
			flush()
			var turn anthropic.MessageParam
			if err := json.Unmarshal(materialJSON(message, "anthropic"), &turn); err != nil {
				return params, err
			}
			converted = append(converted, turn)
		case "assistant":
			flush()
			blocks := []anthropic.ContentBlockParamUnion{}
			if len(message.ProviderState) > 0 {
				for _, raw := range message.ProviderState {
					var block anthropic.ContentBlockParamUnion
					if err := json.Unmarshal(raw, &block); err != nil {
						return params, err
					}
					blocks = append(blocks, block)
				}
				converted = append(converted, anthropic.NewAssistantMessage(blocks...))
				continue
			}
			if message.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(message.Content))
			}
			for _, call := range message.ToolCalls {
				arguments := call.Function.Arguments
				if strings.TrimSpace(arguments) == "" {
					arguments = "{}"
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, json.RawMessage(arguments), call.Function.Name))
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropic.NewTextBlock(""))
			}
			converted = append(converted, anthropic.NewAssistantMessage(blocks...))
		case "tool":
			pendingResults = append(pendingResults, anthropic.NewToolResultBlock(message.ToolCallID, message.Content, false))
		default:
			return anthropic.MessageNewParams{}, fmt.Errorf("不支持的消息角色 %q", message.Role)
		}
	}
	flush()
	params.Messages = converted

	toolParams := []anthropic.ToolUnionParam{}
	for _, tool := range providerToolsOf(tools) {
		properties, required, err := toolSchema(tool.Parameters)
		if err != nil {
			return anthropic.MessageNewParams{}, fmt.Errorf("工具 %s: %w", tool.Name, err)
		}
		toolParams = append(toolParams, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        tool.Name,
			Description: anthropic.String(tool.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: properties,
				Required:   required,
			},
		}})
	}
	if len(toolParams) > 0 {
		params.Tools = toolParams
	}
	return params, nil
}

func anthropicStopReason(reason anthropic.StopReason) string {
	switch reason {
	case anthropic.StopReasonToolUse:
		return "tool_calls"
	case anthropic.StopReasonMaxTokens:
		return "length"
	default:
		return "stop"
	}
}

func (c Client) anthropicChat(ctx context.Context, source config.Model, messages []Message, tools []Tool) (ChatResponse, error) {
	client, err := c.anthropicClient(source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := anthropicRequests(messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	params.Model = source.Name
	message, err := client.Messages.New(ctx, params)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("Anthropic 请求失败: %w", err)
	}
	return anthropicResponse(*message), nil
}

func anthropicResponse(message anthropic.Message) ChatResponse {
	response := ChatResponse{
		FinishReason: anthropicStopReason(message.StopReason),
		Usage: Usage{
			PromptTokens:     int(message.Usage.InputTokens),
			CompletionTokens: int(message.Usage.OutputTokens),
			TotalTokens:      int(message.Usage.InputTokens) + int(message.Usage.OutputTokens),
		},
	}
	response.Message.Role = "assistant"
	var text strings.Builder
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			response.Message.ToolCalls = append(response.Message.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: block.Name, Arguments: string(block.Input)},
			})
		}
	}
	response.Message.Content = text.String()
	return response
}

func (c Client) anthropicChatStream(ctx context.Context, source config.Model, messages []Message, tools []Tool, onDelta func(string) error) (ChatResponse, error) {
	client, err := c.anthropicClient(source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := anthropicRequests(messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	params.Model = source.Name
	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	merger := &deltaMerger{}
	// Tool-call arguments arrive as input_json_delta fragments keyed by the
	// content block index; ids and names arrive on content_block_start.
	blockCalls := map[int64]int{}
	promptTokens := 0
	completionTokens := 0
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "message_start":
			promptTokens = int(event.Message.Usage.InputTokens)
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				merger.addToolCall(len(merger.order), event.ContentBlock.ID, event.ContentBlock.Name, "")
				blockCalls[event.Index] = merger.order[len(merger.order)-1]
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				if err := merger.addText(event.Delta.Text, onDelta); err != nil {
					return ChatResponse{}, err
				}
			case "input_json_delta":
				if index, ok := blockCalls[event.Index]; ok {
					merger.addToolCall(index, "", "", event.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if event.Delta.StopReason != "" {
				merger.finish = anthropicStopReason(event.Delta.StopReason)
			}
			if event.Usage.OutputTokens > 0 {
				completionTokens = int(event.Usage.OutputTokens)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return ChatResponse{}, fmt.Errorf("Anthropic 流式请求失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ChatResponse{}, err
	}
	response := merger.response()
	if promptTokens > 0 {
		response.Usage.PromptTokens = promptTokens
		response.Usage.TotalTokens += promptTokens
	}
	if completionTokens > 0 {
		response.Usage.CompletionTokens = completionTokens
		response.Usage.TotalTokens += completionTokens
	}
	return response, nil
}
