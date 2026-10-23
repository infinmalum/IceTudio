package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
)

// fakeCompactor records what the engine asked it to fold.
type fakeCompactor struct {
	summarize func([]model.Message) (string, error)
	folded    [][]model.Message
}

func (c *fakeCompactor) Summarize(_ context.Context, _ config.Model, older []model.Message) (string, error) {
	c.folded = append(c.folded, append([]model.Message(nil), older...))
	if c.summarize != nil {
		return c.summarize(older)
	}
	return "旧事摘要", nil
}

func scriptChat(t *testing.T, check func(turn int, messages []model.Message) bool, final string) *fakeChat {
	return &fakeChat{chat: func(turn int, messages []model.Message) (model.ChatResponse, error) {
		if !check(turn, messages) {
			t.Fatalf("unexpected model request at turn %d: %+v", turn, messages)
		}
		if turn == 1 {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: final}}, nil
		}
		t.Fatal("model called twice")
		return model.ChatResponse{}, nil
	}}
}

// Compaction must fold only complete turns, keep the current task outside
// the summary and mirror the summary into the system prompt of the actual
// model request.
func TestCompactionFoldsOldTurnsKeepsCurrentTask(t *testing.T) {
	history := []model.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("旧", 2000)},
		{Role: "assistant", Content: strings.Repeat("答", 2000)},
		{Role: "user", Content: "新任务"},
	}
	compactor := &fakeCompactor{}
	var seen []model.Message
	fake := scriptChat(t, func(_ int, messages []model.Message) bool {
		seen = messages
		return true
	}, "完成")
	rec := &memRecorder{}
	_, err := (Engine{Model: fake, Tools: &fakeTools{}, Recorder: rec, Compactor: compactor, ContextBudget: 4096}).Run(context.Background(), config.DefaultModel("lmstudio"), history)
	if err != nil {
		t.Fatal(err)
	}
	if compactor.calls() != 1 || len(compactor.folded) != 1 {
		t.Fatalf("compaction calls: %d", compactor.calls())
	}
	folded := compactor.folded[0]
	if len(folded) != 2 || folded[0].Role != "user" || folded[1].Role != "assistant" {
		t.Fatalf("folded range: %+v", folded)
	}
	if len(rec.summaries) != 1 || !strings.HasPrefix(rec.summaries[0], "2 旧事摘要") {
		t.Fatalf("summary not persisted with absolute coverage: %+v", rec.summaries)
	}
	// The model request carries the summary block in the system prompt and
	// only the current task afterwards.
	if len(seen) != 2 || seen[0].Role != "system" || !strings.Contains(seen[0].Content, summaryHeading) || !strings.Contains(seen[0].Content, "旧事摘要") || strings.Contains(seen[0].Content, "sys\n\nsys") {
		t.Fatalf("system prompt: %q", seen[0].Content)
	}
	if seen[0].Content != "sys\n\n"+summaryBlock("旧事摘要") {
		t.Fatalf("base system prompt lost: %q", seen[0].Content)
	}
	if len(seen) != 2 || seen[1].Role != "user" || seen[1].Content != "新任务" {
		t.Fatalf("current task not kept: %+v", seen)
	}
}

// A call batch may never be split: the boundary lands after the last result
// of the batch, so the model never sees an orphaned tool result.
func TestCompactionNeverSplitsToolBatch(t *testing.T) {
	call := func(id string) model.ToolCall {
		c := timeCall(id)
		return c
	}
	history := []model.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("问", 3000)},
		{Role: "assistant", ToolCalls: []model.ToolCall{call("a"), call("b")}},
		{Role: "tool", ToolCallID: "a", Content: strings.Repeat("甲", 1500)},
		{Role: "tool", ToolCallID: "b", Content: strings.Repeat("乙", 1500)},
		{Role: "user", Content: "新任务"},
	}
	compactor := &fakeCompactor{}
	rec := &memRecorder{}
	fake := scriptChat(t, func(_ int, _ []model.Message) bool { return true }, "完成")
	_, err := (Engine{Model: fake, Tools: &fakeTools{}, Recorder: rec, Compactor: compactor, ContextBudget: 6000}).Run(context.Background(), config.DefaultModel("lmstudio"), history)
	if err != nil {
		t.Fatal(err)
	}
	folded := compactor.folded[0]
	if len(folded) != 4 || folded[0].Role != "user" || folded[1].Role != "assistant" || folded[2].ToolCallID != "a" || folded[3].ToolCallID != "b" {
		t.Fatalf("folded range cut the batch: %+v", folded)
	}
	if len(rec.summaries) != 1 || !strings.HasPrefix(rec.summaries[0], "4 ") {
		t.Fatalf("coverage: %+v", rec.summaries)
	}
}

