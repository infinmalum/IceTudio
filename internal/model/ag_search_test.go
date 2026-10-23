package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"icetudio/internal/config"
)

const anthropicSearchFixture = `{"id":"msg_s","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"server_tool_use","id":"srv1","name":"web_search","input":{"query":"test query"}},{"type":"web_search_tool_result","tool_use_id":"srv1","content":[{"type":"web_search_result","url":"https://example.org/a","title":"来源 A","encrypted_content":"opaque-evidence"}]},{"type":"text","text":"回答 A","citations":[{"type":"web_search_result_location","url":"https://example.org/a","title":"来源 A","encrypted_index":"opaque-index","cited_text":"source"}]},{"type":"tool_use","id":"call1","name":"local__current_time","input":{}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5,"server_tool_use":{"web_search_requests":1}}}`
const geminiSearchFixture = `{"id":"g_s","status":"requires_action","model":"gemini-3.1-pro-preview","steps":[{"type":"thought","signature":"opaque-signature","summary":[]},{"type":"google_search_call","id":"gs1","arguments":{"queries":["test query"]}},{"type":"google_search_result","call_id":"gs1","result":[{"search_suggestions":"<a href=\"https://www.google.com/search?q=test\">Google 搜索建议</a>"}]},{"type":"model_output","content":[{"type":"text","text":"回答 G","annotations":[{"type":"url_citation","url":"https://example.org/g","title":"来源 G","start_index":0,"end_index":4}]}]},{"type":"function_call","id":"call1","name":"local__current_time","arguments":{}}],"usage":{"total_input_tokens":10,"total_output_tokens":5,"total_tokens":15}}`
const geminiPlainFixture = `{"id":"g_end","status":"completed","model":"gemini-3.1-pro-preview","steps":[{"type":"model_output","content":[{"type":"text","text":"完成"}]}],"usage":{"total_input_tokens":10,"total_output_tokens":5,"total_tokens":15}}`

