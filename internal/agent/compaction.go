package agent

import (
	"context"
	"fmt"

	"icetudio/internal/config"
	"icetudio/internal/model"
)

// Compactor summarizes older complete turns when the prompt history no
// longer fits the model window. Implementations must send the summary
// request through the same guarded model surface as ordinary sends — the
// folded range carries tool results with their data scopes — and persist
// the result through Recorder.SaveSummary so restarts and later runs reuse
// it. Original messages are never modified or deleted.
type Compactor interface {
	Summarize(ctx context.Context, source config.Model, older []model.Message) (string, error)
}

// EstimateTokens approximates the token count of a string: CJK and other
// non-ASCII runes count about one token each, ASCII about four bytes per
// token. The estimate runs on every model request, so it trades precision
// for predictability; it errs on the high side, which only means compacting
// slightly earlier.
func EstimateTokens(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}

func estimateMessages(messages []model.Message) int {
	total := 0
	for _, m := range messages {
		for _, item := range m.ProviderState {
			total += EstimateTokens(string(item))
		}
		for _, f := range m.Files {
			total += EstimateTokens(f.Text) + EstimateTokens(f.Name)
			if f.Data != "" {
				total += 4096
			}
		}
		total += EstimateTokens(m.Content) + EstimateTokens(m.ToolCallID) + 8
		for _, c := range m.ToolCalls {
			total += EstimateTokens(c.Function.Name) + EstimateTokens(c.Function.Arguments) + 8
		}
	}
	return total
}

func estimateTools(tools []model.Tool) int {
	total := 0
	for _, t := range tools {
		total += EstimateTokens(t.Function.Name) + EstimateTokens(t.Function.Description) + EstimateTokens(string(t.Function.Parameters)) + 16
	}
	return total
}

// outputReserve keeps room in the window for the model's reply; a history
// that exactly filled the window would truncate every answer.
func outputReserve(budget int) int {
	reserve := budget / 8
	if reserve < 512 {
		reserve = 512
	}
	if reserve > 4096 {
		reserve = 4096
	}
	if reserve >= budget {
		reserve = budget / 2
	}
	return reserve
}

// summaryHeading marks the compaction block inside the system prompt.
const summaryHeading = "[此前会话内容摘要（原始记录保留在本地历史）]"

func summaryBlock(summary string) string {
	return summaryHeading + "\n" + summary
}

// summaryTokenAllowance is the window room set aside for the summary the
// compactor is about to produce, so the post-compaction estimate does not
// depend on the summary's actual length.
const summaryTokenAllowance = 1024

// compactTurn folds older complete turns into the session summary when the
// pending request no longer fits the window budget. The boundary never
// splits an assistant tool-call batch from its results, and the current
// task — the newest user message onward — is always kept outside the
// summary. On success it returns the new summary text and the reduced
// message list; the caller persists it through the Recorder.
func (e Engine) compactTurn(ctx context.Context, rec Recorder, source config.Model, messages *[]model.Message, summary *string, definitions []model.Tool) error {
	inputBudget := e.ContextBudget - outputReserve(e.ContextBudget)
	used := estimateMessages(*messages) + estimateTools(definitions)
	if used <= inputBudget {
		return nil
	}
	if len(*messages) < 2 || (*messages)[0].Role != "system" {
		return nil
	}
	entries := (*messages)[1:]
	// The current task starts at the newest user entry and is never folded;
	// a run always begins with a user message, but stay defensive.
	lastUser := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "user" {
			lastUser = i
			break
		}
	}
	if lastUser < 1 {
		return errOverBudget(e.ContextBudget, entries)
	}
	// b is the message index of the last folded entry; the tail starts at
	// b+1 and must still contain the whole current task. A boundary is only
	// closed when the folded side ends at a natural turn edge: never after
	// an assistant message that requested calls, and never between the
	// results of one call batch.
	closed := func(i int) bool {
		entry := (*messages)[i]
		switch entry.Role {
		case "assistant":
			return len(entry.ToolCalls) == 0
		case "tool":
			return i+1 >= len(*messages) || (*messages)[i+1].Role != "tool"
		default:
			return true
		}
	}
	overhead := estimateTools(definitions) + EstimateTokens((*messages)[0].Content)
	target := inputBudget * 7 / 10
	fits := func(b int) bool {
		return estimateMessages((*messages)[b+1:])+overhead+summaryTokenAllowance <= target
	}
	chosen := 0
	for b := 1; b <= lastUser; b++ {
		if !closed(b) {
			continue
		}
		if fits(b) {
			chosen = b
			break
		}
	}
	if chosen == 0 {
		// Degrade gracefully: fold the largest valid prefix even if the
		// comfortable target is missed, as long as the result fits the hard
		// input budget.
		for b := lastUser; b >= 1; b-- {
			if !closed(b) {
				continue
			}
			if estimateMessages((*messages)[b+1:])+overhead+summaryTokenAllowance <= inputBudget {
				chosen = b
				break
			}
		}
	}
	if chosen == 0 {
		return errOverBudget(e.ContextBudget, entries)
	}
	newSummary, err := e.Compactor.Summarize(ctx, source, (*messages)[1:chosen+1])
	if err != nil {
		return err
	}
	// Coverage is counted on the run's window; the recorder adds the entries
	// the run started without, so the stored boundary is absolute.
	if err := rec.SaveSummary(ctx, chosen, newSummary); err != nil {
		return err
	}
	*summary = newSummary
	tail := append([]model.Message(nil), (*messages)[chosen+1:]...)
	rebuilt := make([]model.Message, 0, len(tail)+1)
	rebuilt = append(rebuilt, model.Message{Role: "system"})
	rebuilt = append(rebuilt, tail...)
	*messages = rebuilt
	return nil
}

func errOverBudget(budget int, entries []model.Message) error {
	return &ContextBudgetError{Budget: budget, Entries: len(entries)}
}

// ContextBudgetError reports a history that cannot be sent within the model
// window and cannot be compacted any further, e.g. when the current task
// alone already exceeds the budget.
type ContextBudgetError struct {
	Budget  int
	Entries int
}

func (e *ContextBudgetError) Error() string {
	return fmt.Sprintf("上下文超过模型窗口预算（%d token），且当前任务无法再压缩", e.Budget)
}
