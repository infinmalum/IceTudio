package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

// These scripted scenes verify assembly, evidence and publication, not model quality.
type sceneModel struct {
	scene, dir string
	blocked    bool
}

func (m *sceneModel) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	respond := func(content string) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: content}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	if source.Name == "scene-review" {
		return (&reviewMock{status: "passed"}).Chat(ctx, config.Model{Name: "review-model"}, messages, tools)
	}
	if source.Name == "scene-plan" {
		var in struct {
			Graph storage.TaskGraph `json:"graph"`
		}
		if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &in); err != nil {
			return model.ChatResponse{}, err
		}
		patch := plannerPatch{ExpectedRevision: in.Graph.Revision, Disposition: "continue"}
		if m.blocked {
			patch.Disposition = "waiting"
			patch.Reason = "请提供待分析材料"
		} else if len(in.Graph.Tasks) == 0 {
			worker := "analyst"
			mode := "read"
			if m.scene == "development" {
				worker = "developer"
				mode = "write"
			}
			patch.Tasks = []storage.TaskSpec{{ID: "analyze", Worker: worker, Task: "处理用户材料", Acceptance: "保留来源与未知项；开发任务实际执行检查", AccessMode: mode}}
		} else {
			patch.Disposition = "complete"
		}
		raw, _ := json.Marshal(patch)
		return respond(string(raw))
	}
	if source.Name == "scene-worker" {
		if m.scene == "development" {
			var executed []model.Message
			for _, msg := range messages {
				if msg.Role == "tool" {
					executed = append(executed, msg)
				}
			}
			if len(executed) < 2 {
				call := model.ToolCall{ID: fmt.Sprintf("scene-tool-%d", len(executed)), Type: "function"}
				var args any
				if len(executed) == 0 {
					call.Function.Name = "local__edit_file"
					args = map[string]any{"path": filepath.Join(m.dir, "check.sh"), "oldText": "exit 1", "newText": "exit 0"}
				} else {
					call.Function.Name = "local__exec"
					args = map[string]any{"executable": "/bin/sh", "args": []string{"check.sh"}, "cwd": m.dir, "timeoutSeconds": 5}
				}
				raw, _ := json.Marshal(args)
				call.Function.Arguments = string(raw)
				return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, Usage: model.Usage{TotalTokens: 10}}, nil
			}
			var check struct {
				Passed bool   `json:"passed"`
				Error  string `json:"error"`
			}
			if err := json.Unmarshal([]byte(executed[1].Content), &check); err != nil || !check.Passed || check.Error != "" {
				return model.ChatResponse{}, fmt.Errorf("check failed: %s", executed[1].Content)
			}

			return respond(`{"status":"succeeded","content":"check.sh 已修复，真实检查退出码0","evidenceCallIds":["scene-tool-0","scene-tool-1"]}`)
		}
		return respond(dynamicOutcome("[A]20元/30天；[B]15元/保留期限未知"))
	}
	switch m.scene {
	case "daily":
		return respond("小林：交稿，周五；小周：审核，期限待确认；发布：负责人和期限待确认。")
	case "writing":
		return respond("周六10点，我们将进行维护。\n\n如有问题，请联系support@example.com。")
	case "research":
		return respond("[B]价格较低；[A]保留30天，[B]保留期限未知。来源：[A]/[B]。")
	default:
		return respond("修改check.sh；实际检查退出码0，无遗留问题。")
	}
}

// sceneData builds the inline execution data each scripted scene needs:
// direct answers for daily/writing, a dynamic planned workflow with a named
// worker and required review for research/development.
func sceneData(scene string) storage.RuntimeConfigData {
	data := storage.BlankRuntimeConfig()
	switch scene {
	case "research", "development":
		data.PlanEnabled = true
		data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
		data.Execution.Workflow = "dynamic"
		data.Execution.Review.Mode = "required"
		data.EnabledMCPServers = []string{"local"}
		if scene == "research" {
			data.Execution.WorkerSources = []storage.WorkerSource{{Name: "analyst"}}
		} else {
			data.Execution.WorkerSources = []storage.WorkerSource{{Name: "developer"}}
		}
	default:
		data.Execution.Review.Mode = "on_demand"
		data.Execution.ImproverEnabled = true
		if scene == "daily" {
			data.EnabledMCPServers = []string{"local"}
		}
	}
	return data
}

