package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type repairMock struct {
	reviews, executions atomic.Int32
	entered             chan struct{}
}

func (m *repairMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name == "review-model" {
		status := "passed"
		if m.reviews.Add(1) == 1 {
			status = "changes_requested"
		}
		return (&reviewMock{status: status}).Chat(ctx, source, messages, tools)
	}
	n := m.executions.Add(1)
	if n > 1 {
		if !strings.Contains(messages[len(messages)-1].Content, "宿主返工要求") {
			return model.ChatResponse{}, fmt.Errorf("missing host feedback")
		}
		if m.entered != nil {
			close(m.entered)
			<-ctx.Done()
			return model.ChatResponse{}, ctx.Err()
		}
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: fmt.Sprintf("交付版本 %d", n)}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestDirectReviewReworkPreservesDeliveryAndBudget(t *testing.T) {
	s, c := reviewFixture(t, &reviewMock{}, "required", nil)
	m := &repairMock{}
	s.chat = m
	r, err := s.StartConversationTask(c.ID, "完整交付", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	reviews, err := s.ListTaskReviews(r.ID)
	if err != nil || len(reviews) != 2 || reviews[0].Current || !reviews[1].Current || reviews[1].Result.Status != "passed" {
		t.Fatal(reviews, err)
	}
	messages, err := s.ListMessages(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := []string{}
	for _, msg := range messages {
		if msg.RunID == r.ID && msg.Role == storage.RoleAssistant {
			deliveries = append(deliveries, msg.Content)
		}
	}
	if len(deliveries) != 2 || deliveries[0] != "交付版本 1" || deliveries[1] != "交付版本 2" {
		t.Fatal(deliveries)
	}
	g, err := s.GetTaskGraph(r.ID)
	if err != nil || g.Budget.ModelCalls != 4 {
		t.Fatal(g.Budget, err)
	}
}
func TestReworkUsesRemainingBudgetAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			s, c := reviewFixture(t, &reviewMock{}, "required", nil)
			m := &repairMock{}
			s.chat = m
			if cancel {
				m.entered = make(chan struct{})
			} else {
				p, err := s.GetRuntimeConfig(c.ConfigID)
				if err != nil {
					t.Fatal(err)
				}
				p.Data.Execution.Budget.MaxModelCalls = 2
				if _, err = s.UpdateRuntimeConfig(p); err != nil {
					t.Fatal(err)
				}
			}
			r, err := s.StartConversationTask(c.ID, "正文", nil)
			if err != nil {
				t.Fatal(err)
			}
			if cancel {
				select {
				case <-m.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("repair not started")
				}
				if err = s.CancelTask(r.ID); err != nil {
					t.Fatal(err)
				}
				waitForRun(t, s, r.ID, "cancelled")
			} else {
				waitForRun(t, s, r.ID, "failed")
				g, err := s.GetTaskGraph(r.ID)
				if err != nil || g.Budget.ModelCalls != 2 || m.executions.Load() != 1 {
					t.Fatal(g.Budget, err)
				}
			}
		})
	}
}

type repairDynamicMock struct {
	execution *dynamicMock
	reviews   atomic.Int32
	ignore    bool
}

func (m *repairDynamicMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name == "review-model" {
		status := "passed"
		if m.reviews.Add(1) == 1 {
			status = "changes_requested"
		}
		r := storage.ReviewResult{Status: status, Summary: "检查整合任务", Defects: []storage.ReviewDefect{}}
		if status == "changes_requested" {
			r.Defects = []storage.ReviewDefect{{Reference: "task:c", Description: "整合结果缺少结论"}}
		}
		raw, _ := json.Marshal(r)
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 15}}, nil
	}
	if source.Name == "planner-model" {
		var input struct {
			Graph  storage.TaskGraph `json:"graph"`
			Rework *reworkRequest    `json:"rework"`
		}
		if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &input); err != nil {
			return model.ChatResponse{}, err
		}
		if input.Rework != nil {
			patch := plannerPatch{ExpectedRevision: input.Graph.Revision, Disposition: "complete"}
			if !m.ignore && len(input.Graph.Tasks) == 3 {
				patch.Disposition = "continue"
				for _, item := range input.Graph.Tasks {
					patch.Tasks = append(patch.Tasks, item.Spec)
				}
				patch.Tasks = append(patch.Tasks, storage.TaskSpec{ID: "repair-c", Worker: input.Rework.Targets["c"].Worker, Task: "Fix combine conclusion", AccessMode: "read", DependsOn: []string{"c"}, Acceptance: "修复缺少结论"})
			} else if !m.ignore && !storage.TaskSatisfied(input.Graph, "repair-c") {
				patch.Disposition = "continue"
			}
			raw, _ := json.Marshal(patch)
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 20}}, nil
		}
	}
	return m.execution.Chat(ctx, source, messages, tools)
}
func TestDynamicReworkRoutesDefectToOriginalWorker(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(fmt.Sprint(ignore), func(t *testing.T) {
			execution := &dynamicMock{barrier: make(chan struct{})}
			s, c, p := dynamicFixture(t, execution)
			s.chat = &repairDynamicMock{execution: execution, ignore: ignore}
			p.Data.Execution.Review.Mode = "required"
			p.Data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "review-model"}
			if _, err := s.UpdateRuntimeConfig(p); err != nil {
				t.Fatal(err)
			}
			r, err := s.StartConversationTask(c.ID, "整合", nil)
			if err != nil {
				t.Fatal(err)
			}
			final := approveDynamicUntilDone(t, s, c, r)
			if ignore {
				if final.Status != "failed" || !strings.Contains(final.Error, "原 worker") {
					t.Fatal(final)
				}
			} else {
				if final.Status != "completed" {
					t.Fatal(final)
				}
				g, err := s.GetTaskGraph(r.ID)
				if err != nil || len(g.Artifacts) != 4 || len(g.Attempts) != 4 || g.Tasks[3].Spec.Worker != g.Tasks[2].Spec.Worker {
					t.Fatal(g, err)
				}
				reviews, err := s.ListTaskReviews(r.ID)
				if err != nil || len(reviews) != 2 || reviews[0].Current || !reviews[1].Current {
					t.Fatal(reviews, err)
				}
			}
		})
	}
}

