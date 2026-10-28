package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"icetudio/internal/agent"
	"icetudio/internal/mcptools"
	"icetudio/internal/storage"
)

// toolGate combines frozen run capabilities with the owning context's live policy.
// Components may further narrow the inherited permission ceiling.
type toolGate struct {
	svc       *Service
	contextID string
	snapshot  *storage.RunConfigSnapshot
	runID     string
	parent    *storage.RunConfigSnapshot
}

var _ agent.ToolGate = toolGate{}

// approvalRank orders the modes from most to least permissive; the stricter
// mode wins for delegated subtasks.
var approvalRank = map[string]int{"automatic": 0, "semi": 1, "manual": 2}

// Resolve only live authorization; 执行配置 choices belong to the Run snapshot.
func (g toolGate) effectiveSnapshot(ctx context.Context) (storage.RunConfigSnapshot, error) {
	if g.snapshot == nil {
		return storage.RunConfigSnapshot{}, fmt.Errorf("工具执行缺少运行配置快照")
	}
	a, err := g.svc.store.GetLocalContext(ctx, g.contextID)
	if err != nil {
		return storage.RunConfigSnapshot{}, err
	}
	p := *g.snapshot
	p.Policy = restrictPolicy(g.snapshot.Policy, a.Policy)
	if g.runID != "" {
		policy, err := g.svc.restrictRunPolicy(ctx, g.runID, p)
		if err != nil {
			return storage.RunConfigSnapshot{}, err
		}
		p.Policy = policy
	}
	if g.parent != nil {
		p.Policy = restrictPolicy(p.Policy, g.parent.Policy)
		return p, nil
	}
	if approvalRank[g.snapshot.Policy.ApprovalMode] > approvalRank[p.Policy.ApprovalMode] {
		p.Policy.ApprovalMode = g.snapshot.Policy.ApprovalMode
	}
	return p, nil
}

func stricterMode(a, b string) string {
	if approvalRank[a] >= approvalRank[b] {
		return a
	}
	return b
}

func (g toolGate) Gate(ctx context.Context, req agent.ToolRequest) agent.Decision {
	profile, err := g.effectiveSnapshot(ctx)
	if err != nil {
		return agent.Decision{Deny: "读取运行配置失败: " + err.Error()}
	}
	if deny := g.hardLimits(ctx, profile, req); deny != "" {
		return agent.Decision{Deny: deny}
	}
	effect, sensitivity := g.svc.classifierFor(profile).Classify(req.Tool, req.Arguments)
	// A changed binary or tool schema suspends automatic execution based on
	// the old registration, in every approval mode.
	if g.svc.endpointChanged(serverOf(req.Tool)) {
		return agent.Decision{Confirm: "MCP 服务指纹变化，基于旧登记的自动执行已暂停，需要重新确认"}
	}
	switch profile.Policy.ApprovalMode {
	case "manual":
		return agent.Decision{Confirm: "当前上下文为全人工审批模式"}
	case "automatic":
		// Unknown reads and credentials never auto-execute, even in
		// automatic mode; everything else policy already allows proceeds.
		if sensitivity == mcptools.SensCredential || sensitivity == mcptools.SensUnknown {
			return agent.Decision{Confirm: "敏感度未知或涉及凭据的访问需要确认，全自动模式也不自动执行"}
		}
		return agent.Decision{}
	default: // semi
		if effect == mcptools.EffectPure {
			return agent.Decision{}
		}
		if effect == mcptools.EffectRead && (sensitivity == mcptools.SensPublic || sensitivity == mcptools.SensWorkspace) {
			return agent.Decision{}
		}
		return agent.Decision{Confirm: confirmReason(effect, sensitivity)}
	}
}

