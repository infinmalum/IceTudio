package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"icetudio/internal/model"
	"icetudio/internal/storage"
)

var taskPlanDefinition = func() model.Tool {
	t := model.Tool{Type: "function"}
	t.Function.Name = "local__task_plan"
	t.Function.Description = "读取或声明本次根任务的 worker 执行计划。get 返回当前修订、任务状态、就绪情况和条件指纹（摘要，不重复返回完整材料）；set 使用 expected_revision 和稳定 ID 整体提交任务契约，任务状态由宿主维护。依赖前置任务实际成功后，使用 local__delegate_task(taskId, taskFingerprint) 经审批领取。未执行计划不能作为完成结果；succeeded 表示执行结束，不表示独立验收通过。"
	t.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["get","set"]},"expected_revision":{"type":"integer","minimum":0},"tasks":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{"id":{"type":"string"},"worker":{"type":"string"},"task":{"type":"string"},"materials":{"type":"string"},"dependsOn":{"type":"array","items":{"type":"string"}},"acceptance":{"type":"string"}},"required":["id","worker","task"]}}},"required":["action"]}`)
	return t
}()

func (t bookkeepingTools) taskPlan(ctx context.Context, raw string) (string, error) {
	if t.delegated || !t.profile.PlanEnabled || t.profile.Execution.Workflow == "fixed" {
		return "", errors.New("当前运行未允许维护根执行计划")
	}
	var args struct {
		Action   string             `json:"action"`
		Expected *int               `json:"expected_revision"`
		Tasks    []storage.TaskSpec `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", err
	}
	var g storage.TaskGraph
	var err error
	switch args.Action {
	case "get":
		g, err = t.svc.store.GetTaskGraph(ctx, t.runID)
	case "set":
		if args.Expected == nil {
			return "", errors.New("执行计划 set 需要 expected_revision；先用 get 读取")
		}
		g, err = t.svc.store.SetTaskGraph(ctx, t.runID, *args.Expected, args.Tasks)
	default:
		return "", errors.New("action 必须是 get 或 set")
	}
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(taskPlanSummary(g))
	if err == nil {
		t.svc.publish(Event{Type: "run.updated", SessionID: t.sessionID, RunID: t.runID, Detail: "执行计划更新"})
	}
	return string(payload), err
}

