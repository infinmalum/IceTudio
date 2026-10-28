package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

// sessionCompactor implements the engine Compactor for one run. The summary
// request is an ordinary model send: it goes through the same guarded client
// (payload and exfiltration checks — the folded range carries tool results
// with their data scopes) and is recorded as a model step, so the audit
// trail shows exactly what was summarized.
type sessionCompactor struct {
	svc       *Service
	runID     string
	sessionID string
	chat      agent.ChatModel
}

var _ agent.Compactor = sessionCompactor{}

func (c sessionCompactor) Summarize(ctx context.Context, source config.Model, older []model.Message) (string, error) {
	previous, found, err := c.svc.store.GetSessionSummary(ctx, c.sessionID)
	if err != nil {
		return "", err
	}
	messages := []model.Message{
		{Role: "system", Content: "你是会话摘要器。把既有摘要与新对话合并为一份供后续对话使用的紧凑摘要：保留任务目标、已完成的步骤、关键工具结果和未决问题；按时间顺序陈述事实，不加评价；不超过 1200 字。"},
		{Role: "user", Content: summaryRequest(previous.Summary, found, older)},
	}
	for _, m := range older {
		messages[1].Files = append(messages[1].Files, m.Files...)
	}
	payload, err := json.Marshal(modelRequestSnapshot{Model: source, Messages: messages, OutputTokenLimit: 8192})
	if err != nil {
		return "", err
	}
	step, err := c.svc.store.BeginModelStep(ctx, c.runID, string(payload))
	if err != nil {
		return "", err
	}
	answer, chatErr := c.chat.Chat(context.WithValue(ctx, suppressSearchKey{}, true), source, messages, nil)
	if err := c.svc.store.CompleteModelStep(ctx, step.ID, chatErr); err != nil {
		return "", err
	}
	if chatErr != nil {
		return "", chatErr
	}
	summary := strings.TrimSpace(answer.Message.Content)
	if summary == "" {
		return "", errors.New("模型返回了空摘要")
	}
	if len(summary) > 16384 {
		summary = summary[:16384] + "\n[摘要过长，已截断]"
	}
	return summary, nil
}

// summaryRequest renders the folded range as flat text. The content is the
// same data the run already sends to this endpoint every turn; the session
// scope check applies to the summary send like to any other.
func summaryRequest(previous string, hasPrevious bool, older []model.Message) string {
	var b strings.Builder
	b.WriteString("请把以下对话压缩为一份摘要。")
	if hasPrevious && previous != "" {
		b.WriteString("\n\n[既有摘要（与其合并，不要重复叙述）：]\n" + previous)
	}
	b.WriteString("\n\n[待合并的对话：]")
	for _, m := range older {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "\n用户：%s", m.Content)
		case "assistant":
			if m.Content != "" {
				fmt.Fprintf(&b, "\n助手：%s", m.Content)
			}
			for _, call := range m.ToolCalls {
				fmt.Fprintf(&b, "\n助手调用工具 %s（%s）", call.Function.Name, call.Function.Arguments)
			}
		case "tool":
			fmt.Fprintf(&b, "\n工具结果(%s)：%s", m.ToolCallID, m.Content)
		}
	}
	return b.String()
}

