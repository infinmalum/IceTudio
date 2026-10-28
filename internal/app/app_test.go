package app

import (
	"context"
	"errors"
	"icetudio/internal/testfixture"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// scriptModel answers according to a script and records every history it is
// shown, so tests can assert the exact structured context the model sees.
type scriptModel struct {
	steps []func(messages []model.Message) (model.ChatResponse, error)
	seen  [][]model.Message
	next  int
}

func (m *scriptModel) Chat(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	shown := make([]model.Message, len(messages))
	copy(shown, messages)
	m.seen = append(m.seen, shown)
	if m.next >= len(m.steps) {
		return model.ChatResponse{}, errors.New("模型脚本用尽")
	}
	step := m.steps[m.next]
	m.next++
	return step(messages)
}

type scriptTools struct {
	defs    []model.Tool
	respond func(name, args string) (string, error)
	calls   []string
}

func (t *scriptTools) Definitions(context.Context) ([]model.Tool, error) { return t.defs, nil }

func (t *scriptTools) Call(_ context.Context, name, args string) (string, error) {
	t.calls = append(t.calls, name+" "+args)
	return t.respond(name, args)
}

func timeToolDef() (defs []model.Tool) {
	definition := model.Tool{Type: "function"}
	definition.Function.Name = "local__current_time"
	return []model.Tool{definition}
}

func toolCall(id string) model.ToolCall {
	call := model.ToolCall{ID: id, Type: "function"}
	call.Function.Name = "local__current_time"
	call.Function.Arguments = "{}"
	return call
}

func newTestService(t *testing.T, chat agent.ChatModel, tools agent.ToolExecutor) (*Service, *storage.Store) {
	t.Helper()
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "app.db"))
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
	svc := New(Options{
		Store: store, Runs: runs, Tools: tools, Skills: catalog, Chat: chat,
		Config: config.Config{Model: source}, ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	})
	return svc, store
}

func waitForRun(t *testing.T, svc *Service, runID, wantStatus string) storage.Run {
	t.Helper()
	svc.WaitIdle()
	run, err := svc.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != wantStatus {
		t.Fatalf("run status = %q (%s), want %q", run.Status, run.Error, wantStatus)
	}
	return run
}

func TestTaskWithToolCallPersistsStructuredHistory(t *testing.T) {
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("call-1")}}}, nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if n := len(messages); n != 4 || messages[1].Role != "user" || len(messages[2].ToolCalls) != 1 || messages[2].ToolCalls[0].ID != "call-1" || messages[3].ToolCallID != "call-1" {
				t.Fatalf("in-run history: %+v", messages)
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "现在是 12:00"}}, nil
		},
	}}
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) { return "12:00", nil }}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "几点了", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")

	ctx := context.Background()
	calls, err := svc.ListToolCalls(session.ID)
	if err != nil || len(calls) != 1 {
		t.Fatalf("tool calls: %+v %v", calls, err)
	}
	call := calls[0]
	if call.CallID != "call-1" || call.State != storage.CallSucceeded || call.Result != "12:00" || call.ID == "" {
		t.Fatalf("persisted call: %+v", call)
	}
	messages, err := svc.store.ListMessages(ctx, session.ID)
	if err != nil || len(messages) != 3 {
		t.Fatalf("persisted messages: %+v %v", messages, err)
	}
	if messages[1].Role != storage.RoleAssistant || len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("assistant tool call message: %+v", messages[1])
	}
	// Display projection shows the tool activity as a synthetic entry.
	display, err := svc.ListMessages(session.ID)
	if err != nil || len(display) != 4 || display[2].Role != storage.RoleTool || !strings.Contains(display[2].Content, "12:00") {
		t.Fatalf("display messages: %+v %v", display, err)
	}
}

// After a restart the model history must be rebuilt from the persisted
// records: same assistant tool call requests, same matched results, same
// final answer — no free-form guesswork.
func TestRestartRebuildsSameStructuredHistory(t *testing.T) {
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("call-1")}}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "现在是 12:00"}}, nil
		},
	}}
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) { return "12:00", nil }}
	svc, store := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "几点了", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")

	// Simulate an application restart on the same data directory.
	restarted := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func(messages []model.Message) (model.ChatResponse, error) {
			want := []struct {
				role    string
				content string
				calls   []string // expected assistant tool call IDs
				toolID  string   // expected tool result pairing
			}{
				{"system", "", nil, ""},
				{"user", "几点了", nil, ""},
				{"assistant", "", []string{"call-1"}, ""},
				{"tool", "12:00", nil, "call-1"},
				{"assistant", "现在是 12:00", nil, ""},
				{"user", "再问一次", nil, ""},
			}
			if len(messages) != len(want) {
				t.Fatalf("rebuilt history has %d entries: %+v", len(messages), messages)
			}
			for i, w := range want {
				got := messages[i]
				if w.role == "system" {
					if !strings.Contains(got.Content, "IceTudio") {
						t.Fatalf("history[%d] system prompt missing: %+v", i, got)
					}
					continue
				}
				if got.Role != w.role || got.Content != w.content {
					t.Fatalf("history[%d] = %+v, want %s/%q", i, got, w.role, w.content)
				}
				if len(w.calls) > 0 {
					if len(got.ToolCalls) != len(w.calls) {
						t.Fatalf("history[%d] tool calls: %+v", i, got)
					}
					for j, id := range w.calls {
						if got.ToolCalls[j].ID != id {
							t.Fatalf("history[%d] tool call %d = %q, want %q", i, j, got.ToolCalls[j].ID, id)
						}
					}
				}
				if w.toolID != "" && got.ToolCallID != w.toolID {
					t.Fatalf("history[%d] tool result pairing: %+v", i, got)
				}
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "好的"}}, nil
		},
	}}
	second, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := agent.NewRuns(store)
	defer runs.Close()
	source := config.DefaultModel("lmstudio")
	source.Name = "test-model"
	svc2 := New(Options{
		Store: store, Runs: runs, Tools: tools, Skills: second, Chat: restarted,
		Config: config.Config{Model: source}, ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	})
	if err := svc2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	run2, err := svc2.StartConversationTask(session.ID, "再问一次", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc2, run2.ID, "completed")
}