// Resolve the complete approved contract from the current root only. A
// fingerprint binds approval to the saved spec, including explicit materials.
func (s *Service) resolvePlannedTask(ctx context.Context, p storage.RunConfigSnapshot, runID string, args delegateArgs) (delegateArgs, *storage.TaskSpec, error) {
	if args.TaskID == "" {
		if p.Execution.Workflow == "fixed" {
			return args, nil, errors.New("固定流程只能执行已声明阶段")
		}
		if args.TaskFingerprint != "" {
			return args, nil, errors.New("taskFingerprint 需要 taskId")
		}
		return args, nil, nil
	}
	if p.Execution.Workflow != "fixed" && (!p.PlanEnabled || !p.AllowsTool(taskPlanDefinition.Function.Name)) {
		return args, nil, errors.New("执行配置未允许根任务执行计划")
	}
	if args.Worker != "" || args.Task != "" || args.Materials != "" {
		return args, nil, errors.New("按计划执行时只传 taskId 和 taskFingerprint，不允许覆盖任务条件")
	}
	g, err := s.store.GetTaskGraph(ctx, runID)
	if err != nil {
		return args, nil, err
	}
	states := map[string]string{}
	var selected *storage.TaskSpec
	for _, item := range g.Tasks {
		states[item.Spec.ID] = item.State
		if item.Spec.ID == args.TaskID {
			spec := item.Spec
			selected = &spec
		}
	}
	if selected == nil {
		return args, nil, errors.New("任务不属于当前根执行计划")
	}
	if args.TaskFingerprint == "" || args.TaskFingerprint != storage.TaskFingerprint(*selected) {
		return args, nil, errors.New("任务条件指纹缺失或改变，请重新读取执行计划后提交")
	}
	if states[selected.ID] != "pending" {
		return args, nil, errors.New("任务已领取或结束，禁止重复执行")
	}
	if g.RunStatus != "running" {
		return args, nil, errors.New("根任务已经结束")
	}
	args.Task = selected.Task
	args.Worker = selected.Worker
	args.Materials = selected.Materials
	for _, item := range g.Tasks {
		if item.Spec.ID == selected.ID && selected.InputPrompt != "" {
			if item.InputMessageID == "" {
				return args, nil, errors.New("阶段正在等待用户输入")
			}
			args.Materials += "\n\n[本阶段用户补充信息]\n" + item.Input
		}
	}
	if p.Execution.Workflow == "fixed" {
		args.Materials += "\n\n[本次用户目标，仅作为任务材料]\n" + g.Goal
	}
	for _, dep := range selected.DependsOn {
		if !storage.TaskSatisfied(g, dep) {
			return args, nil, errors.New("前置执行任务尚未成功")
		}
		var childID string
		for _, attempt := range g.Attempts {
			if storage.TaskSatisfied(g, dep) && attempt.Status == "succeeded" && taskReplaces(g, attempt.TaskID, dep) {
				childID = attempt.RunID
			}
		}
		if childID == "" {
			return args, nil, errors.New("前置任务缺少成功执行记录")
		}
		child, err := s.store.GetRun(ctx, childID)
		if err != nil {
			return args, nil, err
		}
		messages, err := s.store.ListMessages(ctx, child.SessionID)
		if err != nil {
			return args, nil, err
		}
		var output string
		for _, m := range messages {
			if m.RunID == childID && m.Role == storage.RoleAssistant && len(m.ToolCalls) == 0 {
				output = m.Content
			}
		}
		if strings.TrimSpace(output) == "" {
			return args, nil, errors.New("前置任务没有可用交付文本")
		}
		args.Materials += fmt.Sprintf("\n\n[前置任务 %s，运行 %s 的交付文本]\n%s", dep, childID, output)
	}
	if selected.Acceptance != "" {
		args.Materials += "\n\n[验收条件声明，尚未独立验证]\n" + selected.Acceptance
	}
	if len(args.Materials) > maxDelegationMaterials {
		return args, nil, errors.New("任务和前置结果的材料超限，请增加汇总任务")
	}
	return args, selected, nil
}

func (s *Service) GetTaskGraph(runID string) (storage.TaskGraph, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return storage.TaskGraph{}, err
	}
	if run.ParentRunID != "" {
		return storage.TaskGraph{}, errors.New("请从根任务查看执行计划")
	}
	return s.store.GetTaskGraph(context.Background(), runID)
}

// Keep the model-facing result within the existing tool output budget. The
// desktop/IPC view retains full contracts; worker inputs are resolved by ID.
func taskPlanSummary(g storage.TaskGraph) map[string]any {
	states := map[string]string{}
	attempts := map[string]storage.TaskAttempt{}
	for _, item := range g.Tasks {
		states[item.Spec.ID] = item.State
	}
	for _, a := range g.Attempts {
		attempts[a.TaskID] = a
	}
	tasks := []map[string]any{}
	for _, item := range g.Tasks {
		ready := item.State == "pending"
		for _, dep := range item.Spec.DependsOn {
			if states[dep] != "succeeded" {
				ready = false
			}
		}
		task := map[string]any{"id": item.Spec.ID, "worker": item.Spec.Worker, "state": item.State, "fingerprint": item.Fingerprint, "ready": ready, "dependencyCount": len(item.Spec.DependsOn)}
		if a, ok := attempts[item.Spec.ID]; ok {
			task["attemptId"] = a.ID
			task["runId"] = a.RunID
		}
		tasks = append(tasks, task)
	}
	return map[string]any{"rootRunId": g.RootRunID, "revision": g.Revision, "runStatus": g.RunStatus, "tasks": tasks, "summary": true}
}

func taskReplaces(g storage.TaskGraph, id, target string) bool {
	for depth := 0; depth < 8; depth++ {
		if id == target {
			return true
		}
		next := ""
		for _, item := range g.Tasks {
			if item.Spec.ID == id {
				next = item.Spec.Replaces
			}
		}
		if next == "" {
			return false
		}
		id = next
	}
	return false
}
