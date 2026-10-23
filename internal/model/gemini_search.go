package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"
	"icetudio/internal/config"
)

func geminiSearchParams(source config.Model, messages []Message, tools []Tool, stream bool, enabled bool) (interactions.CreateModelInteraction, error) {
	var params interactions.CreateModelInteraction
	if err := validateRequest(source, messages); err != nil {
		return params, err
	}
	history := []any{}
	names := map[string]string{}
	for _, m := range messages {
		for _, call := range m.ToolCalls {
			names[call.ID] = call.Function.Name
		}
	}
	system := ""
	for _, m := range messages {
		switch m.Role {
		case "system":
			system += m.Content + "\n"
		case "user":
			history = append(history, map[string]any{"type": "user_input", "content": materialBlocks(m, "gemini")})
		case "assistant":
			if len(m.ProviderState) > 0 {
				for _, step := range m.ProviderState {
					history = append(history, step)
				}
				continue
			}
			if m.Content != "" {
				history = append(history, map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "text", "text": m.Content}}})
			}
			for _, call := range m.ToolCalls {
				var args map[string]any
				if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
					return params, err
				}
				history = append(history, map[string]any{"type": "function_call", "id": call.ID, "name": call.Function.Name, "arguments": args})
			}
		case "tool":
			if names[m.ToolCallID] == "" {
				return params, errors.New("Gemini 工具结果缺少对应调用")
			}
			history = append(history, map[string]any{"type": "function_result", "call_id": m.ToolCallID, "name": names[m.ToolCallID], "result": []any{map[string]any{"type": "text", "text": m.Content}}})
		default:
			return params, errors.New("不支持的消息角色")
		}
	}
	declared := []any{}
	if enabled {
		declared = append(declared, map[string]any{"type": "google_search"}, map[string]any{"type": "url_context"})
	}
	for _, tool := range providerToolsOf(tools) {
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.Parameters) > 0 {
			if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
				return params, err
			}
		}
		declared = append(declared, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema})
	}
	raw, err := json.Marshal(map[string]any{"model": geminiModelName(source.Name), "input": history, "system_instruction": system, "tools": declared, "store": false, "stream": stream, "background": false, "generation_config": map[string]any{"max_output_tokens": 8192}})
	if err != nil {
		return params, err
	}
	err = json.Unmarshal(raw, &params)
	return params, err
}

type geminiInteractionResult struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Steps  []json.RawMessage `json:"steps"`
	Usage  struct {
		Input   int `json:"total_input_tokens"`
		Output  int `json:"total_output_tokens"`
		Thought int `json:"total_thought_tokens"`
		Total   int `json:"total_tokens"`
	} `json:"usage"`
}

