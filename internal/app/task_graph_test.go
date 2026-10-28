package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func plannedDelegate(id string, spec storage.TaskSpec) model.ChatResponse {
	c := model.ToolCall{ID: id, Type: "function"}
	c.Function.Name = "local__delegate_task"
	raw, _ := json.Marshal(map[string]string{"taskId": spec.ID, "taskFingerprint": storage.TaskFingerprint(spec)})
	c.Function.Arguments = string(raw)
	return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{c}}}
}
func TestPlannedWorkersExecuteDependenciesAndKeepAttempts(t *testing.T) {
	specs := []storage.TaskSpec{{ID: "collect", Worker: "worker", Task: "收集材料", Acceptance: "注明来源"}, {ID: "summarize", Worker: "worker", Task: "总结材料", DependsOn: []string{"collect"}}}
	response := func(name, id, raw string) model.ChatResponse {
		c := model.ToolCall{ID: id, Type: "function"}
		c.Function.Name = name
		c.Function.Arguments = raw
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{c}}}
	}
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			raw, _ := json.Marshal(map[string]any{"action": "set", "expected_revision": 0, "tasks": specs})
			return response("local__task_plan", "plan", string(raw)), nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, `"fingerprint"`) {
				t.Error("missing contract fingerprint")
			}
			return plannedDelegate("too-early", specs[1]), nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, "前置执行任务尚未成功") {
				t.Error("early claim was not refused")
			}
			return plannedDelegate("first", specs[0]), nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, "注明来源") {
				t.Error("worker acceptance missing")
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "材料甲；出处：用户提供"}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return response("local__task_plan", "get", `{"action":"get"}`), nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, `"state":"succeeded"`) {
				t.Error("actual state not delivered")
			}
			return plannedDelegate("duplicate", specs[0]), nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, "禁止重复执行") {
				t.Error("duplicate not refused")
			}
			return plannedDelegate("second", specs[1]), nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, "材料甲；出处：用户提供") {
				t.Error("dependency output not passed")
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "材料总结"}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "两个任务已执行完成"}}, nil
		},
	}}
	svc, _ := newTestService(t, chat, &scriptTools{})
	data := storage.DefaultRuntimeConfig()
	data.PlanEnabled = true
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker"}}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(c.ID, "收集并总结", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := waitForPlannedApproval(t, svc, c.ID, "first")
	if _, err = svc.ApproveToolCall(first.ID); err != nil {
		t.Fatal(err)
	}
	second := waitForPlannedApproval(t, svc, c.ID, "second")
	if second.ID == first.ID {
		t.Fatal("approval call did not advance")
	}
	if _, err = svc.ApproveToolCall(second.ID); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	graph, err := svc.GetTaskGraph(run.ID)
	if err != nil || len(graph.Attempts) != 2 || graph.Tasks[0].State != "succeeded" || graph.Tasks[1].State != "succeeded" || graph.Goal != "收集并总结" {
		t.Fatalf("end to end: %+v %v", graph, err)
	}
	if _, err = svc.GetTaskGraph("missing"); err == nil {
		t.Fatal("missing root accepted")
	}
	if _, err = svc.GetTaskGraph(graph.Attempts[0].RunID); err == nil {
		t.Fatal("child read root graph")
	}
}

func TestPlannedTaskApprovalBindsContract(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	data := storage.DefaultRuntimeConfig()
	data.PlanEnabled = true
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker"}}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, profile, err := store.CreateRunWithOptions(ctx, c.ID, storage.CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec := storage.TaskSpec{ID: "a", Worker: "worker", Task: "初始任务"}
	if _, err = store.SetTaskGraph(ctx, run.ID, 0, []storage.TaskSpec{spec}); err != nil {
		t.Fatal(err)
	}
	args := delegateArgs{TaskID: "a", TaskFingerprint: storage.TaskFingerprint(spec)}
	changed := spec
	changed.Materials = "审批期间变更材料"
	if _, err = store.SetTaskGraph(ctx, run.ID, 1, []storage.TaskSpec{changed}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.resolvePlannedTask(ctx, profile, run.ID, args); err == nil {
		t.Fatal("stale approval contract accepted")
	}
	args.TaskFingerprint = storage.TaskFingerprint(changed)
	args.Task = "override"
	if _, _, err = svc.resolvePlannedTask(ctx, profile, run.ID, args); err == nil {
		t.Fatal("caller override accepted")
	}
}

func waitForPlannedApproval(t *testing.T, svc *Service, sessionID, callID string) storage.ToolCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := svc.ListToolCalls(sessionID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range calls {
			if c.CallID == callID && c.State == storage.CallPending {
				return c
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	runs, _ := svc.ListRuns(sessionID)
	t.Fatalf("planned call %s not pending; runs %+v", callID, runs)
	return storage.ToolCall{}
}

func TestTaskPlanSummaryFitsToolBudget(t *testing.T) {
	graph := storage.TaskGraph{RootRunID: "root", Goal: strings.Repeat("private goal", 2000), RunStatus: "running"}
	for i := 0; i < 20; i++ {
		spec := storage.TaskSpec{ID: strings.Repeat("字", 63) + string(rune('a'+i)), Worker: strings.Repeat("工", 32), Task: strings.Repeat("任务", 2000), Materials: strings.Repeat("private materials", 3000)}
		graph.Tasks = append(graph.Tasks, storage.TaskItem{Spec: spec, Fingerprint: storage.TaskFingerprint(spec), State: "succeeded"})
		graph.Attempts = append(graph.Attempts, storage.TaskAttempt{ID: strings.Repeat("a", 32), TaskID: spec.ID, RunID: strings.Repeat("r", 32), Status: "succeeded"})
	}
	raw, err := json.Marshal(taskPlanSummary(graph))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 16*1024 || strings.Contains(string(raw), "private materials") || strings.Contains(string(raw), "private goal") {
		t.Fatalf("model summary exceeds budget or repeats private materials: %d", len(raw))
	}
	for _, item := range graph.Tasks {
		if !strings.Contains(string(raw), item.Fingerprint) {
			t.Fatal("task contract fingerprint omitted")
		}
	}
}
