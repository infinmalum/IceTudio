package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
)

type fakeChat struct {
	calls int
	chat  func(int, []model.Message) (model.ChatResponse, error)
	tools func([]model.Tool)
}

func (f *fakeChat) Chat(_ context.Context, _ config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	f.calls++
	if f.tools != nil {
		f.tools(tools)
	}
	return f.chat(f.calls, messages)
}

type fakeTools struct {
	defs   []model.Tool
	call   func(name, args string) (string, error)
	called []string
}

func (f *fakeTools) Definitions(context.Context) ([]model.Tool, error) { return f.defs, nil }

func (f *fakeTools) Call(_ context.Context, name, args string) (string, error) {
	f.called = append(f.called, name+" "+args)
	return f.call(name, args)
}

// memRecorder captures what the engine persisted and in which order, so the
// failure-window tests can assert the durable state directly.
type memRecorder struct {
	requests   int
	completed  int
	assistant  []model.Message
	resolves   []string
	approvals  []string
	begins     []string
	finishes   []string // "callID state result errText"
	summaries  []string // "covered summary"
	modelErr   error
	beginErr   error
	finishErr  error
	resolveErr error
	approveErr error
	summaryErr error
}

func (r *memRecorder) ModelRequest(context.Context, config.Model, []model.Message, []model.Tool) (string, error) {
	r.requests++
	return "step", r.modelErr
}

func (r *memRecorder) ModelDone(context.Context, string, error) error { r.completed++; return nil }

func (r *memRecorder) Assistant(_ context.Context, msg model.Message) error {
	r.assistant = append(r.assistant, msg)
	return nil
}

func (r *memRecorder) ResolveCall(_ context.Context, callID string) (string, error) {
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	r.resolves = append(r.resolves, callID)
	return "row-" + callID, nil
}

func (r *memRecorder) ApproveAuto(_ context.Context, callID string) error {
	if r.approveErr != nil {
		return r.approveErr
	}
	r.approvals = append(r.approvals, callID)
	return nil
}

func (r *memRecorder) BeginTool(_ context.Context, callID string) error {
	if r.beginErr != nil {
		return r.beginErr
	}
	r.begins = append(r.begins, callID)
	return nil
}

func (r *memRecorder) FinishTool(_ context.Context, callID, state, result, errText string) error {
	if r.finishErr != nil {
		return r.finishErr
	}
	r.finishes = append(r.finishes, callID+" "+state+" "+result+" "+errText)
	return nil
}

func (r *memRecorder) SaveSummary(_ context.Context, covered int, summary string) error {
	if r.summaryErr != nil {
		return r.summaryErr
	}
	r.summaries = append(r.summaries, fmt.Sprintf("%d %s", covered, summary))
	return nil
}

// fakeGate produces a scripted verdict per call.
type fakeGate struct {
	decision Decision
	recheck  Decision
	rechecks int
}

func (g *fakeGate) Gate(context.Context, ToolRequest) Decision { return g.decision }

func (g *fakeGate) Recheck(context.Context, ToolRequest) Decision {
	g.rechecks++
	return g.recheck
}

// fakeApprover scripts the user's approval decision.
type fakeApprover struct {
	approved bool
	note     string
	err      error
	waits    int
}

func (a *fakeApprover) WaitApproval(context.Context, string) (bool, string, error) {
	a.waits++
	if a.err != nil {
		return false, "", a.err
	}
	return a.approved, a.note, nil
}

func timeDef() model.Tool {
	definition := model.Tool{Type: "function"}
	definition.Function.Name = "local__current_time"
	return definition
}

func timeCall(id string) model.ToolCall {
	call := model.ToolCall{ID: id, Type: "function"}
	call.Function.Name = "local__current_time"
	call.Function.Arguments = "{}"
	return call
}

func TestEnginePairsMultipleToolResults(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "12:00", nil
	}}
	first := model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one"), timeCall("two")}}}
	fake := &fakeChat{chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return first, nil
		}
		if len(messages) != 4 || messages[2].ToolCallID != "one" || messages[3].ToolCallID != "two" {
			t.Fatalf("tool result pairing: %+v", messages)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "完成"}}, nil
	}}
	rec := &memRecorder{}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec, AllowedTools: []string{"local__current_time"}}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "几点了"}})
	if err != nil || answer != "完成" || fake.calls != 2 {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if len(rec.finishes) != 2 || !strings.Contains(rec.finishes[0], "one succeeded 12:00") || !strings.Contains(rec.finishes[1], "two succeeded 12:00") {
		t.Fatalf("recorded finishes: %+v", rec.finishes)
	}
	if len(rec.begins) != 2 || len(rec.assistant) != 2 {
		t.Fatalf("begins=%v assistant=%d", rec.begins, len(rec.assistant))
	}
}