// The plan bookkeeping tool is host-backed: plan updates replace the
// session's plan. It lives in the app layer (not the MCP hub) because
// availability follows the 执行配置 preference, and dispatching it needs the
// run's session scope.
var (
	updatePlanDefinition = func() model.Tool {
		t := model.Tool{Type: "function"}
		t.Function.Name = "local__update_plan"
		t.Function.Description = "创建或更新当前任务计划；提交观察到的 expected_revision（首次为 0），保留已有任务 id。dependsOn 引用前置任务 id，前置未完成不能开始后继。验收条件是声明，不代表已验证。返回当前完整计划；修订冲突时按返回的当前状态重新规划。"
		t.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"expected_revision":{"type":"integer","minimum":0},"steps":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{"id":{"type":"string","description":"稳定任务 ID；新步骤可省略由宿主生成"},"title":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","done"]},"note":{"type":"string"},"dependsOn":{"type":"array","items":{"type":"string"}},"acceptance":{"type":"string","description":"验收条件"},"inputRefs":{"type":"array","items":{"type":"string"},"description":"材料出处标识；不授予访问权限"}},"required":["title","status"]}}},"required":["expected_revision","steps"]}`)
		return t
	}()
)

// bookkeepingTools extends a run's tool executor with the built-ins and
// keeps their definitions aligned with what the gate will actually accept.
// Delegated subtask runs set `delegated`: they never get the plan
// bookkeeping, only the delegation tool itself when depth still allows it.
type bookkeepingTools struct {
	inner     agent.ToolExecutor
	svc       *Service
	profile   storage.RunConfigSnapshot
	sessionID string
	runID     string
	delegated bool
}

var _ agent.ToolExecutor = bookkeepingTools{}

func (t bookkeepingTools) Definitions(ctx context.Context) ([]model.Tool, error) {
	definitions, err := t.inner.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	return t.definitionsWith(ctx, definitions)
}

func (t bookkeepingTools) definitionsWith(ctx context.Context, definitions []model.Tool) ([]model.Tool, error) {
	for _, d := range basicDefinitions() {
		if t.profile.AllowsTool(d.Function.Name) {
			definitions = append(definitions, d)
		}
	}
	// Use the same frozen-target/binding guard as tool invocation.
	if target, scope, _, err := t.projectAccess(ctx); err == nil && target != "" {
		rawScope, err := json.Marshal(scope)
		if err != nil {
			return nil, err
		}
		for _, d := range projectDefinitions() {
			if scope.AccessMode != "write" && (d.Function.Name == "project__edit" || d.Function.Name == "project__transform") {
				continue
			}
			if t.profile.AllowsTool(d.Function.Name) {
				d.Function.Description += "\n宿主冻结的本任务工程范围：" + string(rawScope) + "。使用这里的 projectId；先查询范围内对象和当前 revision，再编辑。这里的 baseRevision 只记录任务起点，不代替最新查询。不得通过文件或命令绕过工程事务。"
				definitions = append(definitions, d)
			}
		}
	}
	if t.profile.ResourcesFrozen && len(t.profile.FrozenSkills) > 0 && t.profile.AllowsTool("local__load_skill") {
		definition := model.Tool{Type: "function"}
		definition.Function.Name = "local__load_skill"
		definition.Function.Description = "读取本任务固定版本的 Skill 指令及 references/scripts/assets；path 留空返回指令和资源清单。脚本仅作为内容读取，执行须通过现有写文件和命令工具及其授权。"
		definition.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"path":{"type":"string"}},"required":["name"]}`)
		found := false
		for i, d := range definitions {
			if d.Function.Name == definition.Function.Name {
				definitions[i] = definition
				found = true
			}
		}
		if !found {
			definitions = append(definitions, definition)
		}
	}
	if t.delegated {
		for _, def := range traceDefinitions() {
			if t.profile.AllowsTool(def.Function.Name) {
				definitions = append(definitions, def)
			}
		}
		spec, ok, err := t.svc.store.RunTaskSpec(ctx, t.runID)
		if err != nil {
			return nil, err
		}
		if ok && spec.AccessMode == "read" {
			filtered := []model.Tool{}
			for _, def := range definitions {
				effect, _ := t.svc.classifierFor(t.profile).Classify(def.Function.Name, "{}")
				if effect == mcptools.EffectRead || effect == mcptools.EffectPure {
					filtered = append(filtered, def)
				}
			}
			definitions = filtered
		}
	}
	if !t.delegated {
		if t.profile.PlanEnabled && t.profile.AllowsTool(updatePlanDefinition.Function.Name) {
			definitions = append(definitions, updatePlanDefinition)
		}
	}
	if !t.delegated && t.profile.PlanEnabled && t.profile.AllowsTool(taskPlanDefinition.Function.Name) {
		resources, err := t.svc.resourcesFor(ctx, t.profile, t.runID)
		if err == nil && len(resources.Workers) > 0 && delegationOffered(ctx, t.svc, t.profile, t.runID) {
			definitions = append(definitions, taskPlanDefinition)
		}
	}
	if delegationOffered(ctx, t.svc, t.profile, t.runID) {
		definitions = append(definitions, t.svc.delegationDefinition(ctx, t.profile, t.runID))
	}
	return definitions, nil
}

func (t bookkeepingTools) Call(ctx context.Context, name, arguments string) (string, error) {
	switch name {
	case "local__write_file", "local__edit_file", "local__exec":
		return t.basicCall(ctx, name, arguments)
	case "project__query", "project__select", "project__edit", "project__transform":
		return t.projectCall(ctx, name, arguments)
	case "local__search_worker_traces", "local__list_worker_traces", "local__get_worker_trace", "local__record_observation":
		return t.traceCall(ctx, name, arguments)
	case "local__load_skill":
		if !t.profile.ResourcesFrozen {
			return t.inner.Call(ctx, name, arguments)
		}
		var args struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", err
		}
		if !t.profile.EnablesSkill(args.Name) {
			return "", errors.New("未选择的 Skill")
		}
		for _, item := range t.profile.FrozenSkills {
			if item.Name == args.Name {
				return item.ReadResource(args.Path)
			}
		}
		return "", errors.New("Skill 不在任务起始快照中")
	case "local__update_plan":
		if t.delegated {
			return "", errors.New("委派子任务不提供规划")
		}
		if !t.profile.PlanEnabled {
			return "", errors.New("该执行配置未启用规划")
		}
		var args struct {
			Steps            []storage.PlanStep `json:"steps"`
			ExpectedRevision *int               `json:"expected_revision"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", errors.New("update_plan 参数必须是 JSON 对象")
		}
		if args.ExpectedRevision == nil {
			current, _, err := t.svc.store.GetSessionPlan(ctx, t.sessionID)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(current)
			return "", fmt.Errorf("需要 expected_revision；当前状态 %s", raw)
		}
		plan, err := t.svc.store.CompareAndSetSessionPlan(ctx, t.sessionID, args.Steps, *args.ExpectedRevision)
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(plan)
		return string(raw), err
	case "local__task_plan":
		return t.taskPlan(ctx, arguments)
	case "local__delegate_task":
		return t.svc.executeDelegation(ctx, t.profile, t.sessionID, t.runID, arguments)
	default:
		return t.inner.Call(ctx, name, arguments)
	}
}

// sessionScopeMax folds the session's accumulated data scopes into the
// single most restrictive token; a session always carries user input, so it
// never returns empty.
func (s *Service) sessionScopeMax(ctx context.Context, sessionID string, profile storage.RunConfigSnapshot) (string, error) {
	scopes, err := s.sessionScopes(ctx, sessionID, profile)
	if err != nil {
		return "", err
	}
	scope := ""
	for _, item := range scopes {
		scope = storage.MaxScope(scope, item)
	}
	if scope == "" {
		scope = storage.ScopePersonal
	}
	return scope, nil
}
