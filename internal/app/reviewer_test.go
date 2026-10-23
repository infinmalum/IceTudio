package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type reviewMock struct {
	callCount *atomic.Int32
	status    string
	tools     bool
	forged    bool
}

func (m *reviewMock) Chat(_ context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name != "review-model" {
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "交付正文"}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	if m.callCount != nil {
		m.callCount.Add(1)
	}
	if len(tools) != 0 || len(messages) != 2 || !strings.Contains(messages[0].Content, reviewerContract) {
		panic("reviewer context was not isolated")
	}
	result := storage.ReviewResult{Status: m.status, Summary: "模拟审查结论", Defects: []storage.ReviewDefect{}, EvidenceCallIDs: []string{}}
	if m.status == "changes_requested" {
		result.Defects = []storage.ReviewDefect{{Reference: "delivery", Description: "缺少目标要求的内容"}}
	}
	if m.forged {
		result.EvidenceCallIDs = []string{"invented"}
	}
	raw, _ := json.Marshal(result)
	response := model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 15}}
	if m.tools {
		response.Message.ToolCalls = []model.ToolCall{{ID: "invented"}}
	}
	return response, nil
}
func reviewFixture(t *testing.T, m *reviewMock, mode string, checks []string) (*Service, storage.Conversation) {
	t.Helper()
	s, _ := newTestService(t, m, &scriptTools{})
	data := storage.DefaultRuntimeConfig()
	data.PlanEnabled = false
	data.Delegation = storage.DelegationPolicy{}
	data.Execution.Review = storage.ReviewPolicy{Mode: mode, RequiredChecks: checks}
	data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "review-model"}
	p, err := s.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}
func TestRequiredReviewSuccessAndSharedBudget(t *testing.T) {
	s, c := reviewFixture(t, &reviewMock{status: "passed"}, "required", nil)
	r, err := s.StartConversationTask(c.ID, "写正文")
	if err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, s, r.ID, "completed")
	reviews, err := s.ListTaskReviews(r.ID)
	if err != nil || len(reviews) != 1 || !reviews[0].Current || reviews[0].Result.Status != "passed" {
		t.Fatalf("reviews=%+v err=%v", reviews, err)
	}
	children, err := s.ListChildRuns(r.ID)
	if err != nil || len(children) != 1 || children[0].Component != "reviewer" || children[0].Status != "completed" {
		t.Fatalf("children %+v %v", children, err)
	}
	g, err := s.GetTaskGraph(final.ID)
	if err != nil || g.Budget.ModelCalls != 2 {
		t.Fatalf("shared budget %+v %v", g.Budget, err)
	}
}
func TestRequiredReviewRejectsMissingChecksDefectsAndForgedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		m      reviewMock
		checks []string
		want   string
	}{
		{"missing", reviewMock{status: "passed"}, []string{"tests__run"}, "inconclusive"},
		{"defects", reviewMock{status: "changes_requested"}, nil, "changes_requested"},
		{"inconclusive", reviewMock{status: "inconclusive"}, nil, "inconclusive"},
		{"forged", reviewMock{status: "passed", forged: true}, nil, ""},
		{"tool", reviewMock{status: "passed", tools: true}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := reviewFixture(t, &tc.m, "required", tc.checks)
			r, err := s.StartConversationTask(c.ID, "写正文")
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, s, r.ID, "failed")
			reviews, err := s.ListTaskReviews(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(reviews) != 0 {
					t.Fatal("invalid review saved")
				}
			} else {
				wantCount := 1
				if tc.name == "missing" || tc.name == "defects" {
					wantCount = 3
				}
				if len(reviews) != wantCount || reviews[len(reviews)-1].Result.Status != tc.want || !reviews[len(reviews)-1].Current {
					t.Fatalf("%+v", reviews)
				}
			}
		})
	}
}
func TestOnDemandReviewPreservesOldOutcomeAndUsesNewRoot(t *testing.T) {
	s, c := reviewFixture(t, &reviewMock{status: "changes_requested"}, "on_demand", nil)
	r, err := s.StartConversationTask(c.ID, "写正文")
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	before, err := s.ListTaskReviews(r.ID)
	if err != nil || len(before) != 0 {
		t.Fatal(before, err)
	}
	other, err := s.CreateConversation(c.ConfigID, "", "isolated review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartConversationReview(other.ID, r.ID); err == nil {
		t.Fatal("review crossed context boundary")
	}
	one, err := s.StartConversationReview(c.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, one.ID, "completed")
	old, err := s.GetRun(r.ID)
	if err != nil || old.Status != "completed" {
		t.Fatal(old, err)
	}
	reviews, err := s.ListTaskReviews(one.ID)
	if err != nil || len(reviews) != 1 || reviews[0].RootRunID != r.ID || reviews[0].Result.Status != "changes_requested" {
		t.Fatal(reviews, err)
	}
}

