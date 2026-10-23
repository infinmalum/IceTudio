package storage

import (
	"context"
	"encoding/json"
	"testing"
)

func fixedData() RuntimeConfigData {
	d := BlankRuntimeConfig()
	d.EnabledMCPServers = []string{"local"}
	d.Execution.Workflow = "fixed"
	d.Execution.WorkerSources = []WorkerSource{{Name: "worker"}}
	d.Delegation = DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
	d.Execution.Stages = []TaskSpec{{ID: "collect", Worker: "worker", Task: "collect", AccessMode: "read"}, {ID: "report", Worker: "worker", Task: "report", AccessMode: "read", DependsOn: []string{"collect"}}}
	return d
}

func TestFixedWorkflowValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RuntimeConfigData)
	}{
		{"empty", func(d *RuntimeConfigData) { d.Execution.Stages = nil }},
		{"duplicate", func(d *RuntimeConfigData) { d.Execution.Stages[1].ID = "collect" }},
		{"missing dependency", func(d *RuntimeConfigData) { d.Execution.Stages[1].DependsOn = []string{"missing"} }},
		{"cycle", func(d *RuntimeConfigData) { d.Execution.Stages[0].DependsOn = []string{"report"} }},
		{"unknown worker", func(d *RuntimeConfigData) { d.Execution.Stages[0].Worker = "missing" }},
		{"undeclared mode", func(d *RuntimeConfigData) { d.Execution.Stages[0].AccessMode = "" }},
		{"unknown mode", func(d *RuntimeConfigData) { d.Execution.Stages[0].AccessMode = "script" }},
		{"retry", func(d *RuntimeConfigData) { d.Execution.Budget.AutoRetry = true }},
		{"replacement", func(d *RuntimeConfigData) { d.Execution.Stages[0].Replaces = "report" }},
		{"delegation disabled", func(d *RuntimeConfigData) { d.Delegation.Enabled = false }},
		{"depth", func(d *RuntimeConfigData) { d.Delegation.MaxDepth = 0 }},
		{"tools", func(d *RuntimeConfigData) { d.AllowedTools = []string{"local__read_file"} }},
		{"main subset", func(d *RuntimeConfigData) { d.Execution.Main.RestrictTools = true }},
		{"attempt budget", func(d *RuntimeConfigData) { d.Execution.Budget.MaxAttempts = 1 }},
		{"model budget", func(d *RuntimeConfigData) { d.Execution.Budget.MaxModelCalls = 2 }},
		{"review budget", func(d *RuntimeConfigData) { d.Execution.Review.Mode = "required"; d.Execution.Budget.MaxModelCalls = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := fixedData()
			tc.mutate(&d)
			if err := validateRuntimeConfigData(d); err == nil {
				t.Fatal("invalid fixed configuration accepted")
			}
		})
	}
	if err := validateRuntimeConfigData(fixedData()); err != nil {
		t.Fatal(err)
	}
}

func TestFixedGraphAtomicCreationAndRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "fixed", fixedData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Initialization is in the same transaction as root creation.
	if _, err = s.db.Exec(`CREATE TRIGGER reject_fixed BEFORE INSERT ON task_items BEGIN SELECT RAISE(ABORT,'reject fixed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{}); err == nil {
		t.Fatal("initialization failure ignored")
	}
	runs, err := s.ListRuns(ctx, c.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("orphan root: %+v %v", runs, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_fixed`); err != nil {
		t.Fatal(err)
	}
	root, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.GetTaskGraph(ctx, root.ID)
	if err != nil || len(g.Tasks) != 2 || g.Revision != 1 {
		t.Fatalf("initial graph: %+v %v", g, err)
	}
	if _, err = s.SetTaskGraph(ctx, root.ID, g.Revision, p.Data.Execution.Stages); err == nil {
		t.Fatal("fixed stages editable")
	}
	childSession, err := s.CreateDelegationSession(ctx, c.ContextID, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateRunWithOptions(ctx, childSession.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker"}); err == nil {
		t.Fatal("unbound worker accepted")
	}
	child, err := createPlannedRun(t, s, c, root, p.Data.Execution.Stages[0])
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
	g, err = s.GetTaskGraph(ctx, root.ID)
	if err != nil || g.RunStatus != "interrupted" || g.Tasks[0].State != "execution_unknown" || g.Tasks[1].State != "cancelled" {
		t.Fatalf("recovery: %+v %v", g, err)
	}
	if _, err = createPlannedRun(t, s, c, root, p.Data.Execution.Stages[0]); err == nil {
		t.Fatal("unknown attempt replayed")
	}
	before, _ := json.Marshal(g)
	if err = s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = s.GetTaskGraph(ctx, root.ID)
	after, _ := json.Marshal(g)
	if err != nil || string(before) != string(after) {
		t.Fatal("recovery changed settled graph", err)
	}
}
