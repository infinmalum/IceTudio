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

func TestLMStudioChatToolCall(t *testing.T) {
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "http://127.0.0.1:1234/v1/chat/completions" {
			t.Fatalf("wrong request: %s %s", req.Method, req.URL)
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Tools    []Tool    `json:"tools"`
			Stream   bool      `json:"stream"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "local-model" || len(body.Messages) != 1 || len(body.Tools) != 1 || body.Stream {
			t.Fatalf("bad body: %+v", body)
		}
		response := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":42}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: http.Header{}}, nil
	})}
	source := config.DefaultModel("lmstudio")
	source.Name = "local-model"
	tool := Tool{Type: "function"}
	tool.Function.Name = "read"
	tool.Function.Parameters = json.RawMessage(`{"type":"object"}`)
	got, err := (ChatClient{Client: client}).Chat(context.Background(), source, []Message{{Role: "user", Content: "Read README"}}, []Tool{tool})
	if err != nil || got.FinishReason != "tool_calls" || len(got.Message.ToolCalls) != 1 || got.Message.ToolCalls[0].Function.Name != "read" || got.Usage.TotalTokens != 42 {
		t.Fatalf("answer: %+v, %v", got, err)
	}
}

func TestChatRequiresModelName(t *testing.T) {
	if _, err := (ChatClient{}).Chat(context.Background(), config.DefaultModel("lmstudio"), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("empty model name accepted")
	}
}
