package app

import (
	"context"
	"encoding/json"
	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"strings"
	"sync"
	"testing"
	"time"
)

type dynamicMock struct {
	mu                          sync.Mutex
	active, maxActive, planners int
	barrier                     chan struct{}
	once                        sync.Once
	waiting                     bool
}

func (m *dynamicMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name == "planner-model" {
		var input struct {
			Graph storage.TaskGraph `json:"graph"`
		}
		if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &input); err != nil {
			return model.ChatResponse{}, err
		}
		m.mu.Lock()
		m.planners++
		m.mu.Unlock()
		patch := plannerPatch{ExpectedRevision: input.Graph.Revision, Disposition: "continue"}
		if m.waiting {
			patch.Disposition = "waiting"
			patch.Reason = "请提供待分析材料"
		} else if len(input.Graph.Tasks) == 0 {
			patch.Tasks = []storage.TaskSpec{{ID: "a", Worker: "worker", Task: "Analyze A", AccessMode: "read", Acceptance: "给出分析"}, {ID: "b", Worker: "worker", Task: "Analyze B", AccessMode: "read", Acceptance: "给出分析"}, {ID: "c", Worker: "worker", Task: "Combine", AccessMode: "read", DependsOn: []string{"a", "b"}, Acceptance: "整合两份材料"}}
		} else {
			done := true
			for _, item := range input.Graph.Tasks {
				if !storage.TaskSatisfied(input.Graph, item.Spec.ID) {
					done = false
				}
			}
			if done {
				patch.Disposition = "complete"
			}
		}
		raw, _ := json.Marshal(patch)
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 30}}, nil
	}
	if strings.Contains(messages[len(messages)-1].Content, "宿主已完成任务调度") {
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "已分析并整合 A、B；尚未独立审查"}, Usage: model.Usage{TotalTokens: 20}}, nil
	}
	content := messages[len(messages)-1].Content
	if strings.HasPrefix(content, "Analyze ") {
		m.mu.Lock()
		m.active++
		if m.active > m.maxActive {
			m.maxActive = m.active
		}
		if m.active == 2 {
			m.once.Do(func() { close(m.barrier) })
		}
		m.mu.Unlock()
		select {
		case <-m.barrier:
		case <-ctx.Done():
			return model.ChatResponse{}, ctx.Err()
		}
		time.Sleep(25 * time.Millisecond)
		m.mu.Lock()
		m.active--
		m.mu.Unlock()
	} else if strings.HasPrefix(content, "Combine") && !strings.Contains(content, "Analyze A result") {
		return model.ChatResponse{}, context.Canceled
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: dynamicOutcome(strings.Split(content, "\n")[0] + " result")}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func dynamicFixture(t *testing.T, chat *dynamicMock) (*Service, storage.Conversation, storage.RuntimeConfig) {
	t.Helper()
	svc, _ := newTestService(t, chat, &scriptTools{})
	data := storage.BlankRuntimeConfig()
	data.SystemPrompt = "围绕问题安排 analyst 子任务并整合结论。"
	data.AllMCPServers = true
	data.PlanEnabled = true
	data.Execution.Review.Mode = "on_demand"
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
	data.Execution.Workflow = "dynamic"
	data.Execution.Planner.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "planner-model"}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{Name: "worker", SystemPrompt: "完成交给你的分析子任务，直接返回结论。", ContextBudget: 8192}}}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return svc, c, p
}
func approveDynamicUntilDone(t *testing.T, svc *Service, c storage.Conversation, run storage.Run) storage.Run {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := svc.ListToolCalls(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if call.State == storage.CallPending && call.ToolName == "local__delegate_task" {
				_, _ = svc.ApproveToolCall(call.ID)
			}
		}
		r, err := svc.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "running" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dynamic run timed out")
	return storage.Run{}
}
func TestDynamicPlannerParallelDependencyAndDelivery(t *testing.T) {
	m := &dynamicMock{barrier: make(chan struct{})}
	svc, c, _ := dynamicFixture(t, m)
	run, err := svc.StartConversationTask(c.ID, "分析两份材料并整合")
	if err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, svc, c, run)
	if final.Status != "completed" {
		t.Fatalf("dynamic failed: %+v", final)
	}
	g, err := svc.GetTaskGraph(run.ID)
	if err != nil || len(g.Attempts) != 3 || len(g.Artifacts) != 3 || g.Coordination.State != "complete" || g.Budget.UnknownUsage != 0 {
		t.Fatalf("dynamic graph: %+v %v", g, err)
	}
	m.mu.Lock()
	maximum := m.maxActive
	planners := m.planners
	m.mu.Unlock()
	if maximum != 2 || planners < 2 {
		t.Fatalf("coordination did not run: max=%d planner=%d", maximum, planners)
	}
	children, err := svc.ListChildRuns(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, child := range children {
		if child.Component == "planner" {
			found = true
		}
	}
	if !found {
		t.Fatal("planner Run role not persisted")
	}
	history, err := svc.ListMessages(c.ID)
	if err != nil || !strings.Contains(history[len(history)-1].Content, "尚未独立审查") {
		t.Fatal("main delivery missing", err)
	}
}
func TestDynamicWaitingDoesNotPretendSuccess(t *testing.T) {
	m := &dynamicMock{waiting: true}
	svc, c, _ := dynamicFixture(t, m)
	run, err := svc.StartConversationTask(c.ID, "分析材料")
	if err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, svc, c, run)
	g, err := svc.GetTaskGraph(run.ID)
	if err != nil || final.Status == "completed" || g.Coordination.State != "waiting" || g.Coordination.Reason != "请提供待分析材料" {
		t.Fatalf("waiting state: %+v %+v %v", final, g, err)
	}
}