type checkRepairMock struct {
	dir    string
	failed bool
	calls  atomic.Int32
}

func (m *checkRepairMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if source.Name == "review-model" {
		return (&reviewMock{status: "passed"}).Chat(ctx, source, messages, tools)
	}
	last := messages[len(messages)-1]
	if last.Role == "user" && strings.HasPrefix(last.Content, "宿主返工要求") {
		script := "printf checked"
		if m.failed {
			script = "exit 1"
		}
		raw, _ := json.Marshal(map[string]any{"executable": "/bin/sh", "args": []string{"-c", script}, "cwd": m.dir, "timeoutSeconds": 5})
		call := model.ToolCall{ID: fmt.Sprintf("repair-check-%d", m.calls.Add(1)), Type: "function"}
		call.Function.Name = "local__exec"
		call.Function.Arguments = string(raw)
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "交付正文，检查以真实工具结果为准"}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestReworkSchedulesNecessaryChecksThroughExistingApproval(t *testing.T) {
	for _, mode := range []string{"pass", "failed", "denied"} {
		t.Run(mode, func(t *testing.T) {
			s, c := reviewFixture(t, &reviewMock{}, "required", []string{"local__exec"})
			m := &checkRepairMock{dir: t.TempDir(), failed: mode == "failed"}
			s.chat = m
			if _, err := s.UpdateContextPolicy(c.ContextID, storage.ContextPolicy{ApprovalMode: "semi", Permissions: storage.PermissionScope{WorkspacePaths: []string{m.dir}, Commands: []string{"/bin/sh"}}}); err != nil {
				t.Fatal(err)
			}
			r, err := s.StartConversationTask(c.ID, "正文并验证", nil)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) {
				calls, err := s.ListToolCalls(c.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, call := range calls {
					if call.State == storage.CallPending {
						if mode == "denied" {
							_, err = s.DenyToolCall(call.ID, "拒绝运行检查")
						} else {
							_, err = s.ApproveToolCall(call.ID)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				final, err := s.GetRun(r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if final.Status != "running" {
					want := "failed"
					wantCalls := 2
					if mode == "denied" {
						wantCalls = 1
					}
					if mode == "pass" {
						want = "completed"
						wantCalls = 1
					}
					if final.Status != want || len(calls) != wantCalls {
						t.Fatal(final, calls)
					}
					if mode == "denied" {
						for _, call := range calls {
							if call.State != storage.CallFailed {
								t.Fatal(call)
							}
						}
					}
					reviews, err := s.ListTaskReviews(r.ID)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "pass" && (len(reviews) != 2 || reviews[1].Result.Status != "passed" || !reviews[1].Current) {
						t.Fatal(reviews)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("check rework timed out")
		})
	}
}

func TestUnknownEffectPreventsAutomaticRework(t *testing.T) {
	s, c := reviewFixture(t, &reviewMock{status: "passed"}, "required", nil)
	ctx := context.Background()
	root, runCtx, p, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.runs.Finish(root.ID, "failed", "未知效果")
	if _, err = s.store.AddMessage(ctx, c.ID, root.ID, storage.RoleUser, "完成操作"); err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.store.AddAssistantMessage(ctx, c.ID, root.ID, "", []storage.MessageToolCall{{ID: "unknown-op", Name: "remote__write", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.store.FinishToolCall(ctx, calls[0].ID, storage.CallExecutionUnknown, "", "连接中断", "public"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AddMessage(ctx, c.ID, root.ID, storage.RoleAssistant, "操作效果未确认"); err != nil {
		t.Fatal(err)
	}
	repaired := false
	err = s.requireDeliveryReview(runCtx, root.ID, c.ID, p, func(reworkRequest) error { repaired = true; return nil })
	if err == nil || repaired {
		t.Fatal("unknown operation was repaired automatically", err)
	}
	reviews, err := s.ListTaskReviews(root.ID)
	if err != nil || len(reviews) != 1 || reviews[0].Result.Status != "inconclusive" {
		t.Fatal(reviews, err)
	}
}