// Recheck re-validates an approved call right before dispatch. It only
// denies: approvals can expire through policy changes, never through
// relaxation here.
func (g toolGate) Recheck(ctx context.Context, req agent.ToolRequest) agent.Decision {
	profile, err := g.effectiveSnapshot(ctx)
	if err != nil {
		return agent.Decision{Deny: "读取运行配置失败: " + err.Error()}
	}
	if deny := g.hardLimits(ctx, profile, req); deny != "" {
		return agent.Decision{Deny: deny}
	}
	if g.runID != "" {
		decision := g.Gate(ctx, req)
		if decision.Deny != "" {
			return decision
		}
		if decision.Confirm != "" {
			call, err := g.svc.store.FindRunToolCall(ctx, g.runID, req.CallID)
			if err != nil {
				return agent.Decision{Deny: "无法复核批准来源"}
			}
			if call.ApprovalSource != storage.ApprovalUser {
				return agent.Decision{Deny: "执行前策略已收紧，原自动批准不再有效"}
			}
		}
	}
	return agent.Decision{}
}

// hardLimits returns the non-negotiable refusals: the tool must be offered
// and allowed, skill reads must target an enabled skill, and file reads must
// stay inside the workspace boundary when one is configured. A user
// confirmation can never override these.
func (g toolGate) hardLimits(ctx context.Context, profile storage.RunConfigSnapshot, req agent.ToolRequest) string {
	if mcptools.IsProjectTool(req.Tool) {
		run, err := g.svc.store.GetRun(ctx, g.runID)
		if err != nil {
			return "音乐工具需要持久运行记录：" + err.Error()
		}
		t := bookkeepingTools{svc: g.svc, runID: g.runID, sessionID: run.SessionID}
		_, scope, _, err := t.projectAccess(ctx)
		if err != nil {
			return err.Error()
		}
		if scope.AccessMode != "write" && (req.Tool == "project__edit" || req.Tool == "project__transform") {
			return "本任务为只读，不能编辑工程"
		}
	}
	if g.runID != "" {
		spec, ok, err := g.svc.store.RunTaskSpec(ctx, g.runID)
		if err != nil {
			return err.Error()
		}
		if ok && spec.AccessMode == "read" {
			effect, _ := g.svc.classifierFor(profile).Classify(req.Tool, req.Arguments)
			if effect != mcptools.EffectRead && effect != mcptools.EffectPure {
				return "只读任务不能执行写入、命令或未知副作用工具"
			}
		}
	}

	if req.Tool == "local__write_file" {
		var args mcptools.WriteArgs
		if err := decodeRequiredOperation(req.Arguments, &args, "path", "content"); err != nil {
			return err.Error()
		}
		if !filepath.IsAbs(args.Path) {
			return "写入文件需绝对路径"
		}
	}
	if req.Tool == "local__edit_file" {
		var args mcptools.EditArgs
		if err := decodeRequiredOperation(req.Arguments, &args, "path", "oldText", "newText"); err != nil {
			return err.Error()
		}
		if !filepath.IsAbs(args.Path) || args.OldText == "" {
			return "编辑文件需绝对路径与非空 oldText"
		}
	}
	if g.snapshot != nil && (req.Tool == "local__read_file" || req.Tool == "local__write_file" || req.Tool == "local__edit_file") && !g.snapshot.Policy.Permissions.Unrestricted {
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(req.Arguments), &args) != nil || !withinAny(args.Path, g.snapshot.Policy.Permissions.WorkspacePaths) {
			return "路径超出任务开始时允许的工作区范围"
		}
	}
	if req.Tool == "local__exec" {
		var args mcptools.ExecArgs
		if err := decodeRequiredOperation(req.Arguments, &args, "executable", "args", "cwd"); err != nil {
			return "无效命令参数：" + err.Error()
		}
		if err := mcptools.ValidateExec(args); err != nil {
			return err.Error()
		}
		if !profile.Policy.Permissions.Unrestricted {
			if !withinAny(args.Cwd, profile.Policy.Permissions.WorkspacePaths) {
				return "命令工作目录超出允许工作区"
			}
			found := false
			for _, name := range profile.Policy.Permissions.Commands {
				if name == args.Executable {
					found = true
				}
			}
			if !found {
				return "命令不在允许列表中"
			}
		}
	}
	if !profile.AllowsTool(req.Tool) {
		return fmt.Sprintf("未被当前运行允许的工具调用 %q", req.Tool)
	}
	if req.Tool == "local__update_plan" || req.Tool == "local__task_plan" {
		if g.parent != nil {
			return "委派子任务不提供规划"
		}
		if !profile.PlanEnabled || profile.Execution.Workflow == "fixed" {
			return "该执行配置未启用规划"
		}
	}
	if req.Tool == "local__delegate_task" {
		if deny := g.delegateLimit(ctx, profile, req.Arguments); deny != "" {
			return deny
		}
	}
	if req.Tool == "local__load_skill" {
		var args struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(req.Arguments), &args) != nil || !profile.EnablesSkill(args.Name) {
			return fmt.Sprintf("未被当前运行启用的 Skill %q", args.Name)
		}
		if profile.ResourcesFrozen {
			found := false
			for _, item := range profile.FrozenSkills {
				if item.Name == args.Name {
					found = true
				}
			}
			if !found {
				return "Skill 不在任务起始快照中"
			}
		}
	}
	if (req.Tool == "local__read_file" || req.Tool == "local__write_file" || req.Tool == "local__edit_file") && !profile.Policy.Permissions.Unrestricted {
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(req.Arguments), &args) != nil || args.Path == "" {
			return "读取文件缺少有效路径"
		}
		if !withinAny(args.Path, profile.Policy.Permissions.WorkspacePaths) {
			return "路径超出该上下文允许的工作区范围"
		}
	}
	return ""
}

