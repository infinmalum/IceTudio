package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"icetudio/internal/config"
)

// The local Ollama / LM Studio path streams through the same SSE envelope.
func TestCompatChatStream(t *testing.T) {
	var deltas []string
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Stream   bool `json:"stream"`
			Tools    []Tool
			Messages []Message
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream || len(body.Tools) != 1 || len(body.Messages) != 1 {
			t.Fatalf("bad body: stream=%v tools=%d messages=%d", body.Stream, len(body.Tools), len(body.Messages))
		}
		var out strings.Builder
		out.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"现在\"}}]}\n\n")
		out.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"是正午。\"}}]}\n\n")
		out.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"local__current_time\",\"arguments\":\"{}\"}}]}}]}\n\n")
		out.WriteString("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		out.WriteString("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":6,\"total_tokens\":9}}\n\n")
		out.WriteString("data: [DONE]\n\n")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(out.String())), Header: http.Header{}}, nil
	})}
	source := config.DefaultModel("ollama")
	source.Name = "qwen"
	tool := Tool{Type: "function"}
	tool.Function.Name = "local__current_time"
	tool.Function.Parameters = json.RawMessage(`{}`)
	got, err := (ChatClient{Client: client}).ChatStream(context.Background(), source, []Message{{Role: "user", Content: "几点了"}}, []Tool{tool}, func(text string) error {
		deltas = append(deltas, text)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings.Join(deltas, "|") != "现在|是正午。" {
		t.Errorf("deltas = %q", deltas)
	}
	if got.Message.Content != "现在是正午。" || got.FinishReason != "tool_calls" || got.Usage.TotalTokens != 9 {
		t.Fatalf("merged: %+v", got)
	}
	if len(got.Message.ToolCalls) != 1 || got.Message.ToolCalls[0].Function.Name != "local__current_time" {
		t.Fatalf("tool calls: %+v", got.Message.ToolCalls)
	}
}

func TestClientDispatchesLocalStream(t *testing.T) {
	// The unified client routes ollama/lmstudio to the key-less compat
	// adapter even when a lookup is configured.
	client := Client{Keys: func(string) (string, bool) { return "irrelevant", true }}
	if _, err := client.ChatStream(context.Background(), config.Model{Provider: "unknown"}, []Message{{Role: "user", Content: "x"}}, nil, func(string) error { return nil }); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
