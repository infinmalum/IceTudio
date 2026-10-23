package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
)

// ChatModel is the minimal model interface the default loop needs.
type ChatModel interface {
	Chat(context.Context, config.Model, []model.Message, []model.Tool) (model.ChatResponse, error)
}

// StreamingChatModel is the optional streaming surface. ChatStream forwards
// every incremental text fragment to onDelta as it arrives and returns the
// complete merged message as its ChatResponse. Adapters that only implement
// ChatModel keep working: the loop falls back to Chat.
type StreamingChatModel interface {
	ChatModel
	ChatStream(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool, onDelta func(string) error) (model.ChatResponse, error)
}

// ToolExecutor is the minimal tool surface the loop needs. The MCP hub
// implements it; tests can supply fakes.
type ToolExecutor interface {
	Definitions(ctx context.Context) ([]model.Tool, error)
	Call(ctx context.Context, name, arguments string) (string, error)
}

// Recorder persists run lifecycle facts around the loop. Implementations must
// guarantee that a successful BeginTool means the executing state is durably
// stored before the tool runs, and that FinishTool commits even when the run
// context is already cancelled (the engine passes a cancellation-free
// context). If persistence fails the run must fail rather than continue.
type Recorder interface {
	// ModelRequest persists the request snapshot (model parameters, prompt
	// history and tool definitions) before the model is called.
	ModelRequest(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (string, error)
	// ModelDone closes the model request step with its outcome.
	ModelDone(ctx context.Context, stepID string, chatErr error) error
	// Assistant persists an assistant message and, when it requests tool
	// calls, the pending call records, atomically.
	Assistant(ctx context.Context, msg model.Message) error
	// ResolveCall maps a model-provided call ID to its stable persisted row
	// ID, so approval decisions bind to the durable record.
	ResolveCall(ctx context.Context, callID string) (string, error)
	// ApproveAuto records that policy granted a pending call without user
	// interaction; a non-nil error forbids dispatch.
	ApproveAuto(ctx context.Context, callID string) error
	// BeginTool moves a call to executing; a non-nil error forbids dispatch.
	BeginTool(ctx context.Context, callID string) error
	// FinishTool records a terminal outcome (succeeded, failed, cancelled,
	// execution_unknown) for the call.
	FinishTool(ctx context.Context, callID, state, result, errText string) error
	// SaveSummary persists a compaction summary covering the oldest
	// `covered` entries of the run's history window. A non-nil error aborts
	// the run: an unpersisted summary would be re-derived and re-sent.
	SaveSummary(ctx context.Context, covered int, summary string) error
}

// ToolRequest identifies one model-requested tool invocation at the gate.
type ToolRequest struct {
	CallID    string
	Tool      string
	Arguments string
}

// Decision is the permission gate's verdict for one pending call. Deny is a
// hard refusal the call can never recover from; Confirm requires an explicit
// user approval bound to this exact call and its arguments before dispatch.
// An empty Decision means policy already allows the call.
type Decision struct {
	Deny    string
	Confirm string
}

// ToolGate is the host's centralized permission check. Gate decides before
// any approval; Recheck re-validates an already-approved call right before
// dispatch (policy may have tightened while the approval was pending) and
// may only deny or allow — it never widens the earlier decision.
type ToolGate interface {
	Gate(ctx context.Context, req ToolRequest) Decision
	Recheck(ctx context.Context, req ToolRequest) Decision
}

// ApprovalWaiter blocks until the user decision for one persisted call
// arrives: approved, denied/cancelled (approved=false, note explains), or an
// infrastructure error. The persisted call state is authoritative — waiting
// implementations must resolve the outcome from the store, not from memory.
type ApprovalWaiter interface {
	WaitApproval(ctx context.Context, callRowID string) (approved bool, note string, err error)
}

type Engine struct {
	Model        ChatModel
	Tools        ToolExecutor
	Recorder     Recorder
	AllowedTools []string
	Gate         ToolGate
	Approver     ApprovalWaiter
	OnTool       func(name, result string) error
	// OnDelta, when set together with a StreamingChatModel, receives each
	// streamed text fragment for live broadcast; the final message is still
	// only committed through Recorder.Assistant.
	OnDelta func(text string) error

	// Compactor folds older complete turns into a persisted summary when
	// the history approaches the model window. It only acts together with a
	// positive ContextBudget: an unknown window (0) never compacts.
	Compactor Compactor
	// ContextBudget is the model context window in tokens, declared by the
	// run's RuntimeConfig (执行配置).
	ContextBudget int
	// InitialSummary is the summary that already covers history before this
	// run's window; the engine mirrors it into the system prompt and keeps
	// it current as it compacts further.
	InitialSummary string

	// ApprovalTimeout bounds how long a call may sit waiting for a user
	// decision; 0 means the 5-minute default.
	ApprovalTimeout time.Duration

	MaxTurns       int
	MaxToolOutput  int
	MaxTotalOutput int
}

const (
	defaultMaxTurns       = 8
	defaultMaxToolOutput  = 16 * 1024
	defaultMaxTotalOutput = 256 * 1024
	defaultApprovalWait   = 5 * time.Minute
)

func (e Engine) budget() (int, int, int) {
	turns, output, total := e.MaxTurns, e.MaxToolOutput, e.MaxTotalOutput
	if turns <= 0 {
		turns = defaultMaxTurns
	}
	if output <= 0 {
		output = defaultMaxToolOutput
	}
	if total <= 0 {
		total = defaultMaxTotalOutput
	}
	return turns, output, total
}

// Run executes the default tool loop against the model. Every model request
// and tool dispatch is persisted through rec before it happens, so a crash at
// any point leaves an explicit record: requested-not-dispatched calls stay
// pending for recovery, executed calls whose result never landed become
// execution_unknown, and no whole tool batch is ever replayed.
func (e Engine) Run(ctx context.Context, source config.Model, history []model.Message) (string, error) {
	if e.Model == nil || e.Tools == nil {
		return "", errors.New("Agent 尚未初始化")
	}
	rec := e.Recorder
	if rec == nil {
		rec = noopRecorder{}
	}
	maxTurns, maxToolOutput, maxTotalOutput := e.budget()
	messages := append([]model.Message(nil), history...)
	definitions, err := e.Tools.Definitions(ctx)
	if err != nil {
		return "", err
	}
	configured := map[string]bool{}
	for _, name := range e.AllowedTools {
		configured[name] = true
	}
	allowed := make(map[string]bool, len(definitions))
	visible := definitions[:0]
	for _, definition := range definitions {
		name := definition.Function.Name
		if e.AllowedTools != nil && !configured[name] {
			continue
		}
		allowed[name] = true
		visible = append(visible, definition)
	}
	definitions = visible
	totalOutput := 0
	baseSystem := ""
	if len(messages) > 0 && messages[0].Role == "system" {
		baseSystem = messages[0].Content
	}
	summary := e.InitialSummary
	applySummary := func() {
		content := baseSystem
		if summary != "" {
			if content != "" {
				content += "\n\n"
			}
			content += summaryBlock(summary)
		}
		switch {
		case len(messages) > 0 && messages[0].Role == "system":
			messages[0].Content = content
		case content != "":
			messages = append([]model.Message{{Role: "system", Content: content}}, messages...)
		}
	}
	applySummary()
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if dynamic, ok := e.Tools.(interface{ DynamicDefinitions() bool }); ok && dynamic.DynamicDefinitions() && turn > 0 {
			fresh, err := e.Tools.Definitions(ctx)
			if err != nil {
				return "", err
			}
			definitions = nil
			allowed = map[string]bool{}
			for _, d := range fresh {
				name := d.Function.Name
				if e.AllowedTools != nil && !configured[name] {
					continue
				}
				definitions = append(definitions, d)
				allowed[name] = true
			}
		}
		if e.Compactor != nil && e.ContextBudget > 0 {
			if err := e.compactTurn(ctx, rec, source, &messages, &summary, definitions); err != nil {
				return "", fmt.Errorf("上下文压缩: %w", err)
			}
			applySummary()
		}
		stepID, err := rec.ModelRequest(ctx, source, messages, definitions)
		if err != nil {
			return "", fmt.Errorf("记录模型请求失败: %w", err)
		}
		var answer model.ChatResponse
		var chatErr error
		if streamer, ok := e.Model.(StreamingChatModel); ok && e.OnDelta != nil {
			answer, chatErr = streamer.ChatStream(ctx, source, messages, definitions, e.OnDelta)
		} else {
			answer, chatErr = e.Model.Chat(ctx, source, messages, definitions)
		}
		if recErr := rec.ModelDone(ctx, stepID, chatErr); recErr != nil {
			return "", fmt.Errorf("记录模型响应失败: %w", recErr)
		}
		if chatErr != nil {
			return "", chatErr
		}
		if len(answer.Message.ToolCalls) == 0 {
			if answer.FinishReason == "tool_calls" || strings.TrimSpace(answer.Message.Content) == "" {
				return "", errors.New("模型未返回有效回答")
			}
			if err := rec.Assistant(ctx, answer.Message); err != nil {
				return "", fmt.Errorf("保存助手消息失败: %w", err)
			}
			return answer.Message.Content, nil
		}
		// Persist the assistant turn together with all pending tool call
		// records before dispatching any of them. Malformed call IDs fail
		// here and nothing gets dispatched.
		if err := rec.Assistant(ctx, answer.Message); err != nil {
			return "", fmt.Errorf("保存工具调用请求失败: %w", err)
		}
		messages = append(messages, answer.Message)
		for _, call := range answer.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			result, state, stateErr := e.dispatch(ctx, rec, allowed, call, maxToolOutput)
			if stateErr != nil {
				return "", stateErr
			}
			if state == CallStateSucceeded || state == CallStateFailed {
				totalOutput += len(result)
				if totalOutput > maxTotalOutput {
					return "", fmt.Errorf("工具输出超过 %d 字节预算", maxTotalOutput)
				}
			}
			if e.OnTool != nil {
				if err := e.OnTool(call.Function.Name, result); err != nil {
					return "", fmt.Errorf("保存工具结果: %w", err)
				}
			}
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
	return "", fmt.Errorf("Agent 超过 %d 轮上限", maxTurns)
}

// dispatch validates, approves, persists and executes one tool call. The
// result text is what the model sees; the returned state is the persisted
// outcome. The flow is fixed: gate → (approval wait) → final guard → durable
// executing state → execute. A final denial can never be overwritten by a
// later module.
func (e Engine) dispatch(ctx context.Context, rec Recorder, allowed map[string]bool, call model.ToolCall, maxToolOutput int) (string, string, error) {
	name, arguments := call.Function.Name, call.Function.Arguments
	if !allowed[name] {
		denial := fmt.Errorf("未提供的 MCP 工具 %q", name)
		// Denied calls never dispatch, so they are recorded as failed
		// directly from pending.
		if err := rec.FinishTool(ctx, call.ID, "failed", "", denial.Error()); err != nil {
			return "", "", fmt.Errorf("记录工具拒绝失败: %w", err)
		}
		return "工具错误：" + denial.Error(), CallStateFailed, nil
	}
	if e.Gate != nil {
		req := ToolRequest{CallID: call.ID, Tool: name, Arguments: arguments}
		decision := e.Gate.Gate(ctx, req)
		if decision.Deny != "" {
			if err := rec.FinishTool(ctx, call.ID, "failed", "", decision.Deny); err != nil {
				return "", "", fmt.Errorf("记录工具拒绝失败: %w", err)
			}
			return "工具错误：" + decision.Deny, CallStateFailed, nil
		}
		if decision.Confirm != "" {
			denied, err := e.waitForApproval(ctx, rec, req)
			if err != nil {
				return "", "", err
			}
			if denied != "" {
				return denied, CallStateFailed, nil
			}

		} else {
			// Auto-approved by policy: persist the approval so the audit
			// trail shows who allowed the call.
			if err := rec.ApproveAuto(ctx, call.ID); err != nil {
				return "", "", fmt.Errorf("记录自动批准失败: %w", err)
			}
		}
	}
	if e.Gate != nil {
		req := ToolRequest{CallID: call.ID, Tool: name, Arguments: arguments}
		if d := e.Gate.Recheck(ctx, req); d.Deny != "" {
			if err := rec.FinishTool(ctx, call.ID, "failed", "", d.Deny); err != nil {
				return "", "", err
			}
			return "工具错误：" + d.Deny, CallStateFailed, nil
		}
	}
	// The executing state must be durable before the tool runs; a failure
	// here means persistence is broken and the tool must not execute.
	if err := rec.BeginTool(ctx, call.ID); err != nil {
		return "", "", fmt.Errorf("确认工具执行状态失败: %w", err)
	}
	result, callErr := e.Tools.Call(ctx, name, arguments)
	switch {
	case callErr == nil:
		if len(result) > maxToolOutput {
			result = result[:maxToolOutput] + "\n[输出已截断]"
		}
		finishCtx := context.WithoutCancel(ctx)
		if err := rec.FinishTool(finishCtx, call.ID, "succeeded", result, ""); err != nil {
			return "", "", fmt.Errorf("保存工具结果失败: %w", err)
		}
		return result, CallStateSucceeded, nil
	case errors.Is(callErr, context.Canceled), errors.Is(callErr, context.DeadlineExceeded), isUnknownOutcome(callErr):
		// Cancellation or timeout cannot prove the external operation did
		// not take effect, so the outcome stays unknown.
		finishCtx := context.WithoutCancel(ctx)
		if err := rec.FinishTool(finishCtx, call.ID, "execution_unknown", "", "工具执行后未收到确定结果："+callErr.Error()); err != nil {
			return "", "", fmt.Errorf("记录未知结果失败: %w", err)
		}
		return "", "", callErr
	default:
		finishCtx := context.WithoutCancel(ctx)
		if err := rec.FinishTool(finishCtx, call.ID, "failed", "", callErr.Error()); err != nil {
			return "", "", fmt.Errorf("保存工具结果失败: %w", err)
		}
		return "工具错误：" + callErr.Error(), CallStateFailed, nil
	}
}

// waitForApproval blocks until the user decision for the call arrives. It
// returns the model-visible denial text when the call was not approved (the
// denial outcome is persisted by the approval service, so the engine only
// surfaces it to the model) and an empty text when it was.
func (e Engine) waitForApproval(ctx context.Context, rec Recorder, req ToolRequest) (string, error) {
	if e.Approver == nil {
		denial := "需要人工审批，但当前没有审批通道"
		if err := rec.FinishTool(ctx, req.CallID, "failed", "", denial); err != nil {
			return "", fmt.Errorf("记录工具拒绝失败: %w", err)
		}
		return "工具错误：" + denial, nil
	}
	rowID, err := rec.ResolveCall(ctx, req.CallID)
	if err != nil {
		return "", fmt.Errorf("定位工具调用记录失败: %w", err)
	}
	timeout := e.ApprovalTimeout
	if timeout <= 0 {
		timeout = defaultApprovalWait
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	approved, note, err := e.Approver.WaitApproval(waitCtx, rowID)
	if err != nil {
		return "", fmt.Errorf("等待审批失败: %w", err)
	}
	if !approved {
		if strings.TrimSpace(note) == "" {
			note = "调用未获批准"
		}
		return "工具错误：" + note, nil
	}
	return "", nil
}

// Tool call states mirrored from storage for recorder callers.
const (
	CallStateSucceeded = "succeeded"
	CallStateFailed    = "failed"
)

type noopRecorder struct{}

func (noopRecorder) ModelRequest(context.Context, config.Model, []model.Message, []model.Tool) (string, error) {
	return "", nil
}

func (noopRecorder) ModelDone(context.Context, string, error) error { return nil }

func (noopRecorder) Assistant(context.Context, model.Message) error { return nil }

func (noopRecorder) ResolveCall(_ context.Context, callID string) (string, error) { return callID, nil }

func (noopRecorder) ApproveAuto(context.Context, string) error { return nil }

func (noopRecorder) BeginTool(context.Context, string) error { return nil }

func (noopRecorder) FinishTool(context.Context, string, string, string, string) error { return nil }

func (noopRecorder) SaveSummary(context.Context, int, string) error { return nil }

// An executor marks errors occurring after external dispatch with this contract.
func isUnknownOutcome(err error) bool {
	var outcome interface{ OutcomeUnknown() bool }
	return errors.As(err, &outcome) && outcome.OutcomeUnknown()
}

// ExecuteTool dispatches an already persisted host-scheduled request through
// exactly the same approval, recheck and durable outcome path as model calls.
func (e Engine) ExecuteTool(ctx context.Context, call model.ToolCall) (string, string, error) {
	rec := e.Recorder
	if rec == nil {
		rec = noopRecorder{}
	}
	defs, err := e.Tools.Definitions(ctx)
	if err != nil {
		return "", "", err
	}
	allowed := map[string]bool{}
	for _, d := range defs {
		ok := e.AllowedTools == nil
		for _, n := range e.AllowedTools {
			if n == d.Function.Name {
				ok = true
			}
		}
		if ok {
			allowed[d.Function.Name] = true
		}
	}
	_, output, _ := e.budget()
	return e.dispatch(ctx, rec, allowed, call, output)
}
