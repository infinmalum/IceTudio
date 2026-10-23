package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type retrospectiveMock struct {
	beforeAnswer                func() error
	failOriginal, forged, tools bool
	calls                       atomic.Int32
	snapshot                    storage.RetrospectiveSnapshot
	entered                     chan struct{}
}

func (m *retrospectiveMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name != "review-model" {
		if m.failOriginal {
			return model.ChatResponse{}, errors.New("模拟模型故障：没有交付")
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "原交付"}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	m.calls.Add(1)
	if len(tools) != 0 || len(messages) != 2 || !strings.Contains(messages[0].Content, retrospectiveContract) || strings.Contains(messages[0].Content, reviewerContract) {
		return model.ChatResponse{}, errors.New("复盘上下文没有隔离")
	}
	if err := json.Unmarshal([]byte(messages[1].Content), &m.snapshot); err != nil {
		return model.ChatResponse{}, err
	}
	if m.beforeAnswer != nil {
		if err := m.beforeAnswer(); err != nil {
			return model.ChatResponse{}, err
		}
	}
	if m.entered != nil {
		close(m.entered)
		<-ctx.Done()
		return model.ChatResponse{}, ctx.Err()
	}
	ref := "run:" + m.snapshot.Graph.RootRunID
	if m.forged {
		ref = "step:invented"
	}
	result := storage.RetrospectiveResult{Summary: "过程复盘，非验收结论", Observations: []storage.RetrospectiveObservation{{Reference: ref, Description: "依据运行终态分析执行过程"}}, Recommendations: []string{"建议补充故障恢复案例；未修改配置"}, Limitations: []string{"模型判断不代替测试"}}
	raw, _ := json.Marshal(result)
	response := model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 15}}
	if m.tools {
		response.Message.ToolCalls = []model.ToolCall{{ID: "forbidden"}}
	}
	return response, nil
}
func TestRetrospectiveSupportsFailedTaskWithoutDeliveryAndPreservesState(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "failed"}[failed], func(t *testing.T) {
			s, c := reviewFixture(t, &reviewMock{}, "on_demand", nil)
			m := &retrospectiveMock{failOriginal: failed}
			s.chat = m
			p, err := s.GetRuntimeConfig(c.ConfigID)
			if err != nil {
				t.Fatal(err)
			}
			p.Data.Execution.Budget.MaxModelCalls = 1
			if _, err = s.UpdateRuntimeConfig(p); err != nil {
				t.Fatal(err)
			}
			original, err := s.StartConversationTask(c.ID, "目标：解释材料")
			if err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if failed {
				want = "failed"
			}
			before := waitForRun(t, s, original.ID, want)
			profileBefore, err := s.GetRuntimeConfig(c.ConfigID)
			if err != nil {
				t.Fatal(err)
			}
			// Unrelated conversation material must never enter the isolated snapshot.
			other, err := s.CreateConversation(c.ConfigID, "", "other")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.AddMessage(context.Background(), other.ID, "", storage.RoleUser, "OTHER_SECRET"); err != nil {
				t.Fatal(err)
			}
			retro, err := s.StartConversationRetrospective(c.ID, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, s, retro.ID, "completed")
			after, err := s.GetRun(original.ID)
			if err != nil || before != after {
				t.Fatal(before, after, err)
			}
			profileAfter, err := s.GetRuntimeConfig(c.ConfigID)
			if err != nil || profileAfter.Revision != profileBefore.Revision {
				t.Fatal(profileAfter, err)
			}
			messages, err := s.ListMessages(c.ID)
			if err != nil || !strings.Contains(messages[len(messages)-1].Content, "建议：建议补充故障恢复案例") || !strings.Contains(messages[len(messages)-1].Content, "限制：模型判断不代替测试") {
				t.Fatal(messages, err)
			}
			reports, err := s.ListTaskRetrospectives(retro.ID)
			if err != nil || len(reports) != 1 || !reports[0].Current || reports[0].RootRunID != original.ID {
				t.Fatal(reports, err)
			}
			reviews, err := s.ListTaskReviews(original.ID)
			if err != nil || len(reviews) != 0 {
				t.Fatal(reviews, err)
			}
			g, err := s.GetTaskGraph(retro.ID)
			if err != nil || g.Budget.ModelCalls != 1 {
				t.Fatal(g.Budget, err)
			}
			if len(m.snapshot.Runs) != 1 || len(m.snapshot.Steps) != 1 || m.snapshot.Steps[0].Payload != "" {
				t.Fatal(m.snapshot)
			}
			raw, _ := json.Marshal(m.snapshot)
			if strings.Contains(string(raw), "OTHER_SECRET") {
				t.Fatal("snapshot leaked other conversation")
			}
			if failed && m.snapshot.Runs[0].Error == "" {
				t.Fatal("failed process missing")
			}
			if _, err = s.StartConversationRetrospective(other.ID, original.ID); err == nil {
				t.Fatal("cross-context retrospective allowed")
			}
		})
	}
}
func TestRetrospectiveRejectsForgedReferencesAndToolCalls(t *testing.T) {
	for _, tools := range []bool{false, true} {
		t.Run(map[bool]string{false: "forged", true: "tools"}[tools], func(t *testing.T) {
			s, c := reviewFixture(t, &reviewMock{}, "on_demand", nil)
			m := &retrospectiveMock{forged: !tools, tools: tools}
			s.chat = m
			original, err := s.StartConversationTask(c.ID, "目标")
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, s, original.ID, "completed")
			retro, err := s.StartConversationRetrospective(c.ID, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, s, retro.ID, "failed")
			reports, err := s.ListTaskRetrospectives(original.ID)
			if err != nil || len(reports) != 0 {
				t.Fatal(reports, err)
			}
		})
	}
}
func TestRetrospectiveCancellationAndFrozenRemoteGrant(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "remote"}[remote], func(t *testing.T) {
			s, c := reviewFixture(t, &reviewMock{}, "on_demand", nil)
			m := &retrospectiveMock{}
			s.chat = m
			original, err := s.StartConversationTask(c.ID, "私人材料")
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, s, original.ID, "completed")
			if remote {
				p, err := s.GetRuntimeConfig(c.ConfigID)
				if err != nil {
					t.Fatal(err)
				}
				p.Data.Execution.Reviewer.Model = &config.Model{Provider: "openai", BaseURL: "https://api.openai.com/v1", Name: "review-model"}
				if _, err = s.UpdateRuntimeConfig(p); err != nil {
					t.Fatal(err)
				}
			} else {
				m.entered = make(chan struct{})
			}
			retro, err := s.StartConversationRetrospective(c.ID, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if remote {
				waitForRun(t, s, retro.ID, "failed")
				if m.calls.Load() != 0 {
					t.Fatal("ungranted remote send")
				}
			} else {
				select {
				case <-m.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("retrospective not started")
				}
				if err = s.CancelTask(retro.ID); err != nil {
					t.Fatal(err)
				}
				waitForRun(t, s, retro.ID, "cancelled")
			}
			reports, err := s.ListTaskRetrospectives(original.ID)
			if err != nil || len(reports) != 0 {
				t.Fatal(reports, err)
			}
		})
	}
}

