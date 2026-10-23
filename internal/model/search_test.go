package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"icetudio/internal/config"
)

const searchResponseFixture = `{"id":"resp_search","object":"response","status":"completed","output":[{"id":"ws1","type":"web_search_call","status":"completed","action":{"type":"search","query":"test query","queries":["test query"]}},{"id":"rs1","type":"reasoning","summary":[],"encrypted_content":"opaque-state"},{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"有出处的回答","annotations":[{"type":"url_citation","url":"https://example.org/source","title":"来源","start_index":0,"end_index":6},{"type":"url_citation","url":"javascript:alert(1)","title":"拒绝","start_index":0,"end_index":1}]}]},{"id":"fn1","type":"function_call","call_id":"call1","name":"local__current_time","arguments":"{}"}],"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20}}`

func TestNativeSearchResponsesProtocolAndFunctionTools(t *testing.T) {
	requests := 0
	client, base := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Tools []struct {
				Type string
				Name string
			}
			Input        []json.RawMessage
			Store        bool
			MaxToolCalls int `json:"max_tool_calls"`
			Include      []string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Tools) != 2 || body.Tools[0].Type != "web_search" || body.Tools[1].Type != "function" || body.Store || body.MaxToolCalls != 2 {
			t.Errorf("bad request: %+v", body)
		}
		if requests == 2 {
			raw, _ := json.Marshal(body.Input)
			for _, want := range []string{"encrypted_content", "opaque-state", "function_call_output", "call1"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("missing %s: %s", want, raw)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, searchResponseFixture)
	})
	source := config.Model{Provider: "openai", BaseURL: base, Name: "gpt-4.1"}
	tool := Tool{Type: "function"}
	tool.Function.Name = "local__current_time"
	tool.Function.Parameters = nil // no-argument tools also coexist with search
	history := []Message{{Role: "system", Content: "助手"}, {Role: "user", Content: "查找资料"}}
	events := []SearchEvent{}
	resp, err := client.ChatWithSearch(context.Background(), source, history, []Tool{tool}, SearchOptions{MaxCalls: 2, OnEvent: func(e SearchEvent) error { events = append(events, e); return nil }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Search.Citations) != 1 || resp.Search.Calls == nil || *resp.Search.Calls != 1 || resp.Search.Cost != nil || len(resp.Message.ProviderState) != 1 || len(resp.Message.ToolCalls) != 1 || resp.Usage.TotalTokens != 20 || len(events) != 1 {
		t.Fatalf("bad response: %+v", resp)
	}
	history = append(history, resp.Message, Message{Role: "tool", ToolCallID: "call1", Content: "time"})
	if _, err := client.ChatWithSearch(context.Background(), source, history, []Tool{tool}, SearchOptions{MaxCalls: 2}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestNativeSearchStreamEvidenceAndTruncation(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			client, base := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.web_search_call.searching\",\"item_id\":\"ws1\",\"sequence_number\":1}\n\n")
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"有出处的回答\",\"output_index\":0,\"content_index\":0,\"sequence_number\":2}\n\n")
				if complete {
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"sequence_number\":3,\"response\":%s}\n\n", searchResponseFixture)
				}
			})
			source := config.Model{Provider: "openai", BaseURL: base, Name: "gpt-4.1"}
			var text strings.Builder
			events := 0
			resp, err := client.ChatWithSearch(context.Background(), source, []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 3, OnEvent: func(SearchEvent) error { events++; return nil }}, func(delta string) error { text.WriteString(delta); return nil })
			if (err == nil) != complete || events != 1 || text.String() != "有出处的回答" {
				t.Fatal(resp, err, events, text.String())
			}
			if complete && len(resp.Search.Citations) != 1 {
				t.Fatal(resp)
			}
			if !complete && resp.Search.Calls != nil {
				t.Fatal("truncated stream claimed known usage")
			}
		})
	}
}
func TestNativeSearchNoRetryOrUnsupportedRequest(t *testing.T) {
	calls := 0
	client, base := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) })
	source := config.Model{Provider: "openai", BaseURL: base, Name: "gpt-4.1"}
	_, err := client.ChatWithSearch(context.Background(), source, []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 3}, nil)
	if err == nil || calls != 1 {
		t.Fatal(err, calls)
	}
	source.Name = "unknown"
	_, err = client.ChatWithSearch(context.Background(), source, []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 3}, nil)
	if err == nil || calls != 1 {
		t.Fatal("unsupported model dispatched", err, calls)
	}
}
func TestNativeSearchCancellationAndEventPersistenceFailure(t *testing.T) {
	client, base := openaiTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.web_search_call.searching\",\"item_id\":\"ws1\",\"sequence_number\":1}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	for _, cancelled := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		_, err := client.ChatWithSearch(ctx, config.Model{Provider: "openai", BaseURL: base, Name: "gpt-4.1"}, []Message{{Role: "user", Content: "search"}}, nil, SearchOptions{MaxCalls: 1, OnEvent: func(SearchEvent) error {
			if cancelled {
				cancel()
				return nil
			}
			return errors.New("persistence failed")
		}}, func(string) error { return nil })
		cancel()
		if err == nil {
			t.Fatal("continued after cancellation/recording failure")
		}
	}
}
