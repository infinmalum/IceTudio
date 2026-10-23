package storage

import (
	"context"
	"sync"
	"testing"
)

func taskGraphFixture(t *testing.T) (*Store, Conversation, Run, []TaskSpec) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "planned", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	p.Data.PlanEnabled = true
	p.Data.Delegation = DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
	p.Data.Execution.WorkerSources = []WorkerSource{{Name: "worker"}}
	p, err = s.UpdateRuntimeConfig(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "graph")
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, r.ID, RoleUser, "按顺序完成两个任务"); err != nil {
		t.Fatal(err)
	}
	specs := []TaskSpec{{ID: "first", Worker: "worker", Task: "先收集材料", Acceptance: "注明来源"}, {ID: "second", Worker: "worker", Task: "总结材料", DependsOn: []string{"first"}}}
	if _, err = s.SetTaskGraph(ctx, r.ID, 0, specs); err != nil {
		t.Fatal(err)
	}
	return s, c, r, specs
}
func createPlannedRun(t *testing.T, s *Store, c Conversation, root Run, spec TaskSpec) (Run, error) {
	t.Helper()
	session, err := s.CreateDelegationSession(context.Background(), c.ContextID, DefaultConfigID, "planned")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRunWithOptions(context.Background(), session.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: spec.Worker, TaskSpec: &spec})
	return run, err
}
func TestTaskClaimIsAtomicAndDependenciesAreActual(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	if _, err := createPlannedRun(t, s, c, root, specs[1]); err == nil {
		t.Fatal("successor claimed before predecessor")
	}
	sessions := make([]Session, 2)
	for i := range sessions {
		var err error
		sessions[i], err = s.CreateDelegationSession(ctx, c.ContextID, DefaultConfigID, "competing")
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	ch := make(chan Run, 2)
	for _, session := range sessions {
		wg.Add(1)
		go func(session Session) {
			defer wg.Done()
			r, _, err := s.CreateRunWithOptions(ctx, session.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker", TaskSpec: &specs[0]})
			if err == nil {
				ch <- r
			}
		}(session)
	}
	wg.Wait()
	close(ch)
	var winner Run
	count := 0
	for r := range ch {
		winner = r
		count++
	}
	if count != 1 {
		t.Fatalf("claims won: %d", count)
	}
	children, err := s.ListChildRuns(ctx, root.ID)
	if err != nil || len(children) != 1 {
		t.Fatalf("orphan runs: %+v %v", children, err)
	}
	g, err := s.GetTaskGraph(ctx, root.ID)
	if err != nil || len(g.Attempts) != 1 || g.Goal != "按顺序完成两个任务" {
		t.Fatalf("graph: %+v %v", g, err)
	}
	if _, err = s.SetTaskGraph(ctx, root.ID, g.Revision, specs[1:]); err == nil {
		t.Fatal("active task dropped")
	}
	if err = s.SetRunStatus(ctx, winner.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	second, err := createPlannedRun(t, s, c, root, specs[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = createPlannedRun(t, s, c, root, specs[0]); err == nil {
		t.Fatal("completed task replayed")
	}
	if err = s.SetRunStatus(ctx, second.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, root.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	g, err = s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.RunStatus != "completed" || len(g.Attempts) != 2 || g.Tasks[1].State != "succeeded" {
		t.Fatalf("complete: %+v %v", g, err)
	}
}
func TestTaskClaimRollbackAndContractBinding(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	changed := specs[0]
	changed.Materials = "changed after approval"
	if _, err := createPlannedRun(t, s, c, root, changed); err == nil {
		t.Fatal("changed approved contract accepted")
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_attempt BEFORE INSERT ON task_attempts BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := createPlannedRun(t, s, c, root, specs[0]); err == nil {
		t.Fatal("injected failure ignored")
	}
	g, err := s.GetTaskGraph(context.Background(), root.ID)
	if err != nil || g.Tasks[0].State != "pending" || len(g.Attempts) != 0 {
		t.Fatalf("partial claim: %+v %v", g, err)
	}
	children, err := s.ListChildRuns(context.Background(), root.ID)
	if err != nil || len(children) != 0 {
		t.Fatalf("partial run: %+v %v", children, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_attempt`); err != nil {
		t.Fatal(err)
	}
	if _, err = createPlannedRun(t, s, c, root, specs[0]); err != nil {
		t.Fatal(err)
	}
}
func TestTaskRecoveryDoesNotReplayUnknownEffects(t *testing.T) {
	s, c, root, specs := taskGraphFixture(t)
	ctx := context.Background()
	child, err := createPlannedRun(t, s, c, root, specs[0])
	if err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.AddAssistantMessage(ctx, child.SessionID, child.ID, "", []MessageToolCall{{ID: "effect", Name: "external__write", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.RunStatus != "interrupted" || g.Tasks[0].State != "execution_unknown" || g.Tasks[1].State != "cancelled" || g.Attempts[0].Status != "execution_unknown" {
		t.Fatalf("recovery: %+v %v", g, err)
	}
	if _, err = createPlannedRun(t, s, c, root, specs[0]); err == nil {
		t.Fatal("unknown attempt replayed")
	}
	revision := g.Revision
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.Revision != revision {
		t.Fatal("recovery not idempotent")
	}
}
func TestRootCannotCompleteWithUnexecutedPlan(t *testing.T) {
	s, _, root, _ := taskGraphFixture(t)
	if err := s.SetRunStatus(context.Background(), root.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRun(context.Background(), root.ID)
	if err != nil || r.Status != "failed" {
		t.Fatalf("premature completion: %+v %v", r, err)
	}
}