func geminiSearchResult(result geminiInteractionResult) (ChatResponse, error) {
	out := ChatResponse{Message: Message{Role: "assistant"}, FinishReason: "stop", Usage: Usage{PromptTokens: result.Usage.Input, CompletionTokens: result.Usage.Output + result.Usage.Thought, TotalTokens: result.Usage.Total}}
	out.Search.BudgetUnit = "grounded_request"
	for _, raw := range result.Steps {
		var step struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			CallID    string          `json:"call_id"`
			IsError   bool            `json:"is_error"`
			Error     json.RawMessage `json:"error"`
			Arguments json.RawMessage `json:"arguments"`
			Content   []struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Annotations []struct {
					Type  string `json:"type"`
					URL   string `json:"url"`
					Title string `json:"title"`
					Start int    `json:"start_index"`
					End   int    `json:"end_index"`
				} `json:"annotations"`
			} `json:"content"`
			Result []struct {
				Suggestions string `json:"search_suggestions"`
				URL         string `json:"url"`
				Status      string `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &step); err != nil {
			return out, err
		}
		out.Message.ProviderState = append(out.Message.ProviderState, raw)
		switch step.Type {
		case "model_output":
			if len(step.Error) > 0 && string(step.Error) != "null" {
				return out, errors.New("Gemini 返回模型输出错误")
			}
			for _, part := range step.Content {
				if part.Type != "text" {
					return out, errors.New("搜索响应包含未支持的输出类型")
				}
				offset := len([]rune(out.Message.Content))
				out.Message.Content += part.Text
				for _, citation := range part.Annotations {
					if citation.Type == "url_citation" && ValidCitationURL(citation.URL) {
						out.Search.Citations = append(out.Search.Citations, Citation{URL: citation.URL, Title: citation.Title, Start: offset + citation.Start, End: offset + citation.End})
					}
				}
			}
		case "google_search_call", "url_context_call":
			var args struct {
				Queries []string `json:"queries"`
				URLs    []string `json:"urls"`
			}
			if err := json.Unmarshal(step.Arguments, &args); err != nil {
				return out, err
			}
			out.Search.Events = append(out.Search.Events, SearchEvent{Type: step.Type, ID: step.ID, Queries: append(args.Queries, args.URLs...), Status: "completed"})
		case "google_search_result", "url_context_result":
			out.Search.Events = append(out.Search.Events, SearchEvent{Type: step.Type, ID: step.CallID, Status: "completed"})
			if step.IsError {
				return out, errors.New("Gemini Google Search 返回错误")
			}
			for _, item := range step.Result {
				if step.Type == "url_context_result" {
					out.Search.Events = append(out.Search.Events, SearchEvent{Type: "url_context_source", ID: step.CallID, URL: item.URL, Status: item.Status})
					if item.Status == "success" && ValidCitationURL(item.URL) {
						out.Search.Citations = append(out.Search.Citations, Citation{URL: item.URL, Title: item.URL})
					}
				}
				if item.Suggestions != "" {
					out.Search.Suggestions = append(out.Search.Suggestions, item.Suggestions)
				}
			}
		case "function_call":
			if step.ID == "" || step.Name == "" || !json.Valid(step.Arguments) {
				return out, errors.New("Gemini 函数调用不完整")
			}
			call := ToolCall{ID: step.ID, Type: "function"}
			call.Function.Name = step.Name
			call.Function.Arguments = string(step.Arguments)
			out.Message.ToolCalls = append(out.Message.ToolCalls, call)
		case "thought":
		default:
			return out, fmt.Errorf("Gemini 搜索返回未授权步骤 %q", step.Type)
		}
	}
	out.Search.Text = out.Message.Content
	// The API does not expose a maximum number of internal Google searches or
	// billed search-call count. Account for one host request; leave Calls unknown.
	one := 1
	out.Search.BudgetUsed = &one
	if result.Status == "requires_action" && len(out.Message.ToolCalls) > 0 {
		out.FinishReason = "tool_calls"
	} else if result.Status != "completed" {
		out.Search.BudgetUsed = nil
		return out, fmt.Errorf("Gemini 搜索未完成: %s", result.Status)
	}
	return out, nil
}

type geminiSearchStream struct {
	steps     map[int]map[string]any
	arguments map[int]string
	stopped   map[int]bool
	result    geminiInteractionResult
	terminal  bool
	events    []SearchEvent
}

func (s *geminiSearchStream) consume(raw []byte, search SearchOptions, onDelta func(string) error) error {
	var event struct {
		Type        string                  `json:"event_type"`
		Index       int                     `json:"index"`
		Step        map[string]any          `json:"step"`
		Delta       map[string]any          `json:"delta"`
		Interaction geminiInteractionResult `json:"interaction"`
		Metadata    struct {
			Usage json.RawMessage `json:"total_usage"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if len(event.Metadata.Usage) > 0 {
		if err := json.Unmarshal(event.Metadata.Usage, &s.result.Usage); err != nil {
			return err
		}
	}
	emit := func(e SearchEvent) error {
		s.events = append(s.events, e)
		if search.OnEvent != nil {
			return search.OnEvent(e)
		}
		return nil
	}
	switch event.Type {
	case "interaction.created":
		s.result.ID = event.Interaction.ID
	case "interaction.status_update", "interaction.completed":
		s.result.Status = event.Interaction.Status
		if len(event.Interaction.Steps) > 0 {
			s.result.Steps = event.Interaction.Steps
		}
		if event.Interaction.Usage.Total > 0 {
			s.result.Usage = event.Interaction.Usage
		}
		if event.Type == "interaction.completed" || event.Interaction.Status == "requires_action" {
			s.terminal = true
		}
	case "step.start":
		if event.Index < 0 || event.Index > 255 || event.Step == nil || s.steps[event.Index] != nil {
			return errors.New("Gemini 流步骤索引无效或重复")
		}
		s.steps[event.Index] = event.Step
		if event.Step["type"] == "google_search_call" || event.Step["type"] == "url_context_call" {
			id, _ := event.Step["id"].(string)
			return emit(SearchEvent{Type: "google_search_started", ID: id, Status: "executing"})
		}
	case "step.stop":
		if s.steps[event.Index] == nil {
			return errors.New("Gemini 流结束了未开始的步骤")
		}
		s.stopped[event.Index] = true
	case "step.delta":
		step := s.steps[event.Index]
		if step == nil || s.stopped[event.Index] {
			return errors.New("Gemini 流增量缺少活动步骤")
		}
		kind, _ := event.Delta["type"].(string)
		switch kind {
		case "text", "text_annotation_delta":
			if step["type"] != "model_output" {
				return errors.New("文本增量不属于模型输出")
			}
			parts, _ := step["content"].([]any)
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"type": "text", "text": ""})
			}
			part, ok := parts[len(parts)-1].(map[string]any)
			if !ok {
				return errors.New("Gemini 文本块无效")
			}
			if kind == "text" {
				text, _ := event.Delta["text"].(string)
				prior, _ := part["text"].(string)
				part["text"] = prior + text
				if err := onDelta(text); err != nil {
					return err
				}
			} else {
				annotations, _ := event.Delta["annotations"].([]any)
				prior, _ := part["annotations"].([]any)
				part["annotations"] = append(prior, annotations...)
			}
			step["content"] = parts
		case "arguments_delta":
			fragment, _ := event.Delta["arguments"].(string)
			s.arguments[event.Index] += fragment
		case "thought_signature":
			step["signature"] = event.Delta["signature"]
		case "thought_summary":
			parts, _ := step["summary"].([]any)
			if content, ok := event.Delta["content"]; ok {
				step["summary"] = append(parts, content)
			}
		case "google_search_call", "google_search_result", "url_context_call", "url_context_result":
			if step["type"] != kind {
				return errors.New("Gemini 搜索增量类型不匹配")
			}
			for key, value := range event.Delta {
				if key != "type" {
					step[key] = value
				}
			}
			if kind == "google_search_call" {
				args, _ := step["arguments"].(map[string]any)
				queries, _ := args["queries"].([]any)
				list := []string{}
				for _, q := range queries {
					if text, ok := q.(string); ok {
						list = append(list, text)
					}
				}
				id, _ := step["id"].(string)
				return emit(SearchEvent{Type: "google_search_queries", ID: id, Queries: list, Status: "executing"})
			}
		default:
			return fmt.Errorf("Gemini 搜索流包含未支持增量 %q", kind)
		}
	case "error":
		return errors.New("Gemini 搜索流返回错误事件")
	default:
		return fmt.Errorf("Gemini 搜索流包含未支持事件 %q", event.Type)
	}
	return nil
}
func (s *geminiSearchStream) response() (ChatResponse, error) {
	if len(s.result.Steps) == 0 {
		keys := []int{}
		for index := range s.steps {
			keys = append(keys, index)
		}
		sort.Ints(keys)
		for _, index := range keys {
			if !s.stopped[index] {
				return ChatResponse{Search: SearchReport{Events: s.events}}, errors.New("Gemini 流存在未结束步骤")
			}
			step := s.steps[index]
			if args, exists := s.arguments[index]; exists {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(args), &parsed); err != nil {
					return ChatResponse{}, err
				}
				step["arguments"] = parsed
			}
			raw, err := json.Marshal(step)
			if err != nil {
				return ChatResponse{}, err
			}
			s.result.Steps = append(s.result.Steps, raw)
		}
	}
	out, err := geminiSearchResult(s.result)
	out.Search.Events = append(s.events, out.Search.Events...)
	if !s.terminal {
		out.Search.BudgetUsed = nil
		return out, errors.New("Gemini 搜索流缺少终止事件")
	}
	return out, err
}
func (c Client) geminiChatWithSearch(ctx context.Context, source config.Model, messages []Message, tools []Tool, search SearchOptions, onDelta func(string) error) (ChatResponse, error) {
	return c.geminiInteractionChat(ctx, source, messages, tools, search, onDelta, true)
}
func (c Client) geminiInteractionChat(ctx context.Context, source config.Model, messages []Message, tools []Tool, search SearchOptions, onDelta func(string) error, enabled bool) (ChatResponse, error) {
	client, err := c.geminiClient(ctx, source)
	if err != nil {
		return ChatResponse{}, err
	}
	params, err := geminiSearchParams(source, messages, tools, onDelta != nil, enabled)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{Body: operations.NewCreateInteractionRequestBody(params)}, operations.WithRetries(retry.Config{Strategy: "none"}))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("Gemini 原生搜索请求失败: %w", err)
	}
	if onDelta == nil {
		if resp.Interaction == nil {
			return ChatResponse{}, errors.New("Gemini 搜索响应缺少 interaction")
		}
		raw, err := json.Marshal(resp.Interaction)
		if err != nil {
			return ChatResponse{}, err
		}
		var result geminiInteractionResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return ChatResponse{}, err
		}
		out, err := geminiSearchResult(result)
		if !enabled {
			if len(out.Search.Events) > 0 {
				return out, errors.New("未开启搜索却返回搜索执行步骤")
			}
			out.Search = SearchReport{}
		}
		if search.OnEvent != nil {
			for _, event := range out.Search.Events {
				if e := search.OnEvent(event); e != nil {
					return out, e
				}
			}
		}
		return out, err
	}
	stream := resp.InteractionSSEStreamEvent
	if stream == nil {
		return ChatResponse{}, errors.New("Gemini 搜索响应缺少事件流")
	}
	defer stream.Close()
	state := geminiSearchStream{steps: map[int]map[string]any{}, arguments: map[int]string{}, stopped: map[int]bool{}}
	for stream.Next() {
		raw, err := json.Marshal(stream.Value().Data)
		if err != nil {
			return ChatResponse{}, err
		}
		if err := state.consume(raw, search, onDelta); err != nil {
			return ChatResponse{Search: SearchReport{Events: state.events}}, err
		}
	}
	out, err := state.response()
	if !enabled {
		if len(out.Search.Events) > 0 {
			return out, errors.New("未开启搜索却返回搜索执行步骤")
		}
		out.Search = SearchReport{}
	}
	if stream.Err() != nil {
		out.Search.BudgetUsed = nil
		return out, fmt.Errorf("Gemini 搜索流失败: %w", stream.Err())
	}
	if ctx.Err() != nil {
		out.Search.BudgetUsed = nil
		return out, ctx.Err()
	}
	return out, err
}
