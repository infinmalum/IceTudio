package app

import (
	"context"
	"encoding/json"
	"icetudio/internal/testfixture"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func TestConversationWithHTTPModelService(t *testing.T) {
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string          `json:"model"`
			Messages []model.Message `json:"messages"`
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if len(body.Messages) == 0 || body.Model != "component-model" {
			http.Error(w, "wrong model", 400)
			return
		}
		requests <- body.Messages[0].Content
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"HTTP 模拟响应\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	svc, _ := newTestService(t, model.Client{HTTP: server.Client()}, &scriptTools{})
	data := storage.BlankRuntimeConfig()
	data.Execution.Main.Model = &config.Model{Provider: "lmstudio", BaseURL: server.URL + "/v1", Name: "component-model"}
	data.Execution.Main.SystemPrompt = "HTTP component prompt"
	if _, err := svc.configureDefault(data); err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(c.ID, "write", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	if got := <-requests; !strings.Contains(got, "HTTP component prompt") {
		t.Fatal("model override lost", got)
	}
	history, err := svc.ListMessages(c.ID)
	if err != nil || len(history) != 2 || history[1].Content != "HTTP 模拟响应" {
		t.Fatalf("protocol result: %+v %v", history, err)
	}
}

func TestConfigFirstTaskUsesComponentAndAuthorizedTool(t *testing.T) {
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func(messages []model.Message) (model.ChatResponse, error) {
			// The component override leads the prompt; host runtime capabilities
			// may follow it without replacing the user configuration.
			if !strings.HasPrefix(messages[0].Content, "component prompt") {
				t.Errorf("component prompt not used: %s", messages[0].Content)
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("time-1")}}}, nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if messages[len(messages)-1].Content != "12:00" {
				t.Error("tool result not returned")
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "现在是 12 点"}}, nil
		},
	}}
	tools := &scriptTools{defs: timeToolDef(), respond: func(_, _ string) (string, error) { return "12:00", nil }}
	svc, _ := newTestService(t, chat, tools)
	data := storage.BlankRuntimeConfig()
	data.EnabledMCPServers = []string{"local"}
	data.Execution.Main.SystemPrompt = "component prompt"
	if _, err := svc.configureDefault(data); err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(c.ID, "现在几点？", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	if len(tools.calls) != 1 || run.Component != "main" {
		t.Fatalf("execution: %+v %v", run, tools.calls)
	}
}

func TestChildTranscriptCannotStartOrdinaryTask(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	child, err := store.CreateDelegationSession(context.Background(), testfixture.ContextID, storage.DefaultConfigID, "child")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartConversationTask(child.ID, "read target memory", nil); err == nil {
		t.Fatal("child transcript admitted an ordinary root run")
	}
}
