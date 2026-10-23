package storage

import (
	"context"

	"sync"
	"testing"
)

func TestDependencyPlanRejectsInvalidGraphs(t *testing.T) {
	cases := map[string][]PlanStep{
		"missing":              {{ID: "a", Title: "a", Status: PlanPending, DependsOn: []string{"missing"}}},
		"cycle":                {{ID: "a", Title: "a", Status: PlanPending, DependsOn: []string{"b"}}, {ID: "b", Title: "b", Status: PlanPending, DependsOn: []string{"a"}}},
		"duplicate":            {{ID: "a", Title: "a", Status: PlanPending}, {ID: "a", Title: "b", Status: PlanPending}},
		"premature":            {{ID: "a", Title: "a", Status: PlanPending}, {ID: "b", Title: "b", Status: PlanInProgress, DependsOn: []string{"a"}}},
		"premature completion": {{ID: "a", Title: "a", Status: PlanPending}, {ID: "b", Title: "b", Status: PlanDone, DependsOn: []string{"a"}}},
	}
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePlanSteps(steps); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}

func TestPlanRevisionDependenciesAndClear(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	session, err := s.CreateContextSession(ctx, testContextID, DefaultConfigID, "dependency")
	if err != nil {
		t.Fatal(err)
	}
	steps := []PlanStep{{ID: "fetch", Title: "取材料", Status: PlanPending, Acceptance: "材料有出处", InputRefs: []string{"user:input"}}, {ID: "write", Title: "写报告", Status: PlanPending, DependsOn: []string{"fetch"}}}
	p, err := s.CompareAndSetSessionPlan(ctx, session.ID, steps, 0)
	if err != nil || p.Revision != 1 {
		t.Fatalf("create: %+v %v", p, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CompareAndSetSessionPlan(ctx, session.ID, steps, 1)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent writers: %d", successes)
	}
	steps[0].DependsOn = []string{}
	steps[0].Status = PlanDone
	steps[1].Status = PlanInProgress
	p, err = s.CompareAndSetSessionPlan(ctx, session.ID, steps, 2)
	if err != nil || p.Revision != 3 {
		t.Fatalf("advance: %+v %v", p, err)
	}
	if _, err = s.CompareAndSetSessionPlan(ctx, session.ID, steps[:1], 3); err == nil {
		t.Fatal("started task removed")
	}
	modified := append([]PlanStep(nil), steps...)
	modified[1].Status = PlanPending
	if _, err = s.CompareAndSetSessionPlan(ctx, session.ID, modified, 3); err == nil {
		t.Fatal("started task reset")
	}
	modified[1].Status = PlanInProgress
	modified[1].Acceptance = "different"
	if _, err = s.CompareAndSetSessionPlan(ctx, session.ID, modified, 3); err == nil {
		t.Fatal("started contract rewritten")
	}
	if err = s.ClearSessionPlan(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	p, found, err := s.GetSessionPlan(ctx, session.ID)
	if err != nil || found || p.Revision != 4 {
		t.Fatalf("clear: %+v %v %v", p, found, err)
	}
	if _, err = s.CompareAndSetSessionPlan(ctx, session.ID, steps, 3); err == nil {
		t.Fatal("stale update resurrected cleared plan")
	}
	if _, err = s.CompareAndSetSessionPlan(ctx, session.ID, steps, 4); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM session_plan_revisions WHERE session_id=?`, session.ID).Scan(&count); err != nil || count != 5 {
		t.Fatalf("history: %d %v", count, err)
	}
}
