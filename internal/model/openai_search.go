package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"icetudio/internal/config"
)

func responseSearchParams(source config.Model, messages []Message, tools []Tool, maxCalls int) (responses.ResponseNewParams, error) {
	if err := validateRequest(source, messages); err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{Model: shared.ResponsesModel(source.Name), MaxOutputTokens: openai.Int(8192), MaxToolCalls: openai.Int(int64(maxCalls)), Store: openai.Bool(false), Include: []responses.ResponseIncludable{"reasoning.encrypted_content"}}
	for _, m := range messages {
		switch m.Role {
		case "system", "user", "assistant":
			for _, raw := range m.ProviderState {
				var item responses.ResponseInputItemUnionParam
				if err := json.Unmarshal(raw, &item); err != nil {
					return params, err
				}
				params.Input.OfInputItemList = append(params.Input.OfInputItemList, item)
			}
			if len(m.Files) > 0 {
				var item responses.ResponseInputItemUnionParam
				if err := json.Unmarshal(materialJSON(m, "responses"), &item); err != nil {
					return params, err
				}
				params.Input.OfInputItemList = append(params.Input.OfInputItemList, item)
			} else if m.Content != "" {
				params.Input.OfInputItemList = append(params.Input.OfInputItemList, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRole(m.Role)))
			}
			for _, call := range m.ToolCalls {
				params.Input.OfInputItemList = append(params.Input.OfInputItemList, responses.ResponseInputItemParamOfFunctionCall(call.Function.Arguments, call.ID, call.Function.Name))
			}
		case "tool":
			params.Input.OfInputItemList = append(params.Input.OfInputItemList, responses.ResponseInputItemParamOfFunctionCallOutput(m.ToolCallID, m.Content))
		default:
			return params, fmt.Errorf("不支持的消息角色 %q", m.Role)
		}
	}
	// The installed SDK models preview tools; override only its wire type below
	// to the current web_search protocol, without keeping a preview fallback.
	params.Tools = append(params.Tools, responses.ToolParamOfWebSearchPreview("web_search_preview"))
	for _, tool := range providerToolsOf(tools) {
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
			if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
				return params, err
			}
		}
		params.Tools = append(params.Tools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{Name: tool.Name, Description: openai.String(tool.Description), Parameters: schema, Strict: openai.Bool(false)}})
	}
	return params, nil
}
func ValidCitationURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
func responseSearchResult(resp responses.Response) ChatResponse {
	out := ChatResponse{Message: Message{Role: "assistant"}, FinishReason: "stop", Usage: Usage{PromptTokens: int(resp.Usage.InputTokens), CompletionTokens: int(resp.Usage.OutputTokens), TotalTokens: int(resp.Usage.TotalTokens)}}
	count := 0
	for _, item := range resp.Output {
		switch item.Type {
		case "reasoning":
			out.Message.ProviderState = append(out.Message.ProviderState, json.RawMessage(item.RawJSON()))
		case "web_search_call":
			count++
			var action struct {
				Action struct {
					Queries []string `json:"queries"`
				}
			}
			_ = json.Unmarshal([]byte(item.RawJSON()), &action)
			out.Search.Events = append(out.Search.Events, SearchEvent{Type: "web_search_call", ID: item.ID, Query: item.Action.Query, Queries: action.Action.Queries, URL: item.Action.URL, Status: item.Status})
		case "message":
			for _, part := range item.Content {
				offset := len([]rune(out.Message.Content))
				out.Message.Content += part.Text
				for _, ann := range part.Annotations {
					if ann.Type == "url_citation" && ValidCitationURL(ann.URL) {
						out.Search.Citations = append(out.Search.Citations, Citation{URL: ann.URL, Title: ann.Title, Start: offset + int(ann.StartIndex), End: offset + int(ann.EndIndex)})
					}
				}
			}
		case "function_call":
			call := ToolCall{ID: item.CallID, Type: "function"}
			call.Function.Name = item.Name
			call.Function.Arguments = item.Arguments
			out.Message.ToolCalls = append(out.Message.ToolCalls, call)
		}
	}
	out.Search.Calls = &count
	out.Search.Text = out.Message.Content
	if len(out.Message.ToolCalls) > 0 {
		out.FinishReason = "tool_calls"
	}
	if resp.Status == "incomplete" {
		out.FinishReason = "length"
	}
	return out
}

func (c Client) openaiChatWithSearch(ctx context.Context, source config.Model, messages []Message, tools []Tool, search SearchOptions, onDelta func(string) error) (ChatResponse, error) {
	if !NativeSearchSupported(source) {
		return ChatResponse{}, errors.New("当前模型接口尚未支持原生联网搜索")
	}
	if search.MaxCalls < 1 || search.MaxCalls > 3 {
		return ChatResponse{}, errors.New("原生搜索调用上限需为 1..3")
	}
	client, err := c.openaiClient(source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := responseSearchParams(source, messages, tools, search.MaxCalls)
	if err != nil {
		return ChatResponse{}, err
	}
	opts := []option.RequestOption{option.WithMaxRetries(0), option.WithJSONSet("tools.0.type", "web_search")}
	if onDelta == nil {
		resp, err := client.Responses.New(ctx, params, opts...)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("OpenAI 原生搜索请求失败: %w", err)
		}
		out := responseSearchResult(*resp)
		if resp.Status != "completed" && resp.Status != "incomplete" {
			return out, errors.New("原生搜索响应未完成")
		}
		if search.OnEvent != nil {
			for _, e := range out.Search.Events {
				if err := search.OnEvent(e); err != nil {
					return out, err
				}
			}
		}
		return out, nil
	}
	stream := client.Responses.NewStreaming(ctx, params, opts...)
	defer stream.Close()
	partial := ChatResponse{Message: Message{Role: "assistant"}}
	for stream.Next() {
		event := stream.Current()
		switch {
		case event.Type == "response.output_text.delta":
			delta := event.Delta.OfString
			partial.Message.Content += delta
			if err := onDelta(delta); err != nil {
				return partial, err
			}
		case strings.HasPrefix(event.Type, "response.web_search_call."):
			e := SearchEvent{Type: event.Type, ID: event.ItemID}
			partial.Search.Events = append(partial.Search.Events, e)
			if search.OnEvent != nil {
				if err := search.OnEvent(e); err != nil {
					return partial, err
				}
			}
		case event.Type == "response.completed" || event.Type == "response.incomplete":
			out := responseSearchResult(event.Response)
			// Keep stream lifecycle evidence as well as complete call actions.
			out.Search.Events = append(partial.Search.Events, out.Search.Events...)
			return out, nil
		case event.Type == "response.failed" || event.Type == "error":
			return partial, errors.New("原生搜索流返回失败事件")
		}
	}
	if err := stream.Err(); err != nil {
		return partial, fmt.Errorf("原生搜索流失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return partial, err
	}
	return partial, errors.New("原生搜索流缺少完成事件")
}