func TestEngineHidesAndRechecksTools(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "12:00", nil
	}}
	fake := &fakeChat{tools: func(tools []model.Tool) {
		if len(tools) != 0 {
			t.Fatalf("tool exposed: %+v", tools)
		}
	}, chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		if !strings.Contains(messages[len(messages)-1].Content, "未提供") {
			t.Fatalf("missing denial: %+v", messages)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "denied"}}, nil
	}}
	rec := &memRecorder{}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec, AllowedTools: []string{}}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "denied" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if len(rec.finishes) != 1 || !strings.Contains(rec.finishes[0], "one failed") {
		t.Fatalf("denial not recorded as failed: %+v", rec.finishes)
	}
	if len(tools.called) != 0 {
		t.Fatalf("denied call dispatched: %+v", tools.called)
	}

	fake = &fakeChat{tools: func(tools []model.Tool) {
		if len(tools) != 1 {
			t.Fatalf("expected one visible tool: %+v", tools)
		}
	}, chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		if !strings.Contains(messages[len(messages)-1].Content, "未被当前") {
			t.Fatalf("missing late denial: %+v", messages)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "denied"}}, nil
	}}
	answer, err = (Engine{Model: fake, Tools: tools, AllowedTools: []string{"local__current_time"}, Gate: &fakeGate{decision: Decision{Deny: "未被当前 Agent 允许的工具调用 \"local__current_time\""}}}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "denied" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}

func TestEngineReturnsToolErrorToModel(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "", errors.New("参数有误")
	}}
	fake := &fakeChat{chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		if !strings.Contains(messages[len(messages)-1].Content, "工具错误") {
			t.Fatalf("tool error not returned: %+v", messages)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "参数有误"}}, nil
	}}
	rec := &memRecorder{}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "参数有误" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if len(rec.finishes) != 1 || !strings.Contains(rec.finishes[0], "one failed") || !strings.Contains(rec.finishes[0], "参数有误") {
		t.Fatalf("error outcome not persisted: %+v", rec.finishes)
	}
}

// A tool must never execute when its executing state cannot be persisted
// first: a broken store suspends dispatch instead of running blind.
func TestEngineDispatchBlockedWithoutPersistence(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		t.Fatal("tool dispatched despite failed persistence")
		return "", nil
	}}
	fake := &fakeChat{chat: func(turn int, _ []model.Message) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
	}}
	rec := &memRecorder{beginErr: errors.New("数据库只读")}
	_, err := (Engine{Model: fake, Tools: tools, Recorder: rec}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err == nil || !strings.Contains(err.Error(), "确认工具执行状态失败") {
		t.Fatalf("expected dispatch gate error, got %v", err)
	}
	if len(tools.called) != 0 {
		t.Fatalf("tool ran anyway: %+v", tools.called)
	}
}

// Cancelling mid-execution cannot prove the external operation did not
// happen, so the recorder must see execution_unknown and the run must stop.
func TestEngineCancelDuringToolIsUnknown(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "", context.Canceled
	}}
	fake := &fakeChat{chat: func(turn int, _ []model.Message) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
	}}
	rec := &memRecorder{}
	_, err := (Engine{Model: fake, Tools: tools, Recorder: rec}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if len(rec.finishes) != 1 || !strings.Contains(rec.finishes[0], "one execution_unknown") {
		t.Fatalf("unknown outcome not persisted: %+v", rec.finishes)
	}
}

// A result that arrives even though cancellation was requested mid-call is
// still a known result and must be persisted as succeeded rather than dropped.
func TestEngineKeepsResultArrivingAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		cancel()
		return "12:00", nil
	}}
	fake := &fakeChat{chat: func(turn int, _ []model.Message) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
	}}
	rec := &memRecorder{}
	_, err := (Engine{Model: fake, Tools: tools, Recorder: rec}).Run(ctx, config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if len(rec.finishes) != 1 || !strings.Contains(rec.finishes[0], "one succeeded 12:00") {
		t.Fatalf("successful result lost on cancel: %+v", rec.finishes)
	}
}