func TestRetrospectiveInheritsUnknownToolScopeAndRejectsChangedEvidence(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "changed"}[changed], func(t *testing.T) {
			s, c := reviewFixture(t, &reviewMock{}, "on_demand", nil)
			ctx := context.Background()
			target, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.AddMessage(ctx, c.ID, target.ID, storage.RoleUser, "分析未知操作"); err != nil {
				t.Fatal(err)
			}
			_, calls, err := s.store.AddAssistantMessage(ctx, c.ID, target.ID, "", []storage.MessageToolCall{{ID: "unknown", Name: "remote__write", Arguments: "{}"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.store.BeginToolCall(ctx, calls[0].ID); err != nil {
				t.Fatal(err)
			}
			if err = s.store.FinishToolCall(ctx, calls[0].ID, storage.CallExecutionUnknown, "", "响应丢失", storage.ScopeCredential); err != nil {
				t.Fatal(err)
			}
			if err = s.runs.Finish(target.ID, "failed", "响应丢失"); err != nil {
				t.Fatal(err)
			}
			request, err := s.CreateConversation(c.ConfigID, c.ID, "同一上下文的复盘")
			if err != nil {
				t.Fatal(err)
			}
			m := &retrospectiveMock{}
			s.chat = m
			if changed {
				m.beforeAnswer = func() error {
					return s.store.ResolveToolCall(ctx, calls[0].ID, storage.CallFailed, "", "用户确认未生效")
				}
			}
			run, err := s.StartConversationRetrospective(request.ID, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if changed {
				want = "failed"
			}
			final := waitForRun(t, s, run.ID, want)
			if changed && !strings.Contains(final.Error, "已变化") {
				t.Fatal(final)
			}
			_, scopes, err := s.store.SessionDataScopes(ctx, request.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, scope := range scopes {
				if scope == storage.ScopeCredential {
					found = true
				}
			}
			if !found {
				t.Fatal("process scope was lost", scopes)
			}
			reports, err := s.ListTaskRetrospectives(target.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantReports := 1
			if changed {
				wantReports = 0
			}
			if len(reports) != wantReports {
				t.Fatal(reports)
			}
			if len(m.snapshot.Calls) != 1 || m.snapshot.Calls[0].State != storage.CallExecutionUnknown {
				t.Fatal(m.snapshot.Calls)
			}
		})
	}
}
