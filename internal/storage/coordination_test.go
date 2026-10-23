package storage

import (
	"context"
	"encoding/json"
	"testing"
)

func setTestLimits(t *testing.T, s *Store, root string, b RootBudget) {
	t.Helper()
	raw, _ := json.Marshal(b)
	if _, err := s.db.Exec(`UPDATE root_budgets SET limits=? WHERE root_run_id=?`, string(raw), root); err != nil {
		t.Fatal(err)
	}
}
func TestRootBudgetIsSharedAcrossComponents(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	setTestLimits(t, s, root.ID, RootBudget{MaxModelCalls: 1, MaxToolCalls: 1})
	child, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	step, err := s.BeginModelStep(ctx, root.ID, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordUsage(ctx, root.ID, 25); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteModelStep(ctx, step.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelStep(ctx, child.ID, "{}"); err == nil {
		t.Fatal("child reset root model budget")
	}
	_, calls, err := s.AddAssistantMessage(ctx, child.SessionID, child.ID, "", []MessageToolCall{{ID: "one", Name: "local__current_time", Arguments: "{}"}, {ID: "two", Name: "local__current_time", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[1].ID); err == nil {
		t.Fatal("child reset root tool budget")
	}
	g, err := s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.Budget.ModelCalls != 1 || g.Budget.ToolCalls != 1 || g.Budget.Tokens != 25 || g.Budget.UnknownUsage != 0 {
		t.Fatalf("root ledger: %+v %v", g.Budget, err)
	}
}
func TestUnknownUsageRemainsChargedAndRecoveryIsIdempotent(t *testing.T) {
	s, _, root, _ := taskGraphFixture(t)
	ctx := context.Background()
	setTestLimits(t, s, root.ID, RootBudget{MaxTokens: 9000})
	step, err := s.BeginModelStep(ctx, root.ID, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteModelStep(ctx, step.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelStep(ctx, root.ID, "{}"); err == nil {
		t.Fatal("unknown token usage released reservation")
	}
	g, err := s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.Budget.UnknownUsage != 1 || g.Budget.Tokens < 8192 {
		t.Fatalf("unknown accounting: %+v %v", g.Budget, err)
	}
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.Coordination.State != "blocked" || g.Coordination.EventSeq <= g.Coordination.PlannedSeq {
		t.Fatalf("recovery cursor: %+v %v", g, err)
	}
	seq := g.Coordination.EventSeq
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GetTaskGraph(ctx, root.ID)
	if g.Coordination.EventSeq != seq || g.Budget.UnknownUsage != 1 {
		t.Fatal("recovery replayed event or released budget")
	}
}
func TestActualUsageOverrunIsPersistedAndStopsNextRequest(t *testing.T) {
	s, _, root, _ := taskGraphFixture(t)
	ctx := context.Background()
	setTestLimits(t, s, root.ID, RootBudget{MaxTokens: 9000})
	if _, err := s.BeginModelStep(ctx, root.ID, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsage(ctx, root.ID, 10000); err == nil {
		t.Fatal("provider overrun ignored")
	}
	g, _ := s.GetTaskGraph(ctx, root.ID)
	if g.Budget.Tokens != 10000 {
		t.Fatal("overrun accounting rolled back")
	}
	if _, err := s.BeginModelStep(ctx, root.ID, "{}"); err == nil {
		t.Fatal("overrun allowed next send")
	}
}
func TestReadonlyParallelAndUnknownEffectsSerialize(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	specs[0].AccessMode = "read"
	specs[1].AccessMode = "read"
	specs[1].DependsOn = nil
	if _, err := s.SetTaskGraph(ctx, root.ID, 1, specs); err != nil {
		t.Fatal(err)
	}
	a, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := createPlannedRun(t, s, c, root, specs[1])
	if err != nil {
		t.Fatal("independent reads did not run together", err)
	}
	if err = s.SetRunStatus(ctx, a.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, b.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	more := append(specs, TaskSpec{ID: "write", Worker: "worker", Task: "写入", AccessMode: "write", Resources: []string{"shared-file"}}, TaskSpec{ID: "read-again", Worker: "worker", Task: "重新读取", AccessMode: "read", Resources: []string{"shared-file"}})
	g, _ := s.GetTaskGraph(ctx, root.ID)
	if _, err = s.SetTaskGraph(ctx, root.ID, g.Revision, more); err != nil {
		t.Fatal(err)
	}
	if _, err = createPlannedRun(t, s, c, root, more[2]); err != nil {
		t.Fatal(err)
	}
	if _, err = createPlannedRun(t, s, c, root, more[3]); err == nil {
		t.Fatal("read ran concurrently with write")
	}
}
func TestBoundedRetryPreservesFailedAttemptAndUnblocksDependency(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	setTestLimits(t, s, root.ID, RootBudget{AutoRetry: true, MaxRetries: 1})
	a, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, a.ID, "failed", "model transport failed"); err != nil {
		t.Fatal(err)
	}
	retry := specs[0]
	retry.ID = "retry"
	retry.Replaces = specs[0].ID
	more := append(specs, retry)
	g, _ := s.GetTaskGraph(ctx, root.ID)
	if _, err = s.SetTaskGraph(ctx, root.ID, g.Revision, more); err != nil {
		t.Fatal(err)
	}
	r, err := createPlannedRun(t, s, c, root, retry)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GetTaskGraph(ctx, root.ID)
	if !TaskSatisfied(g, specs[0].ID) || g.Tasks[0].State != "failed" || len(g.Attempts) != 2 {
		t.Fatal("retry erased failure or did not satisfy dependency")
	}
	if _, err = createPlannedRun(t, s, c, root, specs[1]); err != nil {
		t.Fatal("dependency did not use retry output", err)
	}
}
func TestWorkerTraceScopeAndRootBoundary(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	specs[0].AccessMode = "read"
	specs[1].AccessMode = "read"
	specs[1].DependsOn = nil
	if _, err := s.SetTaskGraph(ctx, root.ID, 1, specs); err != nil {
		t.Fatal(err)
	}
	a, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := createPlannedRun(t, s, c, root, specs[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordObservation(ctx, a.ID, "credential observation", ScopeCredential); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordObservation(ctx, b.ID, "my own observation", ScopePersonal); err != nil {
		t.Fatal(err)
	}
	traces, err := s.WorkerTraces(ctx, b.ID, "observation")
	if err != nil || len(traces) != 1 || traces[0].RunID != a.ID || traces[0].Scope != ScopeCredential || traces[0].Verification != "unreviewed" {
		t.Fatalf("trace scope: %+v %v", traces, err)
	}
	session, err := s.CreateContextSession(ctx, testContextID, DefaultConfigID, "other root")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.CreateRun(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	traces, err = s.WorkerTraces(ctx, other.ID, "observation")
	if err != nil || len(traces) != 0 {
		t.Fatalf("cross-root disclosure: %+v %v", traces, err)
	}
}

func TestLateResponseCannotResolveUnknownEffect(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	child, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.AddAssistantMessage(ctx, child.SessionID, child.ID, "", []MessageToolCall{{ID: "late", Name: "external__write", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishToolCall(ctx, calls[0].ID, CallSucceeded, "late reply", "", ScopeUnknown); err == nil {
		t.Fatal("late response silently resolved unknown effect")
	}
	call, err := s.GetToolCall(ctx, calls[0].ID)
	if err != nil || call.State != CallExecutionUnknown {
		t.Fatal("unknown record lost", call, err)
	}
}
func TestRetryCannotReplayKnownSideEffects(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	setTestLimits(t, s, root.ID, RootBudget{AutoRetry: true})
	child, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.AddAssistantMessage(ctx, child.SessionID, child.ID, "", []MessageToolCall{{ID: "write", Name: "external__write", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveToolCall(ctx, calls[0].ID, ApprovalUser); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishToolCall(ctx, calls[0].ID, CallSucceeded, "wrote", "", ScopeUnknown); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, child.ID, "failed", "subsequent model failed"); err != nil {
		t.Fatal(err)
	}
	retry := specs[0]
	retry.ID = "retry"
	retry.Replaces = specs[0].ID
	g, _ := s.GetTaskGraph(ctx, root.ID)
	if _, err = s.SetTaskGraph(ctx, root.ID, g.Revision, append(specs, retry)); err == nil {
		t.Fatal("automatic retry replayed successful side effect")
	}
}
