package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"icetudio/internal/config"

	"google.golang.org/genai"
)

// geminiClient builds the official SDK client for one request. Only the
// Gemini API backend (API key) is supported; Vertex AI credentials are out of
// scope for this first adapter set.
func (c Client) geminiClient(ctx context.Context, source config.Model) (*genai.Client, error) {
	key, err := c.apiKey("gemini")
	if err != nil {
		return nil, err
	}
	cfg := &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	}
	if base := source.BaseURL; base != "" {
		cfg.HTTPOptions = genai.HTTPOptions{BaseURL: base}
	}
	cfg.HTTPClient = c.httpClient()
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("初始化 Gemini 客户端: %w", err)
	}
	return client, nil
}

func geminiModelName(name string) string {
	return strings.TrimPrefix(name, "models/")
}

// geminiContents maps the unified history to Gemini contents. Tool results
// become functionResponse parts; the function name is recovered from the
// preceding assistant turn because the unified tool message only carries the
// call ID.
func geminiContents(messages []Message) ([]*genai.Content, error) {
	callNames := map[string]string{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			callNames[call.ID] = call.Function.Name
		}
	}
	contents := []*genai.Content{}
	for _, message := range messages {
		switch message.Role {
		case "system":
			// The system prompt goes through config.SystemInstruction, not
			// the contents list.
		case "user":
			parts := []*genai.Part{{Text: materialText(message)}}
			for _, f := range message.Files {
				if f.Data != "" {
					data, err := base64.StdEncoding.DecodeString(f.Data)
					if err != nil {
						return nil, err
					}
					parts = append(parts, &genai.Part{InlineData: &genai.Blob{MIMEType: f.MediaType, Data: data}})
				}
			}
			contents = append(contents, &genai.Content{Role: "user", Parts: parts})
		case "assistant":
			turn := &genai.Content{Role: "model"}
			if message.Content != "" {
				turn.Parts = append(turn.Parts, &genai.Part{Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				args := map[string]any{}
				if strings.TrimSpace(call.Function.Arguments) != "" {
					if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
						return nil, fmt.Errorf("工具 %s 调用参数无效: %w", call.Function.Name, err)
					}
				}
				turn.Parts = append(turn.Parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Function.Name, Args: args}})
			}
			contents = append(contents, turn)
		case "tool":
			name := callNames[message.ToolCallID]
			if name == "" {
				name = "unknown"
			}
			contents = append(contents, &genai.Content{Role: "user", Parts: []*genai.Part{{
				FunctionResponse: &genai.FunctionResponse{
					ID:       message.ToolCallID,
					Name:     name,
					Response: map[string]any{"result": message.Content},
				},
			}}})
		default:
			return nil, fmt.Errorf("不支持的消息角色 %q", message.Role)
		}
	}
	return contents, nil
}

// geminiConfig assembles the request config: the leading system message
// becomes the system instruction and the unified tool declarations are
// converted to function declarations.
func geminiConfig(messages []Message, tools []Tool) (*genai.GenerateContentConfig, error) {
	cfg := &genai.GenerateContentConfig{MaxOutputTokens: 8192}
	if len(messages) > 0 && messages[0].Role == "system" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: messages[0].Content}}}
	}
	declarations := []*genai.FunctionDeclaration{}
	for _, tool := range providerToolsOf(tools) {
		declaration := &genai.FunctionDeclaration{Name: tool.Name, Description: tool.Description}
		if len(tool.Parameters) > 0 {
			var schema genai.Schema
			if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
				return nil, fmt.Errorf("工具 %s 参数 schema 无效: %w", tool.Name, err)
			}
			declaration.Parameters = &schema
		}
		declarations = append(declarations, declaration)
	}
	if len(declarations) > 0 {
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: declarations}}
	}
	return cfg, nil
}

func geminiFinishReason(reason genai.FinishReason, hasCalls bool) string {
	if hasCalls {
		return "tool_calls"
	}
	switch reason {
	case genai.FinishReasonMaxTokens:
		return "length"
	case "":
		return ""
	default:
		return "stop"
	}
}

