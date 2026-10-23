package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func fixedFixture(t *testing.T, m *dynamicMock) (*Service, storage.Conversation, storage.RuntimeConfig) {
	t.Helper()
	s, c, p := dynamicFixture(t, m)
	p.Data.Execution.Workflow = "fixed"
	p.Data.PlanEnabled = false
	p.Data.Execution.Stages = []storage.TaskSpec{
		{ID: "a", Worker: "worker", Task: "Analyze A", AccessMode: "read"},
		{ID: "b", Worker: "worker", Task: "Analyze B", AccessMode: "read"},
		{ID: "c", Worker: "worker", Task: "Combine", AccessMode: "read", DependsOn: []string{"a", "b"}},
	}
	var err error
	p, err = s.UpdateRuntimeConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, p
}

func TestFixedWorkflowParallelJoinFrozenConfigurationAndShare(t *testing.T) {
	m := &dynamicMock{barrier: make(chan struct{})}
	s, c, p := fixedFixture(t, m)
	r, err := s.StartConversationTask(c.ID, "分析用户材料")
	if err != nil {
		t.Fatal(err)
	}
	// Editing after start cannot alter the graph or workers awaiting approval.
	p.Data.Execution.Stages[0].Task = "Changed after start"
	if _, err = s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, s, c, r)
	if final.Status != "completed" {
		t.Fatalf("fixed run: %+v", final)
	}
	g, err := s.GetTaskGraph(r.ID)
	if err != nil || len(g.Attempts) != 3 || len(g.Artifacts) != 3 || g.Coordination.Rounds != 0 || g.Budget.ModelCalls != 4 || g.Tasks[0].Spec.Task != "Analyze A" {
		t.Fatalf("fixed graph: %+v %v", g, err)
	}
	resources, err := s.store.RunResources(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resources.Planner.ID != "" {
		t.Fatal("fixed workflow captured a planner")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.planners != 0 || m.maxActive != 2 {
		t.Fatalf("planner=%d parallel=%d", m.planners, m.maxActive)
	}
}

type fixedMock struct {
	mu                                    sync.Mutex
	active, maxActive, workers, summaries int
	failure, review                       string
	missingGoal                           bool
}

func (m *fixedMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name == "planner-model" {
		panic("fixed workflow called planner")
	}
	if source.Name == "review-model" {
		return (&reviewMock{status: m.review}).Chat(ctx, source, messages, tools)
	}
	content := messages[len(messages)-1].Content
	if strings.Contains(content, "宿主已完成任务调度") {
		m.mu.Lock()
		m.summaries++
		m.mu.Unlock()
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "汇总交付"}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	m.mu.Lock()
	m.workers++
	m.active++
	if m.active > m.maxActive {
		m.maxActive = m.active
	}
	if !strings.Contains(content, "用户特定目标") {
		m.missingGoal = true
	}
	m.mu.Unlock()
	select {
	case <-time.After(25 * time.Millisecond):
	case <-ctx.Done():
		return model.ChatResponse{}, ctx.Err()
	}
	m.mu.Lock()
	m.active--
	m.mu.Unlock()
	outcome := workerOutcome{Status: "succeeded", Content: "阶段交付"}
	if m.failure != "" {
		outcome.Status = m.failure
		outcome.Content = "缺少输入"
	}
	raw, _ := json.Marshal(outcome)
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 10}}, nil
}

