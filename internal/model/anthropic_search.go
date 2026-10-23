package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"icetudio/internal/config"
)

func anthropicSearchResult(message anthropic.Message) (ChatResponse, error) {
	out := anthropicResponse(message)
	out.Usage.PromptTokens += int(message.Usage.CacheCreationInputTokens + message.Usage.CacheReadInputTokens)
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens
	out.Search.BudgetUnit = "search_call"
	attempted := 0
	for _, block := range message.Content {
		raw := json.RawMessage(block.RawJSON())
		out.Message.ProviderState = append(out.Message.ProviderState, raw)
		switch block.Type {
		case "server_tool_use":
			if block.Name != "web_search" && block.Name != "web_fetch" {
				return out, errors.New("服务商返回未授权的服务器工具")
			}
			attempted++
			var args struct {
				Query string `json:"query"`
				URL   string `json:"url"`
			}
			if err := json.Unmarshal(block.Input, &args); err != nil {
				return out, err
			}
			out.Search.Events = append(out.Search.Events, SearchEvent{Type: block.Name + "_call", ID: block.ID, Query: args.Query, URL: args.URL, Status: "completed"})
		case "web_search_tool_result", "web_fetch_tool_result":
			var result struct {
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return out, err
			}
			var failure struct {
				ErrorCode string `json:"error_code"`
				URL       string `json:"url"`
			}
			_ = json.Unmarshal(result.Content, &failure)
			if ValidCitationURL(failure.URL) {
				out.Search.Citations = append(out.Search.Citations, Citation{URL: failure.URL, Title: failure.URL})
				out.Search.Events = append(out.Search.Events, SearchEvent{Type: "web_fetch_result", ID: result.ToolUseID, URL: failure.URL, Status: "completed"})
			}
			if failure.ErrorCode != "" {
				out.Search.Events = append(out.Search.Events, SearchEvent{Type: block.Type + "_error", ID: result.ToolUseID, Status: failure.ErrorCode})
				if failure.ErrorCode != "max_uses_exceeded" {
					return out, fmt.Errorf("Anthropic 搜索失败: %s", failure.ErrorCode)
				}
			}
		case "text":
			var text struct {
				Citations []struct {
					Type  string `json:"type"`
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"citations"`
			}
			if err := json.Unmarshal(raw, &text); err != nil {
				return out, err
			}
			for _, citation := range text.Citations {
				if citation.Type == "web_search_result_location" && ValidCitationURL(citation.URL) {
					out.Search.Citations = append(out.Search.Citations, Citation{URL: citation.URL, Title: citation.Title})
				}
			}
		case "thinking", "redacted_thinking", "tool_use":
		default:
			return out, fmt.Errorf("原生搜索返回未支持内容类型 %q", block.Type)
		}
	}
	var usage struct {
		Usage struct {
			ServerToolUse *struct {
				Requests *int `json:"web_search_requests"`
				Fetches  *int `json:"web_fetch_requests"`
			} `json:"server_tool_use"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(message.RawJSON()), &usage); err != nil {
		return out, err
	}
	if usage.Usage.ServerToolUse != nil && usage.Usage.ServerToolUse.Requests != nil {
		count := *usage.Usage.ServerToolUse.Requests
		if usage.Usage.ServerToolUse.Fetches != nil {
			count += *usage.Usage.ServerToolUse.Fetches
		} else {
			for _, e := range out.Search.Events {
				if e.Type == "web_fetch_call" {
					count++
				}
			}
		}
		out.Search.Calls = &count
	} else if attempted == 0 {
		zero := 0
		out.Search.Calls = &zero
	}
	out.Search.Text = out.Message.Content
	if message.StopReason == "pause_turn" {
		return out, errors.New("Anthropic 搜索暂停，未完成当前回合")
	}
	if message.StopReason != "end_turn" && message.StopReason != "tool_use" && message.StopReason != "max_tokens" && message.StopReason != "stop_sequence" {
		return out, errors.New("Anthropic 搜索未返回终止原因")
	}
	return out, nil
}
func (c Client) anthropicChatWithSearch(ctx context.Context, source config.Model, messages []Message, tools []Tool, search SearchOptions, onDelta func(string) error) (ChatResponse, error) {
	if err := validateRequest(source, messages); err != nil {
		return ChatResponse{}, err
	}
	client, err := c.anthropicClient(source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := anthropicRequests(messages, tools)
	if err != nil {
		return ChatResponse{}, err
	}
	params.Model = source.Name
	// Share the invocation ceiling between search and fetch, never reserve it twice.
	directURL := false
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			directURL = strings.Contains(messages[i].Content, "https://") || strings.Contains(messages[i].Content, "http://")
			break
		}
	}
	searchUses, fetchUses := search.MaxCalls/2, search.MaxCalls-search.MaxCalls/2
	if search.MaxCalls == 1 && !directURL {
		searchUses, fetchUses = 1, 0
	}
	if fetchUses > 0 {
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{OfWebFetchTool20260318: &anthropic.WebFetchTool20260318Param{MaxUses: anthropic.Int(int64(fetchUses)), MaxContentTokens: anthropic.Int(16000), AllowedCallers: []string{"direct"}, Citations: anthropic.CitationsConfigParam{Enabled: anthropic.Bool(true)}}})
	}
	if searchUses > 0 {
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{OfWebSearchTool20260318: &anthropic.WebSearchTool20260318Param{MaxUses: anthropic.Int(int64(searchUses)), AllowedCallers: []string{"direct"}}})
	}

	opts := []option.RequestOption{option.WithMaxRetries(0)}
	if onDelta == nil {
		message, err := client.Messages.New(ctx, params, opts...)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("Anthropic 原生搜索请求失败: %w", err)
		}
		out, err := anthropicSearchResult(*message)
		if search.OnEvent != nil {
			for _, event := range out.Search.Events {
				if e := search.OnEvent(event); e != nil {
					return out, e
				}
			}
		}
		return out, err
	}
	stream := client.Messages.NewStreaming(ctx, params, opts...)
	defer stream.Close()
	var message anthropic.Message
	ended := false
	live := []SearchEvent{}
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return ChatResponse{}, err
		}
		if event.Type == "content_block_start" && event.ContentBlock.Type == "server_tool_use" {
			if event.ContentBlock.Name != "web_search" && event.ContentBlock.Name != "web_fetch" {
				return ChatResponse{}, errors.New("返回未授权的服务器工具")
			}
			e := SearchEvent{Type: event.ContentBlock.Name + "_started", ID: event.ContentBlock.ID, Status: "executing"}
			live = append(live, e)
			if search.OnEvent != nil {
				if err := search.OnEvent(e); err != nil {
					return ChatResponse{Search: SearchReport{Events: live}}, err
				}
			}
		}
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			if err := onDelta(event.Delta.Text); err != nil {
				return ChatResponse{Search: SearchReport{Events: live}}, err
			}
		}
		if event.Type == "message_stop" {
			ended = true
		}
	}
	out, resultErr := anthropicSearchResult(message)
	out.Search.Events = append(live, out.Search.Events...)
	if err := stream.Err(); err != nil {
		out.Search.Calls = nil
		return out, fmt.Errorf("Anthropic 原生搜索流失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		out.Search.Calls = nil
		return out, err
	}
	if !ended {
		out.Search.Calls = nil
		return out, errors.New("Anthropic 搜索流缺少 message_stop")
	}
	return out, resultErr
}
