package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func TestAGNativeSearchThroughHostGrantBudgetAndRecords(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		for _, grant := range []bool{true, false} {
			t.Run(fmt.Sprint(provider, grant), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					if provider == "anthropic" {
						events := []string{
							`{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
							`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","name":"web_search","id":"s","input":{"query":"测试"}}}`,
							`{"type":"content_block_stop","index":0}`,
							`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
							`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"回答"}}`,
							`{"type":"content_block_delta","index":1,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.org/a","title":"来源","encrypted_index":"opaque","cited_text":"source"}}}`,
							`{"type":"content_block_stop","index":1}`,
							`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5,"server_tool_use":{"web_search_requests":1}}}`,
							`{"type":"message_stop"}`,
						}
						for _, event := range events {
							var frame struct {
								Type string `json:"type"`
							}
							_ = json.Unmarshal([]byte(event), &frame)
							fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.Type, event)
						}
					} else {
						io.WriteString(w, "data: {\"event_type\":\"step.start\",\"index\":0,\"step\":{\"type\":\"google_search_call\",\"id\":\"s\",\"arguments\":{\"queries\":[\"测试\"]}}}\n\n")
						io.WriteString(w, "data: {\"event_type\":\"step.stop\",\"index\":0}\n\n")
						io.WriteString(w, "data: {\"event_type\":\"step.start\",\"index\":1,\"step\":{\"type\":\"model_output\"}}\n\n")
						io.WriteString(w, "data: {\"event_type\":\"step.delta\",\"index\":1,\"delta\":{\"type\":\"text\",\"text\":\"回答\"}}\n\n")
						io.WriteString(w, "data: {\"event_type\":\"step.delta\",\"index\":1,\"delta\":{\"type\":\"text_annotation_delta\",\"annotations\":[{\"type\":\"url_citation\",\"url\":\"https://example.org/g\",\"title\":\"来源\",\"start_index\":0,\"end_index\":2}]}}\n\n")
						io.WriteString(w, "data: {\"event_type\":\"step.stop\",\"index\":1}\n\n")
						io.WriteString(w, "data: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"g_end\",\"status\":\"completed\",\"usage\":{\"total_input_tokens\":10,\"total_output_tokens\":5,\"total_tokens\":15}}}\n\n")
					}
				}))
				defer server.Close()
				client := model.Client{HTTP: server.Client(), Keys: func(string) (string, bool) { return "test-key", true }}
				s, _ := newTestService(t, client, &scriptTools{})
				name := "claude-sonnet-4-5"
				if provider == "gemini" {
					name = "gemini-3.1-pro-preview"
				}
				s.mu.Lock()
				s.config.Model = config.Model{Provider: provider, Name: name, BaseURL: server.URL}
				s.mu.Unlock()
				c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
				if err != nil {
					t.Fatal(err)
				}
				if grant {
					if _, err := s.GrantExfil(c.ContextID, server.URL, []string{storage.ScopePersonal, storage.ScopeUnknown}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.SetConversationOptions(c.ID, storage.ConversationOptions{WebSearch: true}); err != nil {
					t.Fatal(err)
				}
				run, err := s.StartConversationTask(c.ID, "搜索")
				if err != nil {
					t.Fatal(err)
				}
				want := "completed"
				if !grant {
					want = "failed"
				}
				finished := waitForRun(t, s, run.ID, want)
				records, err := s.ListConversationSearch(c.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !grant {
					if requests.Load() != 0 || len(records) != 0 || !strings.Contains(finished.Error, "外发") {
						t.Fatal(requests.Load(), records, finished)
					}
					return
				}
				if requests.Load() != 1 || len(records) != 1 || records[0].State != "completed" || len(records[0].Report.Citations) != 1 || len(records[0].Report.Events) == 0 {
					t.Fatal(requests.Load(), records, finished)
				}
				if provider == "gemini" && (records[0].Reserved != 1 || records[0].Report.Calls != nil || records[0].Report.BudgetUnit != "grounded_request") {
					t.Fatal(records)
				}
				graph, err := s.GetTaskGraph(run.ID)
				if err != nil || graph.Budget.ToolCalls != 1 {
					t.Fatal(graph.Budget, err)
				}
				messages, err := s.ListMessages(c.ID)
				if err != nil || len(messages) != 2 || messages[1].Content != "回答" {
					t.Fatal(messages, err)
				}
			})
		}
	}
}
