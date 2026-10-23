package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

// The delegation tool dispatches a bounded task to a configured worker slot.
// It uses a frozen 执行配置 projection and the root context's authorization.

// maxDelegationMaterials bounds the explicitly passed material text.
const maxDelegationMaterials = 64 * 1024

var delegateDefinition = func() model.Tool {
	t := model.Tool{Type: "function"}
	t.Function.Name = "local__delegate_task"
	t.Function.Description = "把明确的子任务交给配置的 worker 执行。子任务拥有独立上下文，不会访问目标的会话或长期记忆，只能使用此处明确给出的任务描述和材料；返回子任务的最终结果。"
	t.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"worker":{"type":"string","description":"配置的 worker 槽位"},"task":{"type":"string","description":"交给子任务的完整任务描述，自包含"},"materials":{"type":"string","description":"执行任务所需的明确材料（可选）"}},"required":["task"]}`)
	return t
}()

type delegateArgs struct {
	TaskID          string `json:"taskId"`
	TaskFingerprint string `json:"taskFingerprint"`
	Worker          string `json:"worker"`
	Task            string `json:"task"`
	Materials       string `json:"materials"`
}

// delegationOffered reports whether a run may offer the delegation tool at
// all: the 执行配置 switch, configured worker slots and the depth budget must
// all allow it. The gate re-checks every call against the live policy.
func delegationOffered(ctx context.Context, svc *Service, profile storage.RunConfigSnapshot, runID string) bool {
	if !profile.Delegation.Enabled || !profile.AllowsTool(delegateDefinition.Function.Name) {
		return false
	}
	resources, err := svc.resourcesFor(ctx, profile, runID)
	if err == nil && len(resources.Workers) > 0 && runID != "" {
		run, err := svc.store.GetRun(ctx, runID)
		if err == nil && run.Depth == 0 && run.Depth+1 <= profile.Delegation.MaxDepth {
			return true
		}
	}
	return false
}

// executeDelegation runs one delegated subtask synchronously inside the
// parent's tool call. The child run is a first-class persisted run: it has
// its own delegation session (hidden from the normal list), its own 执行配置
// snapshot of the selected worker, its own recorder, gate and guarded sends.
func (s *Service) executeDelegation(ctx context.Context, parentProfile storage.RunConfigSnapshot, parentSessionID, parentRunID string, raw string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var args delegateArgs
	if err := decodeOperation(raw, &args); err != nil {
		return "", errors.New("delegate_task 参数必须是 JSON 对象")
	}
	var planned *storage.TaskSpec
	var err error
	args, planned, err = s.resolvePlannedTask(ctx, parentProfile, parentRunID, args)
	if err != nil {
		return "", err
	}
	task := strings.TrimSpace(args.Task)
	if task == "" || len(task) > 10000 {
		return "", errors.New("子任务描述长度必须在 1 到 10000 字节之间")
	}
	materials := strings.TrimSpace(args.Materials)
	if len(materials) > maxDelegationMaterials {
		return "", fmt.Errorf("传入材料超过 %d 字节上限", maxDelegationMaterials)
	}
	userContent := task
	if materials != "" {
		userContent += "\n\n[父任务提供的材料]\n" + materials
	}
	name, worker, selected, err := s.selectedWorker(ctx, parentProfile, parentRunID, args)
	if err != nil {
		return "", err
	}
	if !selected {
		return "", errors.New("执行配置未配置 worker 槽位")
	}
	return s.executeWorker(ctx, parentProfile, parentSessionID, parentRunID, name, worker, userContent, planned)
}

// runDelegatedSubtask executes the child loop and maps its terminal state to
// the parent-visible tool result. `parentCtx` is the delegating run's
// context, `ctx` the child's own (parent-derived, budget-bounded) context.
func (s *Service) runDelegatedSubtask(parentCtx context.Context, ctx context.Context, childRun storage.Run, childSession storage.Session, childProfile storage.RunConfigSnapshot, parentProfile storage.RunConfigSnapshot, parentSessionID string, userContent, materialScope string) (string, error) {
	finalize := func(status, detail string) {
		_ = s.runs.Finish(childRun.ID, status, detail)
		s.publish(Event{Type: "run.updated", SessionID: childRun.SessionID, RunID: childRun.ID, Detail: "委派子任务结束"})
	}
	s.publish(Event{Type: "run.started", SessionID: childRun.SessionID, RunID: childRun.ID})
	defer finalizeOnPanic(s, childRun.ID)
	if _, err := s.store.AddMessage(ctx, childRun.SessionID, childRun.ID, storage.RoleUser, userContent); err != nil {
		finalize("failed", err.Error())
		return "", fmt.Errorf("子任务写入失败: %w", err)
	}
	parentScopes, err := s.sessionScopes(ctx, parentSessionID, parentProfile)
	if err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	if err := s.store.AccumulateContextScopes(ctx, childRun.SessionID, parentScopes); err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	if err := s.store.AccumulateContextScopes(ctx, childRun.SessionID, []string{materialScope}); err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	s.mu.RLock()
	source := s.config.Model
	timeout := s.ApprovalTimeout
	s.mu.RUnlock()
	if childProfile.Model.Provider != "" {
		source = childProfile.Model
	}
	if source.Name == "" {
		finalize("failed", "请先选择模型 ID")
		return "", errors.New("子任务无法启动：worker 配置没有可用模型")
	}
	if err := s.exfilCheckRun(ctx, childRun.ID, childRun.SessionID, source, childProfile); err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	budgetSeconds := childProfile.Delegation.MaxSeconds
	if budgetSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(budgetSeconds)*time.Second)
		defer cancel()
	}
	resources, err := s.resourcesFor(ctx, parentProfile, childRun.ParentRunID)
	if err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	structured := (resources.Root.Data.Execution.Workflow == "dynamic" || resources.Root.Data.Execution.Workflow == "fixed") && childRun.WorkerName != ""
	history := []model.Message{{Role: "system", Content: s.delegationSystemPrompt(parentProfile, childProfile)}}
	if structured {
		history[0].Content += "\n\n" + workerOutcomeContract
	}
	files, err := s.store.RunInputFiles(ctx, childRun.ID)
	if err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	history = append(history, model.Message{Role: "user", Content: userContent, Files: files})
	runTools := s.tools
	if hub, ok := s.tools.(*mcptools.Hub); ok {
		all, names := childProfile.AllMCPServers, childProfile.EnabledMCPServers
		if len(childProfile.AllowedTools) > 0 {
			all = false
			names = nil
			seen := map[string]bool{}
			for _, tool := range childProfile.AllowedTools {
				if childProfile.AllowsTool(tool) {
					server := serverOf(tool)
					if !seen[server] {
						names = append(names, server)
						seen[server] = true
					}
				}
			}
		}
		runTools = hub.ForServers(all, names)
	}
	if spec, ok, err := s.store.RunTaskSpec(ctx, childRun.ID); err != nil {
		finalize("failed", err.Error())
		return "", err
	} else if ok && spec.AccessMode == "read" {
		if hub, ok := s.tools.(*mcptools.Hub); ok {
			runTools = hub.ForServers(false, []string{"local"})
		}
	}
	runner := newDiscoveryTools(bookkeepingTools{inner: runTools, svc: s, profile: childProfile, sessionID: childRun.SessionID, runID: childRun.ID, delegated: true})
	_, err = runner.Definitions(ctx)
	if err != nil {
		finalize("failed", err.Error())
		return "", err
	}
	if err := s.SyncMCPEndpoints(ctx); err != nil {
		finalize("failed", err.Error())
		return "", err
	}

	guarded := guardedChat{svc: s, runID: childRun.ID, inner: s.chat, payloadCheck: func(c context.Context, m []model.Message, t []model.Tool) error {
		return s.checkPayload(c, source, childProfile.ContextID, childRun.SessionID, m, t)
	}, check: func(checkCtx context.Context) error {
		// Frozen grants and current context authorization cover every send.
		return s.exfilCheckRun(checkCtx, childRun.ID, childRun.SessionID, source, childProfile)
	}}
	engine := agent.Engine{
		Model:    guarded,
		Tools:    runner,
		Recorder: runRecorder{svc: s, runID: childRun.ID, sessionID: childRun.SessionID, classifier: s.classifierFor(childProfile)},
		Gate:     toolGate{svc: s, contextID: childProfile.ContextID, snapshot: &childProfile, runID: childRun.ID, parent: &parentProfile},
		Approver: s,
		OnDelta: func(text string) error {
			s.appendStream(childRun.ID, childRun.SessionID, text)
			return nil
		},
		Compactor:       sessionCompactor{svc: s, runID: childRun.ID, sessionID: childRun.SessionID, chat: guarded},
		ContextBudget:   childProfile.ContextBudget,
		ApprovalTimeout: timeout,
	}
	finalText, runErr := engine.Run(ctx, source, history)
	scopes, scopeErr := s.sessionScopes(context.WithoutCancel(ctx), childRun.SessionID, childProfile)
	if scopeErr == nil {
		scopeErr = s.store.AccumulateContextScopes(context.WithoutCancel(ctx), parentSessionID, scopes)
	}
	if scopeErr != nil {
		finalize("failed", scopeErr.Error())
		return "", scopeErr
	}
	if structured && runErr == nil {
		outcome, err := s.validateWorkerOutcome(ctx, childRun.ID, finalText)
		if err != nil {
			runErr = err
		} else if outcome.Status != "succeeded" {
			runErr = fmt.Errorf("worker %s：%s", outcome.Status, outcome.Content)
		} else {
			finalText = outcome.Content
		}
	}
	switch {
	case runErr == nil:
		scopes, err := s.sessionScopes(ctx, childRun.SessionID, childProfile)
		if err != nil {
			finalize("failed", err.Error())
			return "", err
		}
		if err := s.store.AccumulateContextScopes(ctx, parentSessionID, scopes); err != nil {
			finalize("failed", err.Error())
			return "", err
		}
		finalize("completed", "")
		return finalText, nil
	case parentCtx.Err() != nil:
		// The parent context died (user cancel, timeout, host shutdown): the
		// child is cancelled with it — a known outcome, never unknown.
		finalize("cancelled", "父任务已取消")
		return "子任务已取消。", nil
	case errors.Is(runErr, context.Canceled):
		finalize("cancelled", "")
		return "子任务已被用户取消。", nil
	case errors.Is(runErr, context.DeadlineExceeded):
		finalize("failed", "子任务超过时间预算")
		return "", errors.New("子任务超过时间预算")
	default:
		finalize("failed", runErr.Error())
		return "", fmt.Errorf("子任务失败：%s", runErr)
	}
}

func finalizeOnPanic(s *Service, runID string) {
	if recovered := recover(); recovered != nil {
		_ = s.runs.Finish(runID, "failed", fmt.Sprintf("子任务内部错误: %v", recovered))
		panic(recovered)
	}
}

// delegationSystemPrompt composes the child's system prompt: the target
// 执行配置's own prompt plus a fixed note that this is a delegated subtask
// working only from explicitly passed materials. Plan injection never runs
// for delegated sessions.
func (s *Service) delegationSystemPrompt(parentProfile, childProfile storage.RunConfigSnapshot) string {
	prompt := s.systemPrompt(childProfile)
	note := fmt.Sprintf("[委派说明] 本会话是上下文「%s」中主任务委派的子任务：只使用用户消息中给出的任务描述与材料完成工作，不要假设可以访问你既有的会话或计划。", parentProfile.ContextName)
	if prompt == "" {
		return note
	}
	return prompt + "\n\n" + note
}

// intersectPermissions combines frozen and live permission ceilings.
// An empty list means nothing granted.
func intersectPermissions(parent, target storage.PermissionScope) storage.PermissionScope {
	out := storage.PermissionScope{}
	out.Unrestricted = parent.Unrestricted && target.Unrestricted
	switch {
	case parent.Unrestricted:
		out.WorkspacePaths = append([]string(nil), target.WorkspacePaths...)
	case target.Unrestricted:
		out.WorkspacePaths = append([]string(nil), parent.WorkspacePaths...)
	default:
		b := map[string]bool{}
		for _, path := range target.WorkspacePaths {
			b[path] = true
		}
		for _, path := range parent.WorkspacePaths {
			if b[path] {
				out.WorkspacePaths = append(out.WorkspacePaths, path)
			}
		}
	}
	out.Commands = intersectLists(parent.Commands, target.Commands)
	out.NetworkHosts = intersectLists(parent.NetworkHosts, target.NetworkHosts)
	return out
}

func intersectLists(a, b []string) []string {
	set := map[string]bool{}
	for _, item := range b {
		set[item] = true
	}
	out := []string{}
	for _, item := range a {
		if set[item] {
			out = append(out, item)
		}
	}
	return out
}
