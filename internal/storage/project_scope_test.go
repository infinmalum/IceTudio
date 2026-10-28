package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"icetudio/internal/project"
)

func TestRootProjectScopeAllCreationPaths(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateProject(ctx, "根入口", 480)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := s.SubmitProjectOps(ctx, p.ID, "setup", "gui", 0, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{ID: "t1", Name: "轨1"}},
		{Kind: "track.add", Track: &project.TrackSpec{ID: "t2", Name: "轨2"}},
		{Kind: "clip.add", Clip: &project.ClipSpec{ID: "c1", TrackID: "t1", Length: 480}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.GetRuntimeConfig(ctx, DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	config.Data.Execution.ImproverEnabled = true
	config.Data.Execution.Review.Mode = "on_demand"
	if _, err := s.UpdateRuntimeConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, config.ID, "", "根入口")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionProject(ctx, c.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	root, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := ParseProjectScope(root.ProjectScope)
	if err != nil || scope.ProjectID != p.ID || scope.BaseRevision != setup.Revision || scope.AccessMode != "write" {
		t.Fatalf("CreateRun did not freeze binding: %+v %v", scope, err)
	}
	if err := s.SetRunStatus(ctx, root.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []CreateRunOptions{
		{},
		{Component: "improver"},
		{Inspection: &InspectionRequest{TargetRunID: root.ID}},
		{Inspection: &InspectionRequest{TargetRunID: root.ID, Retrospective: true}},
	} {
		run, _, err := s.CreateRunWithOptions(ctx, c.ID, opts)
		if err != nil {
			t.Fatal(err)
		}
		captured, err := ParseProjectScope(run.ProjectScope)
		wantMode := "write"
		if opts.Component == "improver" || opts.Inspection != nil {
			wantMode = "read"
		}
		if err != nil || captured.ProjectID != p.ID || captured.BaseRevision != setup.Revision || captured.AccessMode != wantMode {
			t.Fatalf("scope for %+v: %+v %v", opts, captured, err)
		}
		// No model/draft/review is executed here. Cancel the synthetic run
		// rather than satisfying unrelated completion gates with fake evidence.
		if err := s.SetRunStatus(ctx, run.ID, "cancelled", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Rejection is atomic: neither a run nor its coordination is published.
	for _, raw := range []string{
		`{bad`, `null`, `{}`, `{"projectId":"fake","baseRevision":0,"accessMode":"write"}`,
		fmt.Sprintf(`{"projectId":%q,"accessMode":"write"}`, p.ID),
		fmt.Sprintf(`{"projectId":%q,"baseRevision":null,"accessMode":"write"}`, p.ID),
		fmt.Sprintf(`{"projectId":%q,"baseRevision":999,"accessMode":"write"}`, p.ID),
		fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","clips":["c_missing"]}`, p.ID),
		fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","tracks":["t2"],"clips":["c1"]}`, p.ID),
		fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","tracks":["t1","t1"]}`, p.ID),
		fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","extra":true}`, p.ID),
	} {
		var before, after int
		if err := s.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{ProjectScope: raw}); err == nil {
			t.Fatalf("invalid scope accepted: %s", raw)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&after); err != nil || after != before {
			t.Fatalf("rejected creation left a run: %d -> %d %v", before, after, err)
		}
	}
	// Binding changed between service capture and transaction: reject, never
	// substitute the new project behind the user's explicit selection.
	other, err := s.CreateProject(ctx, "重绑", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionProject(ctx, c.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{ProjectScope: root.ProjectScope}); err == nil {
		t.Fatal("scope from a previous binding accepted")
	}
}

func TestDirectAndPlannedWorkerAdmissionSerialize(t *testing.T) {
	for _, incoming := range []string{"direct", "write", "unknown"} {
		t.Run(incoming, func(t *testing.T) {
			s, c, root, _ := taskGraphFixture(t)
			ctx := context.Background()
			read := TaskSpec{ID: "read", Worker: "worker", Task: "读取", AccessMode: "read"}
			specs := []TaskSpec{read, {ID: "write", Worker: "worker", Task: "写入", AccessMode: "write"}, {ID: "unknown", Worker: "worker", Task: "未知", AccessMode: "unknown"}, {ID: "read2", Worker: "worker", Task: "再次读取", AccessMode: "read"}}
			if _, err := s.SetTaskGraph(ctx, root.ID, 1, specs); err != nil {
				t.Fatal(err)
			}
			active, err := createPlannedRun(t, s, c, root, read)
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateDelegationSession(ctx, c.ContextID, c.ConfigID, "互斥")
			if err != nil {
				t.Fatal(err)
			}
			opts := CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker"}
			if incoming == "write" {
				opts.TaskSpec = &specs[1]
			} else if incoming == "unknown" {
				opts.TaskSpec = &specs[2]
			}
			if _, _, err := s.CreateRunWithOptions(ctx, session.ID, opts); err == nil {
				t.Fatal("writer/unknown overlapped active reader")
			}
			if err := s.SetRunStatus(ctx, active.ID, "completed", ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.CreateRunWithOptions(ctx, session.ID, opts); err != nil {
				t.Fatalf("writer could not start after reader ended: %v", err)
			}
			// The reverse ordering must reject a later reader as well.
			if _, err := createPlannedRun(t, s, c, root, specs[3]); err == nil {
				t.Fatal("reader overlapped writer/unknown")
			}
		})
	}
}

func TestConcurrentDirectWorkerAdmissionIsAtomic(t *testing.T) {
	s, c, root, _ := taskGraphFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		session, err := s.CreateDelegationSession(ctx, c.ContextID, c.ConfigID, "并发委派")
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := s.CreateRunWithOptions(ctx, session.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker"})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent unknown workers admitted: %d", succeeded)
	}
}

func TestReadParentCannotBecomeWriteChild(t *testing.T) {
	raw := `{"projectId":"p_a","baseRevision":1,"accessMode":"read","clips":["c1"]}`
	inherited, err := inheritProjectScope(raw, "worker", "write")
	if err != nil {
		t.Fatal(err)
	}
	var scope ProjectScope
	if err := json.Unmarshal([]byte(inherited), &scope); err != nil || scope.AccessMode != "read" || len(scope.Clips) != 1 || scope.Clips[0] != "c1" {
		t.Fatalf("write worker expanded read ceiling: %s %v", inherited, err)
	}
}

func TestFixedContinuationKeepsProjectScopeAfterRebind(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	d := fixedData()
	d.Execution.Stages[0].InputPrompt = "请提供材料"
	config, err := s.CreateRuntimeConfig(ctx, "工程续跑", d)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, config.ID, "", "工程续跑")
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.CreateProject(ctx, "原工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionProject(ctx, c.ID, original.ID); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMessage(ctx, c.ID, run.ID, RoleUser, "继续原工程任务"); err != nil {
		t.Fatal(err)
	}
	g, err := s.GetTaskGraph(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcknowledgeCoordination(ctx, run.ID, g.Coordination.EventSeq, "waiting", "请提供材料"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunStatus(ctx, run.ID, "failed", "等待输入"); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(ctx, "新绑定工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionProject(ctx, c.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ContinueFixedRun(ctx, run.ID, g.Tasks[0].Spec.ID, "补充材料", nil); err != nil {
		t.Fatal(err)
	}
	continued, err := s.GetRun(ctx, run.ID)
	if err != nil || continued.ProjectScope != run.ProjectScope {
		t.Fatalf("continuation changed project ceiling: %s -> %s %v", run.ProjectScope, continued.ProjectScope, err)
	}
}