func TestDefaultSceneExecutionContracts(t *testing.T) {
	inputs := map[string]string{
		"daily":       "把这些事项整理为三项待办，每项包含负责人和期限；缺失的信息标为待确认：小林周五交稿，小周负责审核，发布时间未定。",
		"writing":     "面向新用户，把下面通知改为亲切、清楚的两段正文，不超过120字，保留时间与联系方式，不添加承诺：周六10点进行维护，问题请联系support@example.com。",
		"research":    "只比较下面两份材料的价格和保留期限，引用[A]/[B]，不要联网；缺失项标未知：[A]每月20元，保留30天。[B]每月15元，未说明保留期限。",
		"development": "修复已授权工作区check.sh的错误退出码，只修改该文件；用/bin/sh check.sh检查并报告真实结果。",
	}
	for _, scene := range []string{"daily", "writing", "research", "development"} {
		t.Run(scene, func(t *testing.T) {
			m := &sceneModel{scene: scene, dir: t.TempDir()}
			s, _ := newTestService(t, m, &scriptTools{})
			data := sceneData(scene)
			data.Execution.Planner.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "scene-plan"}
			data.Execution.Worker.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "scene-worker"}
			data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "scene-review"}
			if scene == "development" {
				data.Execution.Review.RequiredChecks = []string{"local__exec"}
				if err := os.WriteFile(filepath.Join(m.dir, "check.sh"), []byte("exit 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p, err := s.configureDefault(data)
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.CreateConversation(p.ID, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.UpdateContextPolicy(c.ContextID, storage.ContextPolicy{ApprovalMode: "semi", Permissions: storage.PermissionScope{WorkspacePaths: []string{m.dir}, Commands: []string{"/bin/sh"}}}); err != nil {
				t.Fatal(err)
			}
			run, err := s.StartConversationTask(c.ID, inputs[scene], nil)
			if err != nil {
				t.Fatal(err)
			}
			final := approveSceneUntilDone(t, s, c, run)
			if final.Status != "completed" {
				g, _ := s.GetTaskGraph(run.ID)
				children, _ := s.ListChildRuns(run.ID)
				for _, child := range children {
					calls, _ := s.ListToolCalls(child.SessionID)
					t.Logf("child=%+v calls=%+v", child, calls)
				}
				t.Fatalf("final=%+v graph=%+v", final, g)
			}
			reviews, err := s.ListTaskReviews(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if p.Data.Execution.Workflow == "dynamic" {
				if len(reviews) != 1 || reviews[0].Result.Status != "passed" {
					t.Fatal(reviews)
				}
			} else {
				if len(reviews) != 0 {
					t.Fatal("unexpected automatic review", reviews)
				}
			}
			if scene == "development" {
				data, _ := os.ReadFile(filepath.Join(m.dir, "check.sh"))
				if string(data) != "exit 0\n" {
					t.Fatal(string(data))
				}
				g, err := s.GetTaskGraph(run.ID)
				if err != nil || len(g.Artifacts) != 1 {
					t.Fatal(g, err)
				}
			}
		})
	}
}

func TestResearchSceneMissingMaterialsWaits(t *testing.T) {
	s, _ := newTestService(t, &sceneModel{scene: "research", blocked: true}, &scriptTools{})
	data := sceneData("research")
	data.Execution.Planner.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "scene-plan"}
	p, err := s.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "比较未提供的材料", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.WaitIdle()
	g, err := s.GetTaskGraph(r.ID)
	if err != nil || g.Coordination.State != "waiting" || len(g.Tasks) != 0 || g.RunStatus == "completed" {
		t.Fatal(g, err)
	}
}

func approveSceneUntilDone(t *testing.T, s *Service, c storage.Conversation, r storage.Run) storage.Run {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := s.PendingApprovals(c.ContextID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if call.State == storage.CallPending {
				if _, err = s.ApproveToolCall(call.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
		final, err := s.GetRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status != "running" {
			return final
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scene timed out")
	return storage.Run{}
}