// A multi-tool batch where one call succeeds and one loses its result must
// keep the succeeded result, pause auto-advance for the unknown one, and
// never replay the whole batch.
func TestPartialToolBatchNotReplayed(t *testing.T) {
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("call-1"), toolCall("call-2")}}}, nil
		},
	}}
	attempts := 0
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) {
		attempts++
		if attempts == 1 {
			return "12:00", nil
		}
		return "", context.Canceled
	}}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "查两次", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "failed")

	calls, err := svc.ListToolCalls(session.ID)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls: %+v %v", calls, err)
	}
	if calls[0].State != storage.CallSucceeded || calls[0].Result != "12:00" {
		t.Fatalf("first call not kept: %+v", calls[0])
	}
	if calls[1].State != storage.CallExecutionUnknown {
		t.Fatalf("second call state: %+v", calls[1])
	}
	if _, err := svc.StartConversationTask(session.ID, "继续", nil); err == nil || !strings.Contains(err.Error(), "结果未知") {
		t.Fatalf("auto-advance not blocked: %v", err)
	}
	if err := svc.store.ResolveToolCall(context.Background(), calls[1].ID, storage.CallFailed, "", "确认该查询未产生效果"); err != nil {
		t.Fatal(err)
	}
	resolved, err := svc.ListToolCalls(session.ID)
	if err != nil || resolved[1].State != storage.CallFailed {
		t.Fatalf("resolution: %+v %v", resolved[1], err)
	}
	// The succeeded result survives; only the disposition is new. The model
	// sees exactly one record per call, never a replayed batch.
	followup := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func(messages []model.Message) (model.ChatResponse, error) {
			var toolResults []model.Message
			for _, item := range messages {
				if item.Role == "tool" {
					toolResults = append(toolResults, item)
				}
			}
			if len(toolResults) != 2 || toolResults[0].Content != "12:00" || !strings.Contains(toolResults[1].Content, "用户处置") {
				t.Fatalf("history after resolution: %+v", messages)
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "了解"}}, nil
		},
	}}
	svc.chat = followup
	run2, err := svc.StartConversationTask(session.ID, "继续", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run2.ID, "completed")
	if len(tools.calls) != 2 {
		t.Fatalf("tools re-executed across runs: %+v", tools.calls)
	}
}

// Cancelling while a call is in flight cannot prove the operation did not
// happen: the call becomes execution_unknown and the session pauses until a
// user disposition.
func TestCancelDuringExecutionBlocksUntilResolved(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) {
		close(started)
		<-release
		return "", context.Canceled
	}}
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("call-1")}}}, nil
		},
	}}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "慢慢查", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool call never started")
	}
	if err := svc.CancelTask(run.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForRun(t, svc, run.ID, "cancelled")
	calls, err := svc.ListToolCalls(session.ID)
	if err != nil || len(calls) != 1 || calls[0].State != storage.CallExecutionUnknown {
		t.Fatalf("cancelled call must stay unknown: %+v %v", calls, err)
	}
	if _, err := svc.StartConversationTask(session.ID, "继续", nil); err == nil {
		t.Fatal("session advanced despite unknown result")
	}
	if _, err := svc.ResolveToolCall(calls[0].ID, storage.CallFailed, "", "确认查询未执行"); err != nil {
		t.Fatal(err)
	}
}

func TestDoubleRunRejected(t *testing.T) {
	release := make(chan struct{})
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) {
		<-release
		return "12:00", nil
	}}
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("call-1")}}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "完成"}}, nil
		},
	}}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartConversationTask(session.ID, "第一问", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartConversationTask(session.ID, "第二问", nil); err == nil || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("double run accepted: %v", err)
	}
	close(release)
	svc.WaitIdle()
	if _, err := svc.StartConversationTask(session.ID, "第三问", nil); err != nil {
		t.Fatalf("next run after completion rejected: %v", err)
	}
	svc.WaitIdle()
}

func TestAcquireHostLockTwiceFails(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireHostLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := AcquireHostLock(dir); err == nil {
		t.Fatal("second host lock acquired")
	}
}
