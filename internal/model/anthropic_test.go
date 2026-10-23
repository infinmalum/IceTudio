package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icetudio/internal/config"
)

func anthropicSource(base string) config.Model {
	return config.Model{Provider: "anthropic", BaseURL: base, Name: "claude-test"}
}

func anthropicTestClient(t *testing.T, handler http.HandlerFunc) (Client, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return Client{HTTP: server.Client(), Keys: func(provider string) (string, bool) {
		if provider == "anthropic" {
			return "ak-test", true
		}
		return "", false
	}}, server.URL
}

func TestAnthropicChatTextAndMultiTurn(t *testing.T) {
	calls := 0
	client, baseURL := anthropicTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "ak-test" {
			t.Errorf("x-api-key = %q", got)
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int64  `json:"max_tokens"`
			System    []struct {
				Text string `json:"text"`
			} `json:"system"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type      string          `json:"type"`
					Text      string          `json:"text"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					Content   json.RawMessage `json:"content"`
				} `json:"content"`
			} `json:"messages"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "claude-test" || body.MaxTokens <= 0 || len(body.System) != 1 || body.System[0].Text != "你是助手" {
			t.Fatalf("params: %+v", body)
		}
		if calls == 1 {
			if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content[0].Text != "你好" {
				t.Fatalf("turn 1: %+v", body.Messages)
			}
			if len(body.Tools) != 1 {
				t.Fatalf("tools: %s", body.Tools)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"content":[{"type":"text","text":"你好！"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":4}}`)
			return
		}
		// Multi-turn: assistant tool_use turn then a user turn carrying the
		// grouped tool_result blocks.
		if len(body.Messages) != 3 || body.Messages[1].Role != "assistant" || body.Messages[1].Content[0].Type != "tool_use" {
			t.Fatalf("turn 2 messages: %+v", body.Messages)
		}
		if body.Messages[1].Content[0].ID != "toolu_1" || body.Messages[1].Content[0].Name != "read" || string(body.Messages[1].Content[0].Input) != `{"path":"x"}` {
			t.Fatalf("tool_use block: %+v", body.Messages[1].Content[0])
		}
		last := body.Messages[2]
		var resultText string
		var resultBlocks []struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			ToolUseID string `json:"tool_use_id"`
		}
		if err := json.Unmarshal(last.Content[0].Content, &resultBlocks); err == nil && len(resultBlocks) > 0 {
			resultText = resultBlocks[0].Text
		}
		if last.Role != "user" || len(last.Content) != 1 || last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "toolu_1" || resultText != "文件内容" {
			t.Fatalf("tool_result turn: %+v", last)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"读取完成"}],"stop_reason":"end_turn","usage":{"input_tokens":22,"output_tokens":3}}`)
	})
	history := []Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
	}
	tool := Tool{Type: "function"}
	tool.Function.Name = "read"
	tool.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	first, err := client.Chat(context.Background(), anthropicSource(baseURL), history, []Tool{tool})
	if err != nil || first.Message.Content != "你好！" || first.Usage.PromptTokens != 11 || first.FinishReason != "stop" {
		t.Fatalf("turn 1: %+v %v", first, err)
	}
	second, err := client.Chat(context.Background(), anthropicSource(baseURL), append(history,
		Message{Role: "assistant", ToolCalls: []ToolCall{func() ToolCall {
			c := ToolCall{ID: "toolu_1", Type: "function"}
			c.Function.Name = "read"
			c.Function.Arguments = `{"path":"x"}`
			return c
		}()}},
		Message{Role: "tool", ToolCallID: "toolu_1", Content: "文件内容"},
	), nil)
	if err != nil || second.Message.Content != "读取完成" || calls != 2 {
		t.Fatalf("turn 2: %+v %v (calls=%d)", second, err, calls)
	}
}

func TestAnthropicToolUseMapping(t *testing.T) {
	client, baseURL := anthropicTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"我来读取。"},{"type":"tool_use","id":"toolu_2","name":"read","input":{"path":"a.md"}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":8}}`)
	})
	got, err := client.Chat(context.Background(), anthropicSource(baseURL), []Message{{Role: "user", Content: "读取 a.md"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Message.Content != "我来读取。" || got.FinishReason != "tool_calls" || len(got.Message.ToolCalls) != 1 {
		t.Fatalf("response: %+v", got)
	}
	if got.Message.ToolCalls[0].ID != "toolu_2" || got.Message.ToolCalls[0].Function.Name != "read" || got.Message.ToolCalls[0].Function.Arguments != `{"path":"a.md"}` {
		t.Fatalf("tool call: %+v", got.Message.ToolCalls[0])
	}
}

func TestAnthropicStream(t *testing.T) {
	var deltas []string
	client, baseURL := anthropicTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// The SDK's SSE decoder dispatches on the `event:` line, exactly
		// like the real Messages API stream.
		frames := [][2]string{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":9}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"分析"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"完成"}}`},
			{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_3","name":"read"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"y\"}"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}
		for _, frame := range frames {
			io.WriteString(w, "event: "+frame[0]+"\n")
			io.WriteString(w, "data: "+frame[1]+"\n\n")
		}
	})
	got, err := client.ChatStream(context.Background(), anthropicSource(baseURL), []Message{{Role: "user", Content: "读取 y"}}, nil, func(text string) error {
		deltas = append(deltas, text)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings.Join(deltas, "|") != "分析|完成" {
		t.Errorf("deltas = %q", deltas)
	}
	if got.Message.Content != "分析完成" || got.FinishReason != "tool_calls" || got.Usage.PromptTokens != 9 || got.Usage.CompletionTokens != 12 {
		t.Fatalf("merged: %+v", got)
	}
	if len(got.Message.ToolCalls) != 1 || got.Message.ToolCalls[0].ID != "toolu_3" || got.Message.ToolCalls[0].Function.Arguments != `{"path":"y"}` {
		t.Fatalf("tool calls: %+v", got.Message.ToolCalls)
	}
}

func TestAnthropicModelsAndMissingKey(t *testing.T) {
	client, baseURL := anthropicTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"claude-b"},{"id":"claude-a"}]}`)
	})
	models, err := client.List(context.Background(), anthropicSource(baseURL))
	if err != nil || len(models) != 2 || models[0] != "claude-a" {
		t.Fatalf("models: %v %v", models, err)
	}
	noKey := Client{Keys: func(string) (string, bool) { return "", false }}
	if _, err := noKey.Chat(context.Background(), anthropicSource(baseURL), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "未配置 Anthropic API 密钥") {
		t.Fatalf("missing key error = %v", err)
	}
}
