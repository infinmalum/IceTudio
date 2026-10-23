package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"icetudio/internal/testfixture"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// chatScript answers turns; turn 1 requests an external tool call so the
// default semi-auto policy requires a user approval.
type chatScript struct {
	mu    sync.Mutex
	steps []func(messages []model.Message) (model.ChatResponse, error)
	next  int
}

func (m *chatScript) Chat(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.next >= len(m.steps) {
		return model.ChatResponse{}, errors.New("脚本用尽")
	}
	step := m.steps[m.next]
	m.next++
	return step(messages)
}

type blockingTools struct {
	defs    []model.Tool
	block   chan struct{}
	started chan string
}

func (t *blockingTools) Definitions(context.Context) ([]model.Tool, error) { return t.defs, nil }

func (t *blockingTools) Call(_ context.Context, name, args string) (string, error) {
	t.started <- name + " " + args
	<-t.block
	return "", context.Canceled
}

func externalDef() (defs []model.Tool) {
	definition := model.Tool{Type: "function"}
	definition.Function.Name = "extsvc__ping"
	return []model.Tool{definition}
}

func externalCall() model.ToolCall {
	call := model.ToolCall{ID: "call-1", Type: "function"}
	call.Function.Name = "extsvc__ping"
	call.Function.Arguments = "{}"
	return call
}

func newTestHost(t *testing.T, chat agent.ChatModel, tools agent.ToolExecutor) (*app.Service, *storage.Store) {
	t.Helper()
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "ipc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	catalog, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := agent.NewRuns(store)
	t.Cleanup(runs.Close)
	source := config.DefaultModel("lmstudio")
	source.Name = "test-model"
	svc := app.New(app.Options{
		Store: store, Runs: runs, Tools: tools, Skills: catalog, Chat: chat,
		Config: config.Config{Model: source},
	})
	return svc, store
}