type dynamicReviewMock struct {
	execution *dynamicMock
	review    reviewMock
}

func (m *dynamicReviewMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name == "review-model" {
		return m.review.Chat(ctx, source, messages, tools)
	}
	return m.execution.Chat(ctx, source, messages, tools)
}
func TestDynamicRequiredReviewRunsAfterActualDelivery(t *testing.T) {
	m := &dynamicMock{barrier: make(chan struct{})}
	s, c, p := dynamicFixture(t, m)
	s.chat = &dynamicReviewMock{execution: m, review: reviewMock{status: "passed"}}
	p.Data.Execution.Review.Mode = "required"
	p.Data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "review-model"}
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "分析两份材料并整合")
	if err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, s, c, r)
	if final.Status != "completed" {
		t.Fatal(final)
	}
	reviews, err := s.ListTaskReviews(r.ID)
	if err != nil || len(reviews) != 1 || !reviews[0].Current {
		t.Fatal(reviews, err)
	}
}
func TestRequiredReviewBudgetExhaustionCannotComplete(t *testing.T) {
	s, c := reviewFixture(t, &reviewMock{status: "passed"}, "required", nil)
	p, err := s.GetRuntimeConfig(c.ConfigID)
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.Budget.MaxModelCalls = 1
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "正文")
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "failed")
	g, err := s.GetTaskGraph(r.ID)
	if err != nil || g.Budget.ModelCalls != 1 {
		t.Fatal(g, err)
	}
	reviews, err := s.ListTaskReviews(r.ID)
	if err != nil || len(reviews) != 0 {
		t.Fatal(reviews, err)
	}
}

func TestRequiredReviewRemoteModelNeedsFrozenGrant(t *testing.T) {
	count := &atomic.Int32{}
	s, c := reviewFixture(t, &reviewMock{status: "passed", callCount: count}, "required", nil)
	p, err := s.GetRuntimeConfig(c.ConfigID)
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.Reviewer.Model = &config.Model{Provider: "openai", BaseURL: "https://api.openai.com/v1", Name: "review-model"}
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "私人材料")
	if err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, s, r.ID, "failed")
	if !strings.Contains(final.Error, "授权") {
		t.Fatal(final)
	}
	g, err := s.GetTaskGraph(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The attempted send remains conservatively reserved, but no remote
	// payload reached the mock and no passing review was produced.
	if count.Load() != 0 || g.Budget.ModelCalls != 2 {
		t.Fatal(g.Budget)
	}
}

type cancelReviewMock struct{ entered chan struct{} }

func (m *cancelReviewMock) Chat(ctx context.Context, source config.Model, _ []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	if source.Name == "review-model" {
		close(m.entered)
		<-ctx.Done()
		return model.ChatResponse{}, ctx.Err()
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "正文"}}, nil
}
func TestCancelDuringRequiredReviewStopsChildren(t *testing.T) {
	s, c := reviewFixture(t, &reviewMock{status: "passed"}, "required", nil)
	m := &cancelReviewMock{entered: make(chan struct{})}
	s.chat = m
	r, err := s.StartConversationTask(c.ID, "正文")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("review did not start")
	}
	if err := s.CancelTask(r.ID); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "cancelled")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		children, err := s.ListChildRuns(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(children) == 1 && children[0].Status != "running" {
			reviews, err := s.ListTaskReviews(r.ID)
			if err != nil || len(reviews) != 0 {
				t.Fatal(reviews, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("review child survived cancellation")
}
