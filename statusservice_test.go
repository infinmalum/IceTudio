package main

import (
	"context"
	"icetudio/internal/testfixture"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// regression: model config saving round-trips through the binding surface and
// preserves unrelated configuration.
func TestSaveLMStudioAndReadStatus(t *testing.T) {
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalog, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mcptools.NewLocal(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	runs := agent.NewRuns(store)
	defer runs.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	source := config.DefaultModel("ollama")
	source.Name = "local-model"
	service := app.New(app.Options{
		Store: store, Runs: runs, Tools: registry, Skills: catalog, Chat: model.ChatClient{},
		Config:     config.Config{Model: source, MCPServers: []config.MCPServer{{Name: "example", Command: "/usr/bin/example"}}},
		ConfigPath: path,
		Discovery: model.Discovery{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/v1/models" {
				t.Fatalf("wrong path: %s", req.URL.Path)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"local-model"}]}`)), Header: http.Header{}}, nil
		})}},
	})
	svc := &StatusService{svc: service}
	next := config.DefaultModel("lmstudio")
	next.Name = "local-model"
	got, err := svc.SaveModelConfig(next)
	if err != nil || !got.ModelConnected || got.Model.Provider != "lmstudio" || len(got.AvailableModels) != 1 {
		t.Fatalf("status: %+v, %v", got, err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.Model != next || len(saved.MCPServers) != 1 || saved.MCPServers[0].Name != "example" {
		t.Fatalf("persisted config: %+v, %v", saved, err)
	}
}

type finishedModel struct{}

func (finishedModel) Chat(context.Context, config.Model, []model.Message, []model.Tool) (model.ChatResponse, error) {
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "你好"}}, nil
}

// regression: a task runs to completion, a second is cancellable, and the
// transcript is stored. This drives the same service the Wails UI binds to.
func TestTaskPersistenceAndCancellation(t *testing.T) {
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalog, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mcptools.NewLocal(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	runs := agent.NewRuns(store)
	defer runs.Close()
	source := config.DefaultModel("lmstudio")
	source.Name = "test"
	svc := &StatusService{svc: app.New(app.Options{
		Store: store, Runs: runs, Tools: registry, Skills: catalog,
		Chat: finishedModel{}, Config: config.Config{Model: source}, ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	})}
	session, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "你好", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := svc.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task not completed: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	history, err := svc.ListMessages(session.ID)
	if err != nil || len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatalf("history: %+v, %v", history, err)
	}
	// A second task must be startable after the first one finished and its
	// result must persist alongside the first.
	run2, err := svc.StartConversationTask(session.ID, "再来一次", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		got, err := svc.GetRun(run2.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second task not completed: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	history, err = svc.ListMessages(session.ID)
	if err != nil || len(history) != 4 {
		t.Fatalf("history after second task: %+v, %v", history, err)
	}
}

func TestServiceResolvesContextFromSession(t *testing.T) {
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &StatusService{svc: app.New(app.Options{Store: store})}
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListMessages(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListRuns(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListToolCalls(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListMessages("missing"); err == nil {
		t.Fatal("missing session accepted")
	}
}