func dynamicOutcome(text string) string {
	raw, _ := json.Marshal(workerOutcome{Status: "succeeded", Content: text, Evidence: []string{}})
	return string(raw)
}

type retryMock struct {
	mu      sync.Mutex
	workers int
}

func (m *retryMock) Chat(_ context.Context, source config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	if source.Name == "planner-model" {
		var input struct {
			Graph storage.TaskGraph `json:"graph"`
		}
		json.Unmarshal([]byte(messages[len(messages)-1].Content), &input)
		g := input.Graph
		p := plannerPatch{ExpectedRevision: g.Revision, Disposition: "continue"}
		if len(g.Tasks) == 0 {
			p.Tasks = []storage.TaskSpec{{ID: "first", Worker: "worker", Task: "Retry safe analysis", AccessMode: "read", Acceptance: "给出分析"}}
		} else if len(g.Tasks) == 1 && g.Tasks[0].State == "failed" {
			p.Tasks = []storage.TaskSpec{g.Tasks[0].Spec, {ID: "retry", Worker: "worker", Task: "Retry safe analysis", AccessMode: "read", Acceptance: "给出分析", Replaces: "first"}}
		} else if len(g.Tasks) == 2 && storage.TaskSatisfied(g, "first") {
			p.Disposition = "complete"
		}
		raw, _ := json.Marshal(p)
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	if strings.Contains(messages[len(messages)-1].Content, "宿主已完成任务调度") {
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "重试已完成"}}, nil
	}
	m.mu.Lock()
	m.workers++
	n := m.workers
	m.mu.Unlock()
	o := workerOutcome{Status: "succeeded", Content: "有效分析", Evidence: []string{}}
	if n == 1 {
		o.Status = "failed"
		o.Content = "临时分析失败，无工具副作用"
	}
	raw, _ := json.Marshal(o)
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestDynamicBoundedRetryKeepsFailures(t *testing.T) {
	mock := &retryMock{}
	svc, _ := newTestService(t, mock, &scriptTools{})
	data := storage.BlankRuntimeConfig()
	data.SystemPrompt = "围绕问题安排 developer 子任务并整合结论。"
	data.AllMCPServers = true
	data.PlanEnabled = true
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
	data.Execution.Workflow = "dynamic"
	data.Execution.Planner.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "planner-model"}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{Name: "worker", SystemPrompt: "完成交给你的分析子任务，直接返回结论。", ContextBudget: 8192}}}
	data.Execution.Review.Mode = "off" // This test covers R3 retry, independent of R4.
	data.Execution.Budget = storage.RootBudget{AutoRetry: true, MaxRetries: 1}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.StartConversationTask(c.ID, "做安全分析")
	if err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, svc, c, r)
	g, err := svc.GetTaskGraph(r.ID)
	if err != nil || final.Status != "completed" || len(g.Attempts) != 2 || g.Tasks[0].State != "failed" || !storage.TaskSatisfied(g, "first") {
		t.Fatalf("bounded retry: %+v %+v %v", final, g, err)
	}
}
func TestDynamicRootModelBudgetCannotReset(t *testing.T) {
	m := &dynamicMock{barrier: make(chan struct{})}
	svc, c, p := dynamicFixture(t, m)
	p.Data.Execution.Budget.MaxModelCalls = 1
	if _, err := svc.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	r, err := svc.StartConversationTask(c.ID, "分析")
	if err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, svc, c, r)
	g, err := svc.GetTaskGraph(r.ID)
	if err != nil || final.Status == "completed" || g.Budget.ModelCalls != 1 {
		t.Fatalf("model budget: %+v %+v %v", final, g.Budget, err)
	}
}

func TestReadonlyGatewayBlocksUnknownSideEffects(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	data := storage.BlankRuntimeConfig()
	data.SystemPrompt = "只读分析助手"
	data.AllMCPServers = true
	data.PlanEnabled = true
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{Name: "worker", SystemPrompt: "完成交给你的子任务。", ContextBudget: 8192}}}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := store.CreateRunWithOptions(ctx, c.ID, storage.CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec := storage.TaskSpec{ID: "read", Worker: "worker", Task: "只读分析", AccessMode: "read"}
	if _, err = store.SetTaskGraph(ctx, root.ID, 0, []storage.TaskSpec{spec}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateDelegationSession(ctx, c.ContextID, storage.DefaultConfigID, "child")
	if err != nil {
		t.Fatal(err)
	}
	child, profile, err := store.CreateRunWithOptions(ctx, session.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker", TaskSpec: &spec})
	if err != nil {
		t.Fatal(err)
	}
	gate := toolGate{svc: svc, contextID: c.ContextID, snapshot: &profile, runID: child.ID}
	decision := gate.Gate(ctx, agent.ToolRequest{Tool: "external__write", Arguments: "{}"})
	if !strings.Contains(decision.Deny, "只读任务") {
		t.Fatal("readonly gateway widened privileges", decision)
	}
}
