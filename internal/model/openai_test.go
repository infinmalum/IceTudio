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

func openaiSource(base string) config.Model {
	return config.Model{Provider: "openai", BaseURL: base, Name: "gpt-test"}
}

func openaiTestClient(t *testing.T, handler http.HandlerFunc) (Client, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return Client{HTTP: server.Client(), Keys: func(provider string) (string, bool) {
		if provider == "openai" {
			return "sk-test", true
		}
		return "", false
	}}, server.URL + "/v1"
}

func TestOpenAIChatTextAndMultiTurn(t *testing.T) {
	calls := 0
	client, baseURL := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "gpt-test" {
			t.Errorf("model = %q", body.Model)
		}
		if calls == 1 {
			if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" || body.Messages[1].Content != "你好" {
				t.Fatalf("turn 1 messages: %+v", body.Messages)
			}
			if len(body.Tools) != 1 {
				t.Fatalf("tools: %s", body.Tools)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"你好，有什么可以帮忙？"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
			return
		}
		// Multi-turn: the assistant reply and the tool result are replayed.
		if len(body.Messages) != 4 || body.Messages[2].Role != "assistant" || body.Messages[3].Role != "tool" || body.Messages[3].ToolCallID != "call_1" {
			t.Fatalf("turn 2 messages: %+v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"读取完成"},"finish_reason":"stop"}]}`)
	})
	history := []Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
	}
	tool := Tool{Type: "function"}
	tool.Function.Name = "read"
	tool.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	first, err := client.Chat(context.Background(), openaiSource(baseURL), history, []Tool{tool})
	if err != nil || first.Message.Content != "你好，有什么可以帮忙？" || first.Usage.TotalTokens != 15 {
		t.Fatalf("turn 1: %+v %v", first, err)
	}
	call := ToolCall{ID: "call_1", Type: "function"}
	call.Function.Name = "read"
	call.Function.Arguments = "{}"
	second, err := client.Chat(context.Background(), openaiSource(baseURL), append(history,
		Message{Role: "assistant", ToolCalls: []ToolCall{call}},
		Message{Role: "tool", ToolCallID: "call_1", Content: "文件内容"},
	), nil)
	if err != nil || second.Message.Content != "读取完成" || calls != 2 {
		t.Fatalf("turn 2: %+v %v (calls=%d)", second, err, calls)
	}
}

func TestOpenAIChatToolCallAndStream(t *testing.T) {
	var deltas []string
	client, baseURL := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"，世界\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_9\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"x\\\"}\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":9,\"total_tokens\":16}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	})
	call := ToolCall{ID: "call_1", Type: "function"}
	call.Function.Name = "read"
	call.Function.Arguments = "{}"
	got, err := client.ChatStream(context.Background(), openaiSource(baseURL), []Message{
		{Role: "user", Content: "读取文件"},
		{Role: "assistant", ToolCalls: []ToolCall{call}},
		{Role: "tool", ToolCallID: "call_1", Content: "内容"},
	}, nil, func(text string) error {
		deltas = append(deltas, text)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings_ := strings.Join(deltas, "|"); strings_ != "你好|，世界" {
		t.Errorf("deltas = %q", strings_)
	}
	if got.Message.Content != "你好，世界" || got.FinishReason != "tool_calls" || got.Usage.TotalTokens != 16 {
		t.Fatalf("merged: %+v", got)
	}
	if len(got.Message.ToolCalls) != 1 || got.Message.ToolCalls[0].ID != "call_9" || got.Message.ToolCalls[0].Function.Arguments != `{"path":"x"}` {
		t.Fatalf("tool calls: %+v", got.Message.ToolCalls)
	}
}

func TestOpenAIModelsAndMissingKey(t *testing.T) {
	client, baseURL := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"gpt-b"},{"id":"gpt-a"}]}`)
	})
	models, err := client.List(context.Background(), openaiSource(baseURL))
	if err != nil || len(models) != 2 || models[0] != "gpt-a" || models[1] != "gpt-b" {
		t.Fatalf("models: %v %v", models, err)
	}
	// Without a credential the request must never leave: the handler above
	// only serves /models, so a chat attempt would fail with a path error.
	noKey := Client{Keys: func(string) (string, bool) { return "", false }}
	if _, err := noKey.Chat(context.Background(), openaiSource(baseURL), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "未配置 OpenAI API 密钥") {
		t.Fatalf("missing key error = %v", err)
	}
	if _, err := noKey.ChatStream(context.Background(), openaiSource(baseURL), []Message{{Role: "user", Content: "hi"}}, nil, func(string) error { return nil }); err == nil {
		t.Fatal("stream without key accepted")
	}
}

func TestOpenAIErrorMapping(t *testing.T) {
	client, baseURL := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	})
	if _, err := client.Chat(context.Background(), openaiSource(baseURL), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v", err)
	}
}
