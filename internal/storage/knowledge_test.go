package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// buildToolHistory crafts a session with two closed turns, the first ending
// in a two-call tool batch, so summary coverage boundaries can be exercised.
func buildToolHistory(t *testing.T, s *Store, contextID, credentialScope string) (string, []HistoryEntry) {
	t.Helper()
	ctx := context.Background()
	session, err := s.CreateContextSession(ctx, contextID, DefaultConfigID, "history")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRun(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// seq 1: the first task.
	if _, err := s.AddMessage(ctx, session.ID, run.ID, RoleUser, strings.Repeat("问", 50)); err != nil {
		t.Fatal(err)
	}
	// seq 2: assistant requests two calls.
	if _, _, err := s.AddAssistantMessage(ctx, session.ID, run.ID, "", []MessageToolCall{
		{ID: "a", Name: "local__read_file", Arguments: "{}"},
		{ID: "b", Name: "local__read_file", Arguments: "{}"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	// seq 2 results: one credential-scoped, one plain success.
	callA, err := s.FindRunToolCall(ctx, run.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	callB, err := s.FindRunToolCall(ctx, run.ID, "b")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginToolCall(ctx, callA.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginToolCall(ctx, callB.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishToolCall(ctx, callA.ID, CallSucceeded, strings.Repeat("甲", 100), "", credentialScope); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishToolCall(ctx, callB.ID, CallSucceeded, strings.Repeat("乙", 100), "", ScopePublic); err != nil {
		t.Fatal(err)
	}
	// seq 3: the assistant answers, closing the first task.
	if _, err := s.AddMessage(ctx, session.ID, run.ID, RoleAssistant, "第一问完成"); err != nil {
		t.Fatal(err)
	}
	// seq 4: the current task.
	if _, err := s.AddMessage(ctx, session.ID, run.ID, RoleUser, "新任务"); err != nil {
		t.Fatal(err)
	}
	entries, err := s.HistoryEntries(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session.ID, entries
}

func TestHistoryEntriesRenderPersistedOrder(t *testing.T) {
	s := openTestStore(t)
	sessionID, entries := buildToolHistory(t, s, testContextID, ScopePublic)
	if len(entries) != 6 {
		t.Fatalf("entries: %+v", entries)
	}
	want := []struct {
		role   string
		seq    int
		calls  int
		callID string
	}{
		{RoleUser, 1, 0, ""},
		{RoleAssistant, 2, 2, ""},
		{RoleTool, 2, 2, "a"},
		{RoleTool, 2, 2, "b"},
		{RoleAssistant, 3, 0, ""},
		{RoleUser, 4, 0, ""},
	}
	for i, w := range want {
		if entries[i].Role != w.role || entries[i].Seq != w.seq || entries[i].CallTotal != w.calls || entries[i].CallID != w.callID {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], w)
		}
	}
	_ = sessionID
}

func TestSummaryCoverageBoundariesAndScope(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sessionID, entries := buildToolHistory(t, s, testContextID, ScopeCredential)

	// A boundary inside the tool batch is refused, never persisted.
	if _, err := s.SaveSessionSummaryCovering(ctx, sessionID, 2, "x"); err == nil {
		t.Fatal("boundary on assistant-with-calls accepted")
	}
	if _, err := s.SaveSessionSummaryCovering(ctx, sessionID, 3, "x"); err == nil {
		t.Fatal("boundary between batch results accepted")
	}
	// The closed boundary after the batch covers the credential result, so
	// the summary inherits the most restrictive scope.
	sum, err := s.SaveSessionSummaryCovering(ctx, sessionID, 4, "第一问的摘要")
	if err != nil {
		t.Fatal(err)
	}
	if sum.CoversSeq != 2 || sum.CoversCalls != 2 || sum.Scope != ScopeCredential {
		t.Fatalf("summary: %+v", sum)
	}
	covered, ok := SummaryCoverage(entries, sum)
	if !ok || covered != 4 {
		t.Fatalf("coverage: %d %v", covered, ok)
	}
	// Replacing the summary widens the coverage; originals stay untouched.
	sum, err = s.SaveSessionSummaryCovering(ctx, sessionID, 5, "更完整摘要")
	if err != nil {
		t.Fatal(err)
	}
	if sum.CoversSeq != 3 || sum.Scope != ScopeCredential {
		t.Fatalf("replaced summary: %+v", sum)
	}
	list, err := s.ListMessages(ctx, sessionID)
	if err != nil || len(list) != 4 || list[0].Content != strings.Repeat("问", 50) {
		t.Fatalf("originals modified: %+v %v", list, err)
	}
	// Coverage beyond the history is refused; a summary must leave a tail.
	if _, err := s.SaveSessionSummaryCovering(ctx, sessionID, 6, "全部"); err == nil {
		t.Fatal("full-coverage summary accepted")
	}
}

func TestSessionPlanRoundtrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	session, err := s.CreateContextSession(ctx, testContextID, DefaultConfigID, "plan")
	if err != nil {
		t.Fatal(err)
	}
	steps := []PlanStep{{Title: "查资料", Status: PlanDone}, {Title: "写结论", Status: PlanInProgress, Note: "等待结果"}}
	plan, err := s.CompareAndSetSessionPlan(ctx, session.ID, steps, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContextID != testContextID {
		t.Fatalf("plan ownership: %+v", plan)
	}
	if err := s.ClearSessionPlan(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompareAndSetSessionPlan(ctx, session.ID, []PlanStep{{Title: "重来", Status: PlanPending}}, 2); err != nil {
		t.Fatal(err)
	}
	saved, found, err := s.GetSessionPlan(ctx, session.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	var parsed []PlanStep
	if err := json.Unmarshal([]byte(saved.Plan), &parsed); err != nil || len(parsed) != 1 || parsed[0].Title != "重来" {
		t.Fatalf("plan: %q %v %v", saved.Plan, parsed, err)
	}
	if err := s.ClearSessionPlan(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.GetSessionPlan(ctx, session.ID); found {
		t.Fatal("plan not cleared")
	}
	// Clearing again stays a no-op.
	if err := s.ClearSessionPlan(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanSteps(nil); err == nil {
		t.Fatal("empty plan accepted")
	}
	if err := ValidatePlanSteps([]PlanStep{{Title: "x", Status: "later"}}); err == nil {
		t.Fatal("unknown status accepted")
	}
}
