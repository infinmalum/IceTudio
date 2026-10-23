package model

import (
	"context"
	"encoding/json"
	"fmt"

	"icetudio/internal/config"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// openaiClient builds the official SDK client for one request: the stored
// credential is resolved here and never embedded in any message or snapshot.
// An endpoint override (BaseURL) is honored so self-hosted gateways can be
// pointed at, subject to the exfiltration check.
func (c Client) openaiClient(source config.Model) (*openai.Client, error) {
	key, err := c.apiKey("openai")
	if err != nil {
		return nil, err
	}
	opts := []option.RequestOption{option.WithAPIKey(key)}
	if base := source.BaseURL; base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	opts = append(opts, option.WithHTTPClient(c.httpClient()))
	client := openai.NewClient(opts...)
	return &client, nil
}

func openaiMessages(messages []Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	converted := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "system":
			converted = append(converted, openai.SystemMessage(message.Content))
		case "user":
			if len(message.Files) == 0 {
				converted = append(converted, openai.UserMessage(message.Content))
			} else {
				var item openai.ChatCompletionMessageParamUnion
				if err := json.Unmarshal(materialJSON(message, "openai"), &item); err != nil {
					return nil, err
				}
				converted = append(converted, item)
			}
		case "assistant":
			param := openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(message.Content),
				},
			}
			for _, call := range message.ToolCalls {
				param.ToolCalls = append(param.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID: call.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				})
			}
			converted = append(converted, openai.ChatCompletionMessageParamUnion{OfAssistant: &param})
		case "tool":
			converted = append(converted, openai.ToolMessage(message.Content, message.ToolCallID))
		default:
			return nil, fmt.Errorf("不支持的消息角色 %q", message.Role)
		}
	}
	return converted, nil
}

func openaiTools(tools []Tool) ([]openai.ChatCompletionToolParam, error) {
	converted := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, tool := range providerToolsOf(tools) {
		var parameters shared.FunctionParameters
		if len(tool.Parameters) > 0 {
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				return nil, fmt.Errorf("工具 %s 参数 schema 无效: %w", tool.Name, err)
			}
		}
		converted = append(converted, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        tool.Name,
				Description: openai.String(tool.Description),
				Parameters:  parameters,
			},
		})
	}
	return converted, nil
}

func openaiChatParams(source config.Model, messages []Message, tools []Tool) (openai.ChatCompletionNewParams, error) {
	converted, err := openaiMessages(messages)
	if err != nil {
		return openai.ChatCompletionNewParams{}, err
	}
	toolParams, err := openaiTools(tools)
	if err != nil {
		return openai.ChatCompletionNewParams{}, err
	}
	params := openai.ChatCompletionNewParams{
		MaxCompletionTokens: openai.Int(8192),
		Model:               openai.ChatModel(source.Name),
		Messages:            converted,
	}
	if len(toolParams) > 0 {
		params.Tools = toolParams
	}
	return params, nil
}

func (c Client) openaiChat(ctx context.Context, source config.Model, messages []Message, tools []Tool) (ChatResponse, error) {
	client, err := c.openaiClient(source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := openaiChatParams(source, messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	completion, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("OpenAI 请求失败: %w", err)
	}
	if len(completion.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("OpenAI 回答没有 choices")
	}
	choice := completion.Choices[0]
	response := ChatResponse{
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     int(completion.Usage.PromptTokens),
			CompletionTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:      int(completion.Usage.TotalTokens),
		},
	}
	response.Message.Role = "assistant"
	response.Message.Content = choice.Message.Content
	for _, call := range choice.Message.ToolCalls {
		response.Message.ToolCalls = append(response.Message.ToolCalls, ToolCall{
			ID:   call.ID,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: call.Function.Name, Arguments: call.Function.Arguments},
		})
	}
	return response, nil
}

func (c Client) openaiChatStream(ctx context.Context, source config.Model, messages []Message, tools []Tool, onDelta func(string) error) (ChatResponse, error) {
	client, err := c.openaiClient(source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := openaiChatParams(source, messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()
	merger := &deltaMerger{}
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta.Content != "" {
				if err := merger.addText(choice.Delta.Content, onDelta); err != nil {
					return ChatResponse{}, err
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				merger.addToolCall(int(call.Index), call.ID, call.Function.Name, call.Function.Arguments)
			}
			if choice.FinishReason != "" {
				merger.finish = choice.FinishReason
			}
		}
		if chunk.Usage.TotalTokens > 0 {
			merger.usage = Usage{
				PromptTokens:     int(chunk.Usage.PromptTokens),
				CompletionTokens: int(chunk.Usage.CompletionTokens),
				TotalTokens:      int(chunk.Usage.TotalTokens),
			}
		}
	}
	if err := stream.Err(); err != nil {
		return ChatResponse{}, fmt.Errorf("OpenAI 流式请求失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ChatResponse{}, err
	}
	return merger.response(), nil
}