func startServer(t *testing.T, svc *app.Service) string {
	t.Helper()
	dataDir := t.TempDir()
	server, err := Start(svc, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return dataDir
}

func mustConnect(t *testing.T, dataDir string) *Client {
	t.Helper()
	client, err := Connect(dataDir, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestHandshakeRequiresHostToken(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)

	// A client presenting the real token connects.
	client := mustConnect(t, dataDir)
	var status app.AppStatus
	if err := client.Call("status", nil, &status); err != nil {
		t.Fatalf("status call: %v", err)
	}
	client.Close()

	// A client with a wrong token is refused before any method runs: the
	// handshake fails and the connection is closed.
	conn, err := net.Dial("unix", filepath.Join(dataDir, SocketName))
	if err != nil {
		t.Fatal(err)
	}
	hello, _ := json.Marshal(Hello{Hello: "icetudio", Token: "wrong-token", Version: ProtocolVersion})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, _ := io.ReadAll(conn)
	if !strings.Contains(string(reply), "身份校验失败") {
		t.Fatalf("handshake reply: %q", reply)
	}
	conn.Close()
	// A data directory without a host has no socket at all.
	other := t.TempDir()
	if _, err := Connect(other, 200*time.Millisecond); err == nil {
		t.Fatal("connected to a directory without host")
	}
}

func TestRuntimeConfigsRoundtrip(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)
	client := mustConnect(t, dataDir)

	var configs []storage.RuntimeConfig
	if err := client.Call("configs.list", nil, &configs); err != nil {
		t.Fatalf("configs.list: %v", err)
	}
	if len(configs) == 0 || configs[0].ID != storage.DefaultConfigID {
		t.Fatalf("configs: %+v", configs)
	}
	var current storage.RuntimeConfig
	if err := client.Call("configs.get", map[string]any{"id": storage.DefaultConfigID}, &current); err != nil {
		t.Fatalf("configs.get: %v", err)
	}
	current.Data.Description = "CLI 更新"
	var updated storage.RuntimeConfig
	if err := client.Call("configs.update", map[string]any{"config": current}, &updated); err != nil {
		t.Fatalf("configs.update: %v", err)
	}
	if updated.Revision != current.Revision+1 || updated.Data.Description != "CLI 更新" {
		t.Fatalf("updated: %+v", updated)
	}
	if err := client.Call("configs.update", map[string]any{"config": current}, nil); err == nil {
		t.Fatal("stale revision accepted")
	}
	var restored storage.RuntimeConfig
	if err := client.Call("configs.restoreDefault", map[string]any{"revision": updated.Revision}, &restored); err != nil {
		t.Fatalf("configs.restoreDefault: %v", err)
	}
	if restored.Data.Description == "CLI 更新" {
		t.Fatal("default data not restored")
	}
	var conversation storage.Conversation
	if err := client.Call("conversations.create", map[string]any{"configId": restored.ID}, &conversation); err != nil {
		t.Fatal(err)
	}
}

// The acceptance scenario: one client (desktop) starts a run, the other
// (CLI) approves and cancels, and both read identical durable state.
func TestCrossClientApprovalAndCancel(t *testing.T) {
	chat := &chatScript{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			call := model.ToolCall{ID: "discover", Type: "function"}
			call.Function.Name = "local__tool_search"
			call.Function.Arguments = `{"query":"extsvc__ping"}`
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{externalCall()}}}, nil
		},
	}}
	block := make(chan struct{})
	tools := &blockingTools{defs: externalDef(), block: block, started: make(chan string, 1)}
	svc, store := newTestHost(t, chat, tools)
	dataDir := startServer(t, svc)

	desktop := mustConnect(t, dataDir)
	cliClient := mustConnect(t, dataDir)

	var session storage.Conversation
	if err := desktop.Call("conversations.create", map[string]any{"configId": storage.DefaultConfigID, "reuseSession": reuseDefaultConversation(t, svc)}, &session); err != nil {
		t.Fatalf("conversations.create: %v", err)
	}
	var run storage.Run
	if err := desktop.Call("conversations.start", map[string]any{"id": session.ID, "input": "ping 一下"}, &run); err != nil {
		t.Fatalf("conversations.start: %v", err)
	}

	// The CLI client sees the pending approval and approves it.
	var calls []storage.ToolCall
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := cliClient.Call("calls.list", map[string]any{"sessionId": session.ID}, &calls); err != nil {
			t.Fatalf("calls.list: %v", err)
		}
		if len(calls) == 2 && calls[1].State == storage.CallPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending call never appeared: %+v", calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cliClient.Call("calls.approve", map[string]any{"callId": calls[1].ID}, nil); err != nil {
		t.Fatalf("calls.approve: %v", err)
	}

	// The tool starts executing and blocks; the CLI cancels the run.
	select {
	case name := <-tools.started:
		if name != "extsvc__ping {}" {
			t.Fatalf("tool call: %q", name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tool never started")
	}
	if err := cliClient.Call("tasks.cancel", map[string]any{"runId": run.ID}, nil); err != nil {
		t.Fatalf("tasks.cancel: %v", err)
	}
	close(block)

	// Both clients read the same final, durable state.
	for _, client := range []*Client{desktop, cliClient} {
		var final storage.Run
		seen := false
		readDeadline := time.Now().Add(3 * time.Second)
		for !seen && time.Now().Before(readDeadline) {
			if err := client.Call("runs.get", map[string]any{"runId": run.ID}, &final); err != nil {
				t.Fatalf("runs.get: %v", err)
			}
			if final.Status != "running" {
				seen = true
				continue
			}
			time.Sleep(10 * time.Millisecond)
		}
		if final.Status == "running" {
			t.Fatalf("run still running")
		}
		var messages []storage.Message
		if err := client.Call("sessions.messages", map[string]any{"sessionId": session.ID}, &messages); err != nil {
			t.Fatalf("sessions.messages: %v", err)
		}
		if len(messages) == 0 || messages[0].Role != storage.RoleUser {
			t.Fatalf("messages: %+v", messages)
		}
		var unknown []storage.ToolCall
		if err := client.Call("calls.list", map[string]any{"sessionId": session.ID}, &unknown); err != nil {
			t.Fatalf("calls.list after cancel: %v", err)
		}
		if len(unknown) != 2 || unknown[1].State != storage.CallExecutionUnknown {
			t.Fatalf("call state after cancel: %+v", unknown)
		}
	}
	_ = store
}

func TestEventsBroadcastToClients(t *testing.T) {
	chat := &chatScript{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "完成"}}, nil
		},
	}}
	tools := &blockingTools{defs: externalDef(), block: make(chan struct{}), started: make(chan string, 1)}
	svc, _ := newTestHost(t, chat, tools)
	dataDir := startServer(t, svc)

	client := mustConnect(t, dataDir)
	var mu sync.Mutex
	var events []string
	client.OnEvent(func(evt app.Event) {
		mu.Lock()
		events = append(events, evt.Type)
		mu.Unlock()
	})

	var session storage.Conversation
	if err := client.Call("conversations.create", map[string]any{"configId": storage.DefaultConfigID, "reuseSession": reuseDefaultConversation(t, svc)}, &session); err != nil {
		t.Fatalf("conversations.create: %v", err)
	}
	if err := client.Call("conversations.start", map[string]any{"id": session.ID, "input": "你好"}, nil); err != nil {
		t.Fatalf("conversations.start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		seen := len(events) > 0 && events[len(events)-1] == "run.updated"
		mu.Unlock()
		if seen {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("events: %v", events)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	hasStarted := false
	for _, evt := range events {
		if evt == "run.started" {
			hasStarted = true
		}
	}
	mu.Unlock()
	if !hasStarted {
		t.Fatalf("run.started missing in %v", events)
	}
}

func TestUnknownMethodRejected(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)
	client := mustConnect(t, dataDir)
	if err := client.Call("agents.dangerousAction", nil, nil); err == nil || !strings.Contains(err.Error(), "未知 IPC 方法") {
		t.Fatalf("unknown method accepted: %v", err)
	}
}

// The knowledge surface is reachable over IPC with the same ownership
// checks the desktop UI goes through: summary/plan reads report their
// absence as errors, and foreign sessions are refused.
func TestSummaryAndPlanOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)
	client := mustConnect(t, dataDir)

	session, err := svc.CreateConversation(storage.DefaultConfigID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Call("summaries.get", map[string]any{"sessionId": session.ID}, nil); err == nil {
		t.Fatal("absent summary accepted")
	}
	if err := client.Call("plans.get", map[string]any{"sessionId": session.ID}, nil); err == nil {
		t.Fatal("absent plan accepted")
	}
	var plan app.SessionPlanView
	if err := client.Call("plans.get", map[string]any{"sessionId": "no-such"}, &plan); err == nil {
		t.Fatal("foreign session plan accepted")
	}
	if err := client.Call("plans.clear", map[string]any{"sessionId": session.ID}, nil); err != nil {
		t.Fatalf("plans.clear: %v", err)
	}
}