func TestFixedWorkflowSerialEffectsAndReviewGate(t *testing.T) {
	for _, status := range []string{"passed", "changes_requested"} {
		t.Run(status, func(t *testing.T) {
			s, c, p := fixedFixture(t, &dynamicMock{})
			m := &fixedMock{review: status}
			s.chat = m
			p.Data.Execution.Stages = []storage.TaskSpec{{ID: "one", Worker: "worker", Task: "第一步", AccessMode: "write"}, {ID: "two", Worker: "worker", Task: "第二步", AccessMode: "unknown"}}
			p.Data.Execution.Review.Mode = "required"
			p.Data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "review-model"}
			if _, err := s.UpdateRuntimeConfig(p); err != nil {
				t.Fatal(err)
			}
			r, err := s.StartConversationTask(c.ID, "用户特定目标")
			if err != nil {
				t.Fatal(err)
			}
			final := approveDynamicUntilDone(t, s, c, r)
			want := "completed"
			if status != "passed" {
				want = "failed"
			}
			if final.Status != want {
				t.Fatalf("review gate: %+v", final)
			}
			reviews, err := s.ListTaskReviews(r.ID)
			if err != nil || len(reviews) != 1 || reviews[0].Result.Status != status {
				t.Fatalf("reviews: %+v %v", reviews, err)
			}
			g, err := s.GetTaskGraph(r.ID)
			if err != nil || len(g.Attempts) != 2 || g.Budget.ModelCalls != 4 || g.Coordination.Rounds != 0 {
				t.Fatalf("unexpected rework: %+v %v", g, err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.maxActive != 1 || m.workers != 2 || m.summaries != 1 || m.missingGoal {
				t.Fatalf("serial workers: %+v", m)
			}
		})
	}
}

func TestFixedWorkflowFailureStopsSuccessorsAndDoesNotRetry(t *testing.T) {
	for _, status := range []string{"blocked", "failed", "invalid"} {
		t.Run(status, func(t *testing.T) {
			s, c, p := fixedFixture(t, &dynamicMock{})
			m := &fixedMock{failure: status}
			s.chat = m
			p.Data.Execution.Stages = p.Data.Execution.Stages[:2]
			p.Data.Execution.Stages[1].DependsOn = []string{"a"}
			if _, err := s.UpdateRuntimeConfig(p); err != nil {
				t.Fatal(err)
			}
			r, err := s.StartConversationTask(c.ID, "用户特定目标")
			if err != nil {
				t.Fatal(err)
			}
			final := approveDynamicUntilDone(t, s, c, r)
			if final.Status != "blocked" {
				t.Fatalf("failed stage completed root: %+v", final)
			}
			g, err := s.GetTaskGraph(r.ID)
			if err != nil || len(g.Attempts) != 1 || g.Tasks[1].State != "cancelled" || g.Coordination.Rounds != 0 {
				t.Fatalf("successor executed: %+v %v", g, err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.workers != 1 || m.summaries != 0 {
				t.Fatal("failed workflow retried or delivered")
			}
		})
	}
}

func TestFixedWorkflowApprovalDenialStopsDispatch(t *testing.T) {
	s, c, p := fixedFixture(t, &dynamicMock{})
	p.Data.Execution.Stages = p.Data.Execution.Stages[:1]
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "用户特定目标")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	denied := false
	for time.Now().Before(deadline) {
		calls, err := s.ListToolCalls(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if call.State == storage.CallPending {
				if _, err = s.DenyToolCall(call.ID, "拒绝派发"); err != nil {
					t.Fatal(err)
				}
				denied = true
			}
		}
		if denied {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !denied {
		t.Fatal("no dispatch approval")
	}
	final := waitForRun(t, s, r.ID, "blocked")
	g, err := s.GetTaskGraph(final.ID)
	if err != nil || len(g.Attempts) != 0 || g.Tasks[0].State != "cancelled" {
		t.Fatalf("denied dispatch executed: %+v %v", g, err)
	}
	calls, err := s.ListToolCalls(c.ID)
	if err != nil || len(calls) != 1 {
		t.Fatalf("denied dispatch repeated: %+v %v", calls, err)
	}
}

func TestFixedWorkflowIndependentInspectionsDoNotExecuteStages(t *testing.T) {
	s, c, p := fixedFixture(t, &dynamicMock{})
	m := &fixedMock{review: "passed"}
	s.chat = m
	p.Data.Execution.Stages = p.Data.Execution.Stages[:1]
	p.Data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "review-model"}
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	original, err := s.StartConversationTask(c.ID, "用户特定目标")
	if err != nil {
		t.Fatal(err)
	}
	if r := approveDynamicUntilDone(t, s, c, original); r.Status != "completed" {
		t.Fatal(r)
	}
	r, err := s.StartConversationReview(c.ID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	g, err := s.GetTaskGraph(r.ID)
	if err != nil || len(g.Tasks) != 0 || len(g.Attempts) != 0 || g.Budget.ModelCalls != 1 {
		t.Fatalf("inspection initialized business stages: %+v %v", g, err)
	}
	m.mu.Lock()
	workers := m.workers
	m.mu.Unlock()
	if workers != 1 {
		t.Fatal("inspection executed workers")
	}
	s.chat = &retrospectiveMock{}
	r, err = s.StartConversationRetrospective(c.ID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	g, err = s.GetTaskGraph(r.ID)
	if err != nil || len(g.Tasks) != 0 || len(g.Attempts) != 0 || g.Budget.ModelCalls != 1 {
		t.Fatalf("retrospective initialized business stages: %+v %v", g, err)
	}
}
