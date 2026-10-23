package model

import (
	"context"
	"encoding/json"
	"fmt"
	"icetudio/internal/config"
	"io"
	"net/http"
	"strings"
	"testing"
)

const fetchFixture = `{"id":"msg_f","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"server_tool_use","id":"fetch1","name":"web_fetch","input":{"url":"https://example.org/page"}},{"type":"web_fetch_tool_result","tool_use_id":"fetch1","content":{"type":"web_fetch_result","url":"https://example.org/page","retrieved_at":"2026-10-15T00:00:00Z","content":{"type":"document","title":"Page","source":{"type":"text","media_type":"text/plain","data":"body"}}}},{"type":"text","text":"page answer"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":5,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":1}}}`
const urlFixture = `{"id":"url_i","status":"completed","steps":[{"type":"url_context_call","id":"url1","arguments":{"urls":["https://example.org/page"]}},{"type":"url_context_result","call_id":"url1","result":[{"url":"https://example.org/page","status":"success"}]},{"type":"model_output","content":[{"type":"text","text":"page answer"}]}],"usage":{"total_input_tokens":12,"total_output_tokens":5,"total_tokens":17}}`

func TestNativePageReadingHTTPAndStreams(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic", "gemini"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", provider, stream), func(t *testing.T) {
				fixture := urlFixture
				if provider == "anthropic" {
					fixture = fetchFixture
				}
				if provider == "openai" {
					fixture = strings.Replace(searchResponseFixture, `"type":"search","query":"test query","queries":["test query"]`, `"type":"open_page","url":"https://example.org/page"`, 1)
				}
				handler := func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					text := string(raw)
					required := "url_context"
					if provider == "anthropic" {
						required = "web_fetch_20260318"
					}
					if provider == "openai" {
						required = "web_search"
					}
					if !strings.Contains(text, required) || !strings.Contains(text, "https://example.org/page") {
						t.Errorf("missing URL/tool: %s", text)
					}
					if !stream {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, fixture)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if provider == "openai" {
						fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", fixture)
						return
					}
					if provider == "gemini" {
						fmt.Fprintf(w, "data: {\"event_type\":\"interaction.completed\",\"interaction\":%s}\n\n", fixture)
						return
					}
					var message map[string]any
					json.Unmarshal([]byte(fixture), &message)
					blocks := message["content"].([]any)
					message["content"] = []any{}
					message["stop_reason"] = nil
					start, _ := json.Marshal(map[string]any{"type": "message_start", "message": message})
					fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", start)
					for i, b := range blocks {
						raw, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": i, "content_block": b})
						fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", raw, i)
					}
					fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5,\"server_tool_use\":{\"web_search_requests\":0,\"web_fetch_requests\":1}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}
				var c Client
				var base string
				switch provider {
				case "openai":
					c, base = openaiTestClient(t, handler)
				case "anthropic":
					c, base = anthropicTestClient(t, handler)
				default:
					c, base = geminiTestClient(t, handler)
				}
				source := nativeSource(provider, base)
				if provider == "openai" {
					source = config.Model{Provider: provider, BaseURL: base, Name: "gpt-4.1"}
				}
				var delta func(string) error
				if stream {
					delta = func(string) error { return nil }
				}
				out, err := c.ChatWithSearch(context.Background(), source, []Message{{Role: "user", Content: "读取 https://example.org/page"}}, nil, SearchOptions{MaxCalls: 2}, delta)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, e := range out.Search.Events {
					if e.URL == "https://example.org/page" {
						found = true
					}
				}
				if !found {
					t.Fatalf("URL reading evidence lost: %+v", out.Search)
				}
				if provider == "anthropic" && (out.Search.Calls == nil || *out.Search.Calls != 1) {
					t.Fatalf("fetch budget lost: %+v", out.Search)
				}
			})
		}
	}
}