// runs.children exposes the delegation task tree over IPC.
func TestChildRunsAndApprovalsOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)
	client := mustConnect(t, dataDir)

	// runs.children on a run-less id is refused; the tree surface exists.
	if err := client.Call("runs.children", map[string]any{"runId": "missing"}, nil); err == nil {
		t.Fatal("runs.children accepted an unknown run")
	}
	var pending []storage.ToolCall
	if err := client.Call("approvals.pending", map[string]any{"contextId": testfixture.ContextID}, &pending); err != nil {
		t.Fatalf("approvals.pending: %v", err)
	}
}

func reuseDefaultConversation(t *testing.T, svc *app.Service) string {
	t.Helper()
	session, err := svc.CreateConversation(storage.DefaultConfigID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	return session.ID
}

func TestResourceIPC(t *testing.T) {
	svc, store := newTestHost(t, &chatScript{}, &blockingTools{})
	client := mustConnect(t, startServer(t, svc))
	ctx := context.Background()
	var contexts []string
	var calls []storage.ToolCall
	for _, title := range []string{"first", "second"} {
		c, err := svc.CreateConversation(storage.DefaultConfigID, "", title)
		if err != nil {
			t.Fatal(err)
		}
		contexts = append(contexts, c.ContextID)
		run, _, err := store.CreateRun(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, created, err := store.AddAssistantMessage(ctx, c.ID, run.ID, "", []storage.MessageToolCall{{ID: title, Name: "local__read_file", Arguments: `{"path":"example"}`}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, created[0])
		var found storage.Run
		if err := client.Call("runs.get", map[string]any{"runId": run.ID}, &found); err != nil || found.ContextID != c.ContextID {
			t.Fatalf("resource identity: %+v %v", found, err)
		}
		if err := client.Call("runs.get", map[string]any{"runId": run.ID, "contextId": c.ContextID}, nil); err == nil {
			t.Fatal("redundant identity accepted")
		}
	}
	var pending []storage.ToolCall
	if err := client.Call("approvals.pending", nil, &pending); err != nil || len(pending) != 2 {
		t.Fatal(pending, err)
	}
	if err := client.Call("approvals.pending", map[string]any{"contextId": contexts[0]}, &pending); err != nil || len(pending) != 1 || pending[0].ID != calls[0].ID {
		t.Fatal(pending, err)
	}
	if err := client.Call("calls.approve", map[string]any{"callId": calls[0].ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("approvals.pending", nil, &pending); err != nil || len(pending) != 1 || pending[0].ID != calls[1].ID {
		t.Fatal(pending, err)
	}
}