// A summarizer failure or a failed summary persist must abort the run
// instead of sending an over-budget or untracked request.
func TestCompactionFailureAbortsRun(t *testing.T) {
	history := []model.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("旧", 2000)},
		{Role: "assistant", Content: strings.Repeat("答", 2000)},
		{Role: "user", Content: "新任务"},
	}
	compactor := &fakeCompactor{summarize: func([]model.Message) (string, error) {
		return "", errors.New("模型不可用")
	}}
	_, err := (Engine{Model: scriptChat(t, func(int, []model.Message) bool { return true }, ""), Tools: &fakeTools{}, Recorder: &memRecorder{}, Compactor: compactor, ContextBudget: 4096}).Run(context.Background(), config.DefaultModel("lmstudio"), history)
	if err == nil || !strings.Contains(err.Error(), "模型不可用") {
		t.Fatalf("summarizer error not surfaced: %v", err)
	}

	compactor = &fakeCompactor{}
	rec := &memRecorder{summaryErr: errors.New("数据库只读")}
	_, err = (Engine{Model: scriptChat(t, func(int, []model.Message) bool { return true }, ""), Tools: &fakeTools{}, Recorder: rec, Compactor: compactor, ContextBudget: 4096}).Run(context.Background(), config.DefaultModel("lmstudio"), history)
	if err == nil || !strings.Contains(err.Error(), "数据库只读") {
		t.Fatalf("summary persistence error not surfaced: %v", err)
	}
}

// Without a declared window the loop never compacts, and a history whose
// current task alone exceeds the budget fails with a clear error instead of
// being silently truncated.
func TestCompactionWindowEdges(t *testing.T) {
	history := []model.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("旧", 2000)},
		{Role: "user", Content: strings.Repeat("新", 2000)},
	}
	compactor := &fakeCompactor{}
	// Unknown window: no compaction, the loop runs as before.
	if _, err := (Engine{Model: scriptChat(t, func(int, []model.Message) bool { return true }, "完成"), Tools: &fakeTools{}, Recorder: &memRecorder{}, Compactor: compactor}).Run(context.Background(), config.DefaultModel("lmstudio"), history); err != nil {
		t.Fatal(err)
	}
	if compactor.calls() != 0 {
		t.Fatal("compacted without a declared window")
	}
	// The current task itself over the budget: nothing can be folded.
	hugeTask := []model.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("巨", 6000)},
	}
	_, err := (Engine{Model: scriptChat(t, func(int, []model.Message) bool { return true }, ""), Tools: &fakeTools{}, Recorder: &memRecorder{}, Compactor: compactor, ContextBudget: 4096}).Run(context.Background(), config.DefaultModel("lmstudio"), hugeTask)
	var budgetErr *ContextBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected budget error, got %v", err)
	}
}

// A summary the run starts with rides in the system prompt from the first
// request on; it is injected by the host, and the engine keeps it current
// when the run compacts further.
func TestCompactionExtendsInitialSummary(t *testing.T) {
	history := []model.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "较短的历史"},
		{Role: "assistant", Content: "较短的回答"},
		{Role: "user", Content: "新任务"},
	}
	compactor := &fakeCompactor{}
	var seen []model.Message
	fake := scriptChat(t, func(_ int, messages []model.Message) bool {
		seen = messages
		return true
	}, "完成")
	if _, err := (Engine{Model: fake, Tools: &fakeTools{}, Recorder: &memRecorder{}, Compactor: compactor, ContextBudget: 100000, InitialSummary: "先前摘要"}).Run(context.Background(), config.DefaultModel("lmstudio"), history); err != nil {
		t.Fatal(err)
	}
	if compactor.calls() != 0 {
		t.Fatal("compacted although the window was not reached")
	}
	if seen[0].Content != "sys\n\n"+summaryBlock("先前摘要") {
		t.Fatalf("initial summary missing: %q", seen[0].Content)
	}
}

func (c *fakeCompactor) calls() int { return len(c.folded) }