func nativeSource(provider, base string) config.Model {
	name := "claude-sonnet-4-5"
	if provider == "gemini" {
		name = "gemini-3.1-pro-preview"
	}
	return config.Model{Provider: provider, BaseURL: base, Name: name}
}
func TestAGNativeSearchProtocolStateAndSwitchOff(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			requests := 0
			handler := func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				tools, _ := body["tools"].([]any)
				raw, _ := json.Marshal(body)
				if provider == "anthropic" {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "ak-test" {
						t.Error("incorrect Anthropic endpoint/auth")
					}
					if requests == 1 {
						if len(tools) != 3 {
							t.Errorf("missing tools: %s", raw)
						}
						search := tools[len(tools)-1].(map[string]any)
						if search["type"] != "web_search_20260318" || search["max_uses"] != float64(1) || fmt.Sprint(search["allowed_callers"]) != "[direct]" {
							t.Errorf("unsafe search config: %s", raw)
						}
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, anthropicSearchFixture)
						return
					}
					if strings.Contains(string(raw), "web_search_20260318") || !strings.Contains(string(raw), "opaque-evidence") || !strings.Contains(string(raw), "tool_result") {
						t.Errorf("disabled/replay request wrong: %s", raw)
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"msg_end","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"完成"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`)
				} else {
					if r.URL.Path != "/v1beta/interactions" || r.Header.Get("x-goog-api-key") != "gk-test" {
						t.Errorf("incorrect Gemini endpoint/auth %s", r.URL.Path)
					}
					if body["store"] != false || body["background"] != false {
						t.Errorf("server state enabled: %s", raw)
					}
					if requests == 1 {
						if len(tools) != 3 || tools[0].(map[string]any)["type"] != "google_search" || tools[1].(map[string]any)["type"] != "url_context" {
							t.Errorf("missing tools: %s", raw)
						}
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, geminiSearchFixture)
						return
					}
					if len(tools) != 1 || tools[0].(map[string]any)["type"] != "function" || !strings.Contains(string(raw), "opaque-signature") || !strings.Contains(string(raw), "function_result") {
						t.Errorf("disabled/replay request wrong: %s", raw)
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, geminiPlainFixture)
				}
			}
			var client Client
			var base string
			if provider == "anthropic" {
				client, base = anthropicTestClient(t, handler)
			} else {
				client, base = geminiTestClient(t, handler)
			}
			source := nativeSource(provider, base)
			tool := Tool{Type: "function"}
			tool.Function.Name = "local__current_time"
			history := []Message{{Role: "system", Content: "助手"}, {Role: "user", Content: "检索"}}
			response, err := client.ChatWithSearch(context.Background(), source, history, []Tool{tool}, SearchOptions{MaxCalls: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Search.Citations) != 1 || len(response.Message.ToolCalls) != 1 || len(response.Message.ProviderState) == 0 || response.Usage.TotalTokens != 15 {
				t.Fatalf("bad result: %+v", response)
			}
			if provider == "anthropic" && (response.Search.Calls == nil || *response.Search.Calls != 1) {
				t.Fatal(response.Search)
			}
			if provider == "gemini" && (response.Search.Calls != nil || response.Search.BudgetUsed == nil || *response.Search.BudgetUsed != 1 || len(response.Search.Suggestions) != 1) {
				t.Fatal(response.Search)
			}
			history = append(history, response.Message, Message{Role: "tool", ToolCallID: "call1", Content: "now"})
			response, err = client.Chat(context.Background(), source, history, []Tool{tool})
			if err != nil || response.Message.Content != "完成" {
				t.Fatal(response, err)
			}
		})
	}
}
func writeAnthropicSearchStream(w http.ResponseWriter, complete bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	events := []string{
		`{"type":"message_start","message":{"id":"msg_s","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srv1","name":"web_search","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"test query\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srv1","content":[{"type":"web_search_result","url":"https://example.org/a","title":"来源 A","encrypted_content":"opaque-evidence"}]}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"回答 A"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.org/a","title":"来源 A","encrypted_index":"opaque-index","cited_text":"source"}}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5,"server_tool_use":{"web_search_requests":1}}}`,
	}
	for _, event := range events {
		var frame struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &frame)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.Type, event)
	}
	if complete {
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
}
func writeGeminiSearchStream(w http.ResponseWriter, complete bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	events := []string{
		`{"event_type":"interaction.created","interaction":{"id":"g_s","status":"in_progress"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"google_search_call","id":"gs1"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"google_search_call","arguments":{"queries":["test query"]}}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"step.start","index":1,"step":{"type":"google_search_result","call_id":"gs1"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"google_search_result","result":[{"search_suggestions":"<a href=\"https://www.google.com/search?q=test\">Google</a>"}]}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"step.start","index":2,"step":{"type":"model_output"}}`,
		`{"event_type":"step.delta","index":2,"delta":{"type":"text","text":"回答 G"}}`,
		`{"event_type":"step.delta","index":2,"delta":{"type":"text_annotation_delta","annotations":[{"type":"url_citation","url":"https://example.org/g","title":"来源 G","start_index":0,"end_index":4}]}}`,
		`{"event_type":"step.stop","index":2}`,
	}
	for _, event := range events {
		fmt.Fprintf(w, "data: %s\n\n", event)
	}
	if complete {
		fmt.Fprint(w, "data: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"g_s\",\"status\":\"completed\",\"usage\":{\"total_input_tokens\":10,\"total_output_tokens\":5,\"total_tokens\":15}}}\n\n")
	}
}
func TestAGNativeSearchStreamingAndTruncation(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		for _, complete := range []bool{true, false} {
			t.Run(fmt.Sprint(provider, complete), func(t *testing.T) {
				handler := func(w http.ResponseWriter, r *http.Request) {
					if provider == "anthropic" {
						writeAnthropicSearchStream(w, complete)
					} else {
						writeGeminiSearchStream(w, complete)
					}
				}
				var client Client
				var base string
				if provider == "anthropic" {
					client, base = anthropicTestClient(t, handler)
				} else {
					client, base = geminiTestClient(t, handler)
				}
				var text strings.Builder
				events := 0
				response, err := client.ChatWithSearch(context.Background(), nativeSource(provider, base), []Message{{Role: "user", Content: "搜索"}}, nil, SearchOptions{MaxCalls: 3, OnEvent: func(SearchEvent) error { events++; return nil }}, func(delta string) error { text.WriteString(delta); return nil })
				if (err == nil) != complete || text.Len() == 0 || events == 0 {
					t.Fatal(response, err, text.String(), events)
				}
				if complete && provider == "anthropic" && (response.Search.Calls == nil || *response.Search.Calls != 1) {
					t.Fatal("missing streamed search usage", response.Search)
				}
				if complete && (len(response.Search.Citations) != 1 || response.Usage.TotalTokens != 15 || len(response.Message.ProviderState) == 0) {
					t.Fatal(response)
				}
				if !complete && response.Search.BudgetUsed != nil {
					t.Fatal("truncated stream reported known budget usage")
				}
			})
		}
	}
}
func TestAGNativeSearchNoRetryAndCancellation(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			requests := 0
			handler := func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(503) }
			var client Client
			var base string
			if provider == "anthropic" {
				client, base = anthropicTestClient(t, handler)
			} else {
				client, base = geminiTestClient(t, handler)
			}
			if _, err := client.ChatWithSearch(context.Background(), nativeSource(provider, base), []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 1}, nil); err == nil || requests != 1 {
				t.Fatal(err, requests)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := client.ChatWithSearch(ctx, nativeSource(provider, base), []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 1}, nil); err == nil || requests != 1 {
				t.Fatal("cancelled call dispatched", err, requests)
			}
		})
	}
}

