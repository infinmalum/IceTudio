package app

import (
	"context"
	"encoding/json"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunGrantCeilingAndRevocation(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	remote := config.Model{Provider: "lmstudio", BaseURL: "https://api.example.com/v1", Name: "test"}
	origin, _, err := endpointOrigin(remote.EffectiveBaseURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.GrantExfil(c.ContextID, origin, []string{storage.ScopePersonal}); err != nil {
		t.Fatal(err)
	}
	run, profile, err := store.CreateRunWithOptions(ctx, c.ID, storage.CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMessage(ctx, c.ID, run.ID, storage.RoleUser, "private task"); err != nil {
		t.Fatal(err)
	}
	if err = svc.exfilCheckRun(ctx, run.ID, c.ID, remote, profile); err != nil {
		t.Fatal(err)
	}
	if err = svc.RevokeExfil(c.ContextID, origin); err != nil {
		t.Fatal(err)
	}
	if err = svc.exfilCheckRun(ctx, run.ID, c.ID, remote, profile); err == nil {
		t.Fatal("revoked grant still allowed send")
	}
	if _, err = svc.GrantExfil(c.ContextID, origin, []string{storage.ScopePersonal, storage.ScopeCredential}); err != nil {
		t.Fatal(err)
	}
	if err = store.AccumulateContextScopes(ctx, c.ID, []string{storage.ScopeCredential}); err != nil {
		t.Fatal(err)
	}
	if err = svc.exfilCheckRun(ctx, run.ID, c.ID, remote, profile); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatal("live expansion widened starting ceiling", err)
	}
	g, _ := svc.GetTaskGraph(run.ID)
	if g.Coordination.EventSeq <= 1 {
		t.Fatal("authority mutation did not persist event")
	}
}

type cancelMock struct {
	mu      sync.Mutex
	started int
	ready   chan struct{}
	once    sync.Once
}

func (m *cancelMock) Chat(ctx context.Context, source config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	if source.Name == "planner-model" {
		var input struct {
			Graph storage.TaskGraph `json:"graph"`
		}
		json.Unmarshal([]byte(messages[len(messages)-1].Content), &input)
		patch := plannerPatch{ExpectedRevision: input.Graph.Revision, Disposition: "continue", Tasks: []storage.TaskSpec{{ID: "a", Worker: "worker", Task: "wait a", AccessMode: "read"}, {ID: "b", Worker: "worker", Task: "wait b", AccessMode: "read"}}}
		raw, _ := json.Marshal(patch)
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}}, nil
	}
	m.mu.Lock()
	m.started++
	if m.started == 2 {
		m.once.Do(func() { close(m.ready) })
	}
	m.mu.Unlock()
	<-ctx.Done()
	return model.ChatResponse{}, ctx.Err()
}
func TestDynamicCancelPropagatesAndKeepsUnknownDispatch(t *testing.T) {
	mock := &cancelMock{ready: make(chan struct{})}
	svc, _ := newTestService(t, mock, &scriptTools{})
	data := storage.BlankRuntimeConfig()
	data.SystemPrompt = "研究分析：按来源安排 analyst 并整合结论。"
	data.AllMCPServers = true
	data.PlanEnabled = true
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
	data.Execution.Workflow = "dynamic"
	data.Execution.Planner.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "planner-model"}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{Name: "worker", SystemPrompt: "完成交给你的子任务。", ContextBudget: 8192}}}
	if _, err := svc.configureDefault(data); err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.StartConversationTask(c.ID, "等待测试", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	ready := false
	for !ready {
		select {
		case <-mock.ready:
			ready = true
		case <-deadline:
			t.Fatal("workers did not start")
		default:
			calls, _ := svc.ListToolCalls(c.ID)
			for _, call := range calls {
				if call.State == storage.CallPending {
					_, _ = svc.ApproveToolCall(call.ID)
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err = svc.CancelTask(r.ID); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		children, _ := svc.ListChildRuns(r.ID)
		running := false
		for _, child := range children {
			if child.Status == "running" {
				running = true
			}
		}
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	children, _ := svc.ListChildRuns(r.ID)
	for _, child := range children {
		if child.Status == "running" {
			t.Fatal("cancel did not reach child")
		}
	}
	calls, _ := svc.ListToolCalls(c.ID)
	unknown := 0
	for _, call := range calls {
		if call.State == storage.CallExecutionUnknown {
			unknown++
		}
	}
	if unknown == 0 {
		t.Fatal("cancellation erased ambiguous dispatch outcome")
	}
}