// geminiResponse converts one GenerateContentResponse into the unified form.
// Streaming chunks are merged by geminiMerge before conversion.
func geminiResponse(resp *genai.GenerateContentResponse, callOffset int) ChatResponse {
	response := ChatResponse{}
	if resp == nil || len(resp.Candidates) == 0 {
		return response
	}
	candidate := resp.Candidates[0]
	if candidate.Content != nil {
		var text strings.Builder
		for _, part := range candidate.Content.Parts {
			switch {
			case part.Text != "" && !part.Thought:
				text.WriteString(part.Text)
			case part.FunctionCall != nil:
				call := part.FunctionCall
				id := call.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", callOffset+len(response.Message.ToolCalls))
				}
				arguments, _ := json.Marshal(call.Args)
				response.Message.ToolCalls = append(response.Message.ToolCalls, ToolCall{
					ID:   id,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: call.Name, Arguments: string(arguments)},
				})
			}
		}
		response.Message.Content = text.String()
	}
	response.FinishReason = geminiFinishReason(candidate.FinishReason, len(response.Message.ToolCalls) > 0)
	if usage := resp.UsageMetadata; usage != nil {
		response.Usage = Usage{
			PromptTokens:     int(usage.PromptTokenCount),
			CompletionTokens: int(usage.CandidatesTokenCount),
			TotalTokens:      int(usage.TotalTokenCount),
		}
	}
	return response
}

// geminiMerge folds streamed chunks into one unified response: text deltas
// concatenate, function calls collect, and the last usage/finish reason wins.
func geminiMerge(chunks []*genai.GenerateContentResponse) ChatResponse {
	merged := ChatResponse{}
	var text strings.Builder
	callOffset := 0
	for _, chunk := range chunks {
		part := geminiResponse(chunk, callOffset)
		callOffset += len(part.Message.ToolCalls)
		text.WriteString(part.Message.Content)
		merged.Message.ToolCalls = append(merged.Message.ToolCalls, part.Message.ToolCalls...)
		if part.FinishReason != "" {
			merged.FinishReason = part.FinishReason
		}
		if part.Usage.TotalTokens > 0 {
			merged.Usage = part.Usage
		}
	}
	if len(merged.Message.ToolCalls) > 0 {
		merged.FinishReason = "tool_calls"
	}
	merged.Message.Role = "assistant"
	merged.Message.Content = text.String()
	return merged
}

func (c Client) geminiChat(ctx context.Context, source config.Model, messages []Message, tools []Tool) (ChatResponse, error) {
	if NativeSearchSupported(source) {
		return c.geminiInteractionChat(ctx, source, messages, tools, SearchOptions{}, nil, false)
	}
	client, err := c.geminiClient(ctx, source)
	if err != nil {
		return ChatResponse{}, err
	}
	contents, err := geminiContents(messages)
	if err != nil {
		return ChatResponse{}, err
	}
	cfg, err := geminiConfig(messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := client.Models.GenerateContent(ctx, geminiModelName(source.Name), contents, cfg)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("Gemini 请求失败: %w", err)
	}
	response := geminiResponse(resp, 0)
	response.Message.Role = "assistant"
	return response, nil
}

func (c Client) geminiChatStream(ctx context.Context, source config.Model, messages []Message, tools []Tool, onDelta func(string) error) (ChatResponse, error) {
	if NativeSearchSupported(source) {
		return c.geminiInteractionChat(ctx, source, messages, tools, SearchOptions{}, onDelta, false)
	}
	client, err := c.geminiClient(ctx, source)
	if err != nil {
		return ChatResponse{}, err
	}
	contents, err := geminiContents(messages)
	if err != nil {
		return ChatResponse{}, err
	}
	cfg, err := geminiConfig(messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	chunks := []*genai.GenerateContentResponse{}
	for chunk, err := range client.Models.GenerateContentStream(ctx, geminiModelName(source.Name), contents, cfg) {
		if err != nil {
			return ChatResponse{}, fmt.Errorf("Gemini 流式请求失败: %w", err)
		}
		chunks = append(chunks, chunk)
		if onDelta != nil {
			if delta := streamText(chunk); delta != "" {
				if err := onDelta(delta); err != nil {
					return ChatResponse{}, err
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ChatResponse{}, err
	}
	return geminiMerge(chunks), nil
}

// streamText extracts the user-visible text of one stream chunk (thought
// summaries are not streamed to the client).
func streamText(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" && !part.Thought {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}