// delegateLimit enforces the host-side delegation bounds before dispatch:
// the frozen 执行配置 switch, worker slots and depth budget.
func (g toolGate) delegateLimit(ctx context.Context, profile storage.RunConfigSnapshot, arguments string) string {
	if !profile.Delegation.Enabled {
		return "该执行配置未启用委派"
	}
	if g.runID != "" {
		run, err := g.svc.store.GetRun(ctx, g.runID)
		if err != nil {
			return "读取任务记录失败: " + err.Error()
		}
		if run.Depth+1 > profile.Delegation.MaxDepth {
			return fmt.Sprintf("超过委派深度上限（%d）", profile.Delegation.MaxDepth)
		}
	}
	var args delegateArgs
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return "delegate_task 参数必须是 JSON 对象"
	}
	args, _, err := g.svc.resolvePlannedTask(ctx, profile, g.runID, args)
	if err != nil {
		return err.Error()
	}
	name, _, selected, err := g.svc.selectedWorker(ctx, profile, g.runID, args)
	if err != nil {
		return err.Error()
	}
	if selected {
		return g.svc.workerLimit(ctx, profile, g.runID, name)
	}
	return "执行配置未配置 worker 槽位"
}

func confirmReason(effect mcptools.Effect, sensitivity mcptools.Sensitivity) string {
	switch effect {
	case mcptools.EffectWrite:
		return "写入操作需要确认"
	case mcptools.EffectExecute:
		return "命令执行需要确认"
	case mcptools.EffectExternal:
		return "外部 MCP 工具的副作用无法由本机约束，需要确认"
	case mcptools.EffectRead:
		return "读取敏感度未知或敏感的数据需要确认"
	default:
		return "效果未知的工具调用需要确认"
	}
}

// serverOf extracts the MCP server prefix of a tool name; built-in tools
// report "local".
func serverOf(tool string) string {
	for i := 0; i < len(tool); i++ {
		if tool[i] == '_' && i+1 < len(tool) && tool[i+1] == '_' {
			return tool[:i]
		}
	}
	return "local"
}

// withinAny reports whether path resolves inside one of the roots.
func withinAny(path string, roots []string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
			rootAbs = resolved
		}
		if under(abs, rootAbs) {
			return true
		}
	}
	return false
}

func under(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