func TestEngineOutputBudget(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "12345", nil
	}}
	fake := &fakeChat{chat: func(turn int, _ []model.Message) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("call-" + string(rune('0'+turn)))}}}, nil
	}}
	_, err := (Engine{Model: fake, Tools: tools, MaxTotalOutput: 8}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err == nil || !strings.Contains(err.Error(), "预算") {
		t.Fatalf("expected output budget error, got %v", err)
	}
}

// A confirm decision suspends dispatch until the approver reports the user's
// decision; an approved call then dispatches through the normal durable
// path.
func TestEngineApprovalConfirmedDispatches(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "12:00", nil
	}}
	fake := &fakeChat{chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		if !strings.Contains(messages[len(messages)-1].Content, "12:00") {
			t.Fatalf("tool result missing after approval: %+v", messages)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "完成"}}, nil
	}}
	rec := &memRecorder{}
	gate := &fakeGate{decision: Decision{Confirm: "需要确认"}, recheck: Decision{}}
	approver := &fakeApprover{approved: true}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec, AllowedTools: []string{"local__current_time"}, Gate: gate, Approver: approver}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "完成" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if approver.waits != 1 || gate.rechecks != 1 || len(rec.resolves) != 1 {
		t.Fatalf("approval flow: waits=%d rechecks=%d resolves=%v", approver.waits, gate.rechecks, rec.resolves)
	}
	if len(rec.approvals) != 0 || len(rec.begins) != 1 || len(rec.finishes) != 1 || !strings.Contains(rec.finishes[0], "one succeeded") {
		t.Fatalf("approved dispatch records: %+v %+v %+v", rec.approvals, rec.begins, rec.finishes)
	}
}

// A policy-allowed call is auto-approved: the approval is persisted and no
// user wait happens.
func TestEngineAutoApprovalPersisted(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		return "12:00", nil
	}}
	fake := &fakeChat{chat: func(turn int, _ []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "完成"}}, nil
	}}
	rec := &memRecorder{}
	approver := &fakeApprover{approved: true}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec, AllowedTools: []string{"local__current_time"}, Gate: &fakeGate{}, Approver: approver}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "完成" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if approver.waits != 0 || len(rec.approvals) != 1 || rec.approvals[0] != "one" {
		t.Fatalf("auto approval not persisted: waits=%d approvals=%v", approver.waits, rec.approvals)
	}
}

// A user denial reaches the model as a tool error and the tool never runs;
// the denial itself is persisted by the approval service, not the engine.
func TestEngineApprovalDeniedReturnsToModel(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		t.Fatal("denied call dispatched")
		return "", nil
	}}
	fake := &fakeChat{chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		if !strings.Contains(messages[len(messages)-1].Content, "用户拒绝") {
			t.Fatalf("denial not returned to model: %+v", messages)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "了解"}}, nil
	}}
	rec := &memRecorder{}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec, AllowedTools: []string{"local__current_time"}, Gate: &fakeGate{decision: Decision{Confirm: "需要确认"}}, Approver: &fakeApprover{approved: false, note: "用户拒绝执行（不愿意）"}}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "了解" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if len(tools.called) != 0 || len(rec.begins) != 0 || len(rec.finishes) != 0 {
		t.Fatalf("denied call left engine-side records: %+v %+v %+v", tools.called, rec.begins, rec.finishes)
	}
}

// Policy may tighten between approval and dispatch; the final guard must
// stop the call and record the rejection.
func TestEngineFinalGuardBlocksAfterApproval(t *testing.T) {
	tools := &fakeTools{defs: []model.Tool{timeDef()}, call: func(name, _ string) (string, error) {
		t.Fatal("call dispatched despite recheck denial")
		return "", nil
	}}
	fake := &fakeChat{chat: func(turn int, _ []model.Message) (model.ChatResponse, error) {
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{timeCall("one")}}}, nil
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "了解"}}, nil
	}}
	rec := &memRecorder{}
	answer, err := (Engine{Model: fake, Tools: tools, Recorder: rec, AllowedTools: []string{"local__current_time"}, Gate: &fakeGate{decision: Decision{Confirm: "需要确认"}, recheck: Decision{Deny: "路径超出工作区范围"}}, Approver: &fakeApprover{approved: true}}).Run(context.Background(), config.DefaultModel("lmstudio"), []model.Message{{Role: "user", Content: "test"}})
	if err != nil || answer != "了解" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if len(tools.called) != 0 || len(rec.begins) != 0 || len(rec.finishes) != 1 || !strings.Contains(rec.finishes[0], "路径超出工作区范围") {
		t.Fatalf("final guard not enforced: %+v %+v", tools.called, rec.finishes)
	}
}