func TestAGNativeSearchActiveCancelAndEventFailure(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprint(provider, failure), func(t *testing.T) {
				handler := func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					if provider == "anthropic" {
						fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
						fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"name\":\"web_search\",\"id\":\"s\",\"input\":{}}}\n\n")
					} else {
						fmt.Fprint(w, "data: {\"event_type\":\"step.start\",\"index\":0,\"step\":{\"type\":\"google_search_call\",\"id\":\"s\"}}\n\n")
					}
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
				var client Client
				var base string
				if provider == "anthropic" {
					client, base = anthropicTestClient(t, handler)
				} else {
					client, base = geminiTestClient(t, handler)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				_, err := client.ChatWithSearch(ctx, nativeSource(provider, base), []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 1, OnEvent: func(SearchEvent) error {
					if failure {
						return fmt.Errorf("recording failed")
					}
					cancel()
					return nil
				}}, func(string) error { return nil })
				if err == nil {
					t.Fatal("continued after cancel/persistence failure")
				}
			})
		}
	}
}
func TestAGNativeSearchRejectsFailedOrUnauthorizedResults(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if provider == "anthropic" {
					io.WriteString(w, strings.ReplaceAll(anthropicSearchFixture, `"stop_reason":"tool_use"`, `"stop_reason":"pause_turn"`))
				} else {
					io.WriteString(w, strings.ReplaceAll(geminiSearchFixture, `"type":"google_search_result","call_id"`, `"type":"google_search_result","is_error":true,"call_id"`))
				}
			}
			var client Client
			var base string
			if provider == "anthropic" {
				client, base = anthropicTestClient(t, handler)
			} else {
				client, base = geminiTestClient(t, handler)
			}
			if _, err := client.ChatWithSearch(context.Background(), nativeSource(provider, base), []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 1}, nil); err == nil {
				t.Fatal("failed/paused search was accepted")
			}
		})
	}
}
