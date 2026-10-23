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

func geminiSource(base string) config.Model {
	return config.Model{Provider: "gemini", BaseURL: base, Name: "gemini-test"}
}

func geminiTestClient(t *testing.T, handler http.HandlerFunc) (Client, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return Client{HTTP: server.Client(), Keys: func(provider string) (string, bool) {
		if provider == "gemini" {
			return "gk-test", true
		}
		return "", false
	}}, server.URL
}

func TestGeminiChatTextAndMultiTurn(t *testing.T) {
	calls := 0
	client, baseURL := geminiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "gk-test" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		var body struct {
			SystemInstruction *struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						ID   string         `json:"id"`
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
					FunctionResponse *struct {
						ID       string         `json:"id"`
						Name     string         `json:"name"`
						Response map[string]any `json:"response"`
					} `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.SystemInstruction.Parts) != 1 || body.SystemInstruction.Parts[0].Text != "你是助手" {
			t.Fatalf("system: %+v", body.SystemInstruction)
		}
		if calls == 1 {
			if len(body.Contents) != 1 || body.Contents[0].Role != "user" || body.Contents[0].Parts[0].Text != "你好" {
				t.Fatalf("turn 1: %+v", body.Contents)
			}
			if len(body.Tools) != 1 {
				t.Fatalf("tools: %s", body.Tools)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"你好！"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,"totalTokenCount":14}}`)
			return
		}
		// Multi-turn: the model functionCall turn is replayed, followed by
		// the tool result as a functionResponse part with the recovered name.
		if len(body.Contents) != 3 || body.Contents[1].Role != "model" || body.Contents[1].Parts[0].FunctionCall == nil {
			t.Fatalf("turn 2 contents: %+v", body.Contents)
		}
		call := body.Contents[1].Parts[0].FunctionCall
		if call.Name != "read" || call.Args["path"] != "a.md" {
			t.Fatalf("functionCall: %+v", call)
		}
		resp := body.Contents[2].Parts[0].FunctionResponse
		if resp == nil || resp.Name != "read" || resp.Response["result"] != "文件内容" {
			t.Fatalf("functionResponse: %+v", resp)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"读取完成"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":3,"totalTokenCount":23}}`)
	})
	history := []Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
	}
	tool := Tool{Type: "function"}
	tool.Function.Name = "read"
	tool.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	first, err := client.Chat(context.Background(), geminiSource(baseURL), history, []Tool{tool})
	if err != nil || first.Message.Content != "你好！" || first.Usage.TotalTokens != 14 || first.FinishReason != "stop" {
		t.Fatalf("turn 1: %+v %v", first, err)
	}
	second, err := client.Chat(context.Background(), geminiSource(baseURL), append(history,
		Message{Role: "assistant", ToolCalls: []ToolCall{func() ToolCall {
			c := ToolCall{ID: "call_1", Type: "function"}
			c.Function.Name = "read"
			c.Function.Arguments = `{"path":"a.md"}`
			return c
		}()}},
		Message{Role: "tool", ToolCallID: "call_1", Content: "文件内容"},
	), nil)
	if err != nil || second.Message.Content != "读取完成" || calls != 2 {
		t.Fatalf("turn 2: %+v %v (calls=%d)", second, err, calls)
	}
}

func TestGeminiFunctionCallMapping(t *testing.T) {
	client, baseURL := geminiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"我来读取。"},{"functionCall":{"name":"read","args":{"path":"a.md"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":8,"totalTokenCount":13}}`)
	})
	got, err := client.Chat(context.Background(), geminiSource(baseURL), []Message{{Role: "user", Content: "读取 a.md"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Message.Content != "我来读取。" || got.FinishReason != "tool_calls" || len(got.Message.ToolCalls) != 1 {
		t.Fatalf("response: %+v", got)
	}
	if got.Message.ToolCalls[0].ID == "" || got.Message.ToolCalls[0].Function.Name != "read" || got.Message.ToolCalls[0].Function.Arguments != `{"path":"a.md"}` {
		t.Fatalf("tool call: %+v", got.Message.ToolCalls[0])
	}
}

func TestGeminiStream(t *testing.T) {
	var deltas []string
	client, baseURL := geminiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"分析\"}]}}]}\n\n")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"完成\"}]}}]}\n\n")
		io.WriteString(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"role\":\"model\",\"parts\":[]}}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":6,\"totalTokenCount\":15}}\n\n")
	})
	got, err := client.ChatStream(context.Background(), geminiSource(baseURL), []Message{{Role: "user", Content: "你好"}}, nil, func(text string) error {
		deltas = append(deltas, text)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings.Join(deltas, "|") != "分析|完成" {
		t.Errorf("deltas = %q", deltas)
	}
	if got.Message.Content != "分析完成" || got.FinishReason != "stop" || got.Usage.TotalTokens != 15 {
		t.Fatalf("merged: %+v", got)
	}
}

func TestGeminiModelsAndMissingKey(t *testing.T) {
	client, baseURL := geminiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"name":"models/gemini-b"},{"name":"models/gemini-a"}]}`)
	})
	models, err := client.List(context.Background(), geminiSource(baseURL))
	if err != nil || len(models) != 2 || models[0] != "gemini-a" || models[1] != "gemini-b" {
		t.Fatalf("models: %v %v", models, err)
	}
	noKey := Client{Keys: func(string) (string, bool) { return "", false }}
	if _, err := noKey.Chat(context.Background(), geminiSource(baseURL), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "未配置 Gemini API 密钥") {
		t.Fatalf("missing key error = %v", err)
	}
}
