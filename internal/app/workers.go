package app

import (
	"context"
	"encoding/json"
	"errors"

	"sort"
	"strings"

	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func (s *Service) resourcesFor(ctx context.Context, p storage.RunConfigSnapshot, runID string) (storage.RunResources, error) {
	if p.Resources != nil {
		return *p.Resources, nil
	}
	if runID == "" {
		return storage.RunResources{}, nil
	}
	return s.store.RunResources(ctx, runID)
}

func (s *Service) delegationDefinition(ctx context.Context, p storage.RunConfigSnapshot, runID string) model.Tool {
	d := delegateDefinition
	resources, err := s.resourcesFor(ctx, p, runID)
	if err != nil || len(resources.Workers) == 0 {
		return d
	}
	names := []string{}
	for name := range resources.Workers {
		names = append(names, name)
	}
	sort.Strings(names)
	d.Function.Description = "把明确的子任务交给已配置的 worker；只传入必要材料，等待其独立执行结果。worker 使用本任务起始配置，不读取其他对话或长期记忆。"
	d.Function.Parameters, _ = json.Marshal(map[string]any{"type": "object", "properties": map[string]any{
		"worker":          map[string]any{"type": "string", "enum": names, "description": "worker 槽位；只有一个槽位时可省略"},
		"taskFingerprint": map[string]any{"type": "string", "description": "执行计划返回的任务条件指纹，审批绑定该指纹"},
		"taskId":          map[string]any{"type": "string", "description": "按执行计划领取任务，只提供 taskId，宿主使用已保存的执行条件与依赖结果"},
		"task":            map[string]any{"type": "string", "description": "完整、自包含的子任务"},
		"materials":       map[string]any{"type": "string", "description": "明确提供的文本材料"}}, "anyOf": []any{map[string]any{"required": []string{"task"}}, map[string]any{"required": []string{"taskId", "taskFingerprint"}}}})
	return d
}

func (s *Service) selectedWorker(ctx context.Context, p storage.RunConfigSnapshot, runID string, args delegateArgs) (string, storage.RuntimeConfigRevision, bool, error) {
	resources, err := s.resourcesFor(ctx, p, runID)
	if err != nil {
		return "", storage.RuntimeConfigRevision{}, false, err
	}
	if len(resources.Workers) == 0 {
		return "", storage.RuntimeConfigRevision{}, false, nil
	}
	name := strings.TrimSpace(args.Worker)
	if name == "" {
		if len(resources.Workers) != 1 {
			return "", storage.RuntimeConfigRevision{}, true, errors.New("请明确选择 worker 槽位")
		}
		for key := range resources.Workers {
			name = key
		}
	}
	worker, ok := resources.Workers[name]
	if !ok {
		return "", storage.RuntimeConfigRevision{}, true, errors.New("worker 不在本任务起始配置中")
	}
	return name, worker, true, nil
}

func (s *Service) workerLimit(ctx context.Context, p storage.RunConfigSnapshot, runID string, name string) string {
	if !p.Delegation.Enabled || !p.AllowsTool("local__delegate_task") {
		return "该执行配置未允许委派"
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err.Error()
	}
	if run.Depth != 0 || run.Depth+1 > p.Delegation.MaxDepth {
		return "worker 槽位仅在主任务的委派深度内使用"
	}
	resources, err := s.resourcesFor(ctx, p, runID)
	if err != nil {
		return err.Error()
	}
	if _, ok := resources.Workers[name]; !ok {
		return "worker 不在任务起始快照中"
	}
	return ""
}

func (s *Service) executeWorker(ctx context.Context, parent storage.RunConfigSnapshot, sessionID, runID string, name string, worker storage.RuntimeConfigRevision, task string, planned *storage.TaskSpec) (string, error) {
	if deny := s.workerLimit(ctx, parent, runID, name); deny != "" {
		return "", errors.New(deny)
	}
	children, err := s.store.ListChildRuns(ctx, runID)
	if err != nil {
		return "", err
	}
	running := 0
	for _, child := range children {
		if child.Component == "worker" && child.Status == "running" {
			running++
		}
		if planned == nil && child.WorkerName == name {
			previous, err := s.store.FirstUserMessage(ctx, child.SessionID)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(previous) == strings.TrimSpace(task) {
				return "", errors.New("重复 worker 任务")
			}
		}
	}
	limit := parent.Delegation.MaxConcurrency
	if limit <= 0 {
		limit = storage.DefaultDelegationConcurrency
	}
	if running >= limit {
		return "", errors.New("并发子任务已达上限")
	}
	scope, err := s.sessionScopeMax(ctx, sessionID, parent)
	if err != nil {
		return "", err
	}
	childSession, err := s.store.CreateDelegationSession(ctx, parent.ContextID, parent.ConfigID, "子任务 → "+name+"（"+worker.Name+"）")
	if err != nil {
		return "", err
	}
	var childRun storage.Run
	var childCtx context.Context
	var childProfile storage.RunConfigSnapshot
	if planned != nil {
		childRun, childCtx, childProfile, err = s.runs.StartPlannedWorker(ctx, childSession.ID, runID, *planned)
	} else {
		childRun, childCtx, childProfile, err = s.runs.StartWorker(ctx, childSession.ID, runID, 1, name)
	}
	if err != nil {
		return "", err
	}
	return s.runDelegatedSubtask(ctx, childCtx, childRun, childSession, childProfile, parent, sessionID, task, scope)
}
