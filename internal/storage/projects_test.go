package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"icetudio/internal/project"
)

func TestProjectGeneratedIDsSurviveRedo(t *testing.T) {
	for _, kind := range []string{"track.add", "clip.add", "note.add", "control.add"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "ids.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			p, err := s.CreateProject(ctx, "稳定身份", 480)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.SubmitProjectOps(ctx, p.ID, "seed", "test", 0, []project.Op{
				{Kind: "track.add", Track: &project.TrackSpec{ID: "t_base"}},
				{Kind: "clip.add", Clip: &project.ClipSpec{ID: "c_base", TrackID: "t_base", Length: 1920}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var ops []project.Op
			switch kind {
			case "track.add":
				ops = []project.Op{{Kind: kind, Track: &project.TrackSpec{Name: "自动轨"}}}
			case "clip.add":
				ops = []project.Op{{Kind: kind, Clip: &project.ClipSpec{TrackID: "t_base", Length: 960}}}
			case "note.add":
				ops = []project.Op{{Kind: kind, ClipID: "c_base", Notes: []project.NewNote{{Duration: 120, Pitch: 60, Velocity: 100}, {ID: "n_explicit", Start: 120, Duration: 240, Pitch: 64, Velocity: 80}, {Start: 480, Duration: 120, Pitch: 67, Velocity: 90}}}}
			case "control.add":
				ops = []project.Op{{Kind: kind, ClipID: "c_base", Events: []project.EventSpec{{Kind: project.KindCC, Controller: 64, Value: 127}, {ID: "e_explicit", Kind: project.KindProgram, Tick: 120, Program: 2}, {Kind: project.KindPitchBend, Tick: 240, Value: 9000}}}}
			}
			before, _ := s.GetProject(ctx, p.ID)
			requestBefore, _ := json.Marshal(ops)
			res, err := s.SubmitProjectOps(ctx, p.ID, "create", "agent:test", 1, ops)
			if err != nil {
				t.Fatal(err)
			}
			created, _ := s.GetProject(ctx, p.ID)
			requestAfter, _ := json.Marshal(ops)
			if string(requestAfter) != string(requestBefore) {
				t.Fatal("submit modified caller's input")
			}
			for cycle := 0; cycle < 3; cycle++ {
				// Reopen before every undo and redo, not just the first edit.
				for _, undo := range []bool{true, false} {
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					s, err = Open(path)
					if err != nil {
						t.Fatal(err)
					}
					current, _ := s.GetProject(ctx, p.ID)
					id := fmt.Sprintf("%t-%d", undo, cycle)
					var got project.Result
					if undo {
						got, err = s.UndoProject(ctx, p.ID, id, "gui", current.Revision)
					} else {
						got, err = s.RedoProject(ctx, p.ID, id, "gui", current.Revision)
					}
					if err != nil {
						t.Fatal(err)
					}
					after, _ := s.GetProject(ctx, p.ID)
					want := created.State
					if undo {
						want = before.State
					}
					if !reflect.DeepEqual(after.State, want) {
						t.Fatalf("cycle %d undo=%t changed stable identity: got %+v want %+v", cycle, undo, after.State, want)
					}
					if !undo && !reflect.DeepEqual(got.CreatedIDs, res.CreatedIDs) {
						t.Fatalf("redo IDs %v, original %v", got.CreatedIDs, res.CreatedIDs)
					}
					old, found, err := s.GetProjectTransaction(ctx, p.ID, "create")
					if err != nil || !found || !reflect.DeepEqual(old, res) {
						t.Fatalf("original result changed: %+v %v %v", old, found, err)
					}
				}
			}
			// A newer edit refers to the creation's original ID. Both edits
			// must remain redoable in order, then undoable again.
			var follow project.Op
			name, pitch := "后续修改", 72
			switch kind {
			case "track.add":
				follow = project.Op{Kind: "track.update", TrackID: res.CreatedIDs[0], TrackPatch: &project.TrackPatch{Name: &name}}
			case "clip.add":
				follow = project.Op{Kind: "clip.update", ClipID: res.CreatedIDs[0], ClipPatch: &project.ClipPatch{Name: &name}}
			case "note.add":
				follow = project.Op{Kind: "note.update", ClipID: "c_base", NoteDeltas: []project.NoteDelta{{ID: res.CreatedIDs[0], Pitch: &pitch}}}
			case "control.add":
				follow = project.Op{Kind: "control.remove", ClipID: "c_base", EventIDs: []string{res.CreatedIDs[0]}}
			}
			current, _ := s.GetProject(ctx, p.ID)
			if _, err := s.SubmitProjectOps(ctx, p.ID, "follow", "gui", current.Revision, []project.Op{follow}); err != nil {
				t.Fatal(err)
			}
			modified, _ := s.GetProject(ctx, p.ID)
			for i, undo := range []bool{true, true, false, false, true, true, false, false} {
				current, _ = s.GetProject(ctx, p.ID)
				id := fmt.Sprintf("stack-%d", i)
				if undo {
					_, err = s.UndoProject(ctx, p.ID, id, "gui", current.Revision)
				} else {
					_, err = s.RedoProject(ctx, p.ID, id, "gui", current.Revision)
				}
				if err != nil {
					t.Fatalf("dependent stack step %d: %v", i, err)
				}
				if i%4 == 3 {
					got, _ := s.GetProject(ctx, p.ID)
					if !reflect.DeepEqual(got.State, modified.State) {
						t.Fatalf("dependent stack changed state at %d", i)
					}
				}
			}
			current, _ = s.GetProject(ctx, p.ID)
			replayed, err := s.SubmitProjectOps(ctx, p.ID, "create", "changed-source", 0, nil)
			if err != nil || !reflect.DeepEqual(replayed, res) {
				t.Fatalf("creation replay changed original outcome: %+v %v", replayed, err)
			}
			afterReplay, _ := s.GetProject(ctx, p.ID)
			if !reflect.DeepEqual(afterReplay, current) {
				t.Fatal("creation replay modified current project")
			}
		})
	}
}

func TestProjectGeneratedIDHistoryFailureIsAtomic(t *testing.T) {
	for _, direction := range []string{"edit", "undo", "redo"} {
		t.Run(direction, func(t *testing.T) {
			ctx := context.Background()
			s := openProjects(t)
			p := seededProject(t, ctx, s)
			ops := []project.Op{{Kind: "track.add", Track: &project.TrackSpec{Name: "自动 ID"}}}
			var creation project.Result
			var err error
			if direction != "edit" {
				creation, err = s.SubmitProjectOps(ctx, p.ID, "creation", "test", p.Revision, ops)
				if err != nil {
					t.Fatal(err)
				}
			}
			if direction == "redo" {
				if _, err := s.UndoProject(ctx, p.ID, "undo-setup", "test", creation.Revision); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.GetProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			historyBefore, err := s.ProjectHistory(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			// INSERT is after state UPDATE and undone-flag UPDATE. Failure
			// must roll all of those writes back, including generated IDs.
			if _, err := s.db.Exec(`CREATE TRIGGER fail_generated_history BEFORE INSERT ON project_revisions BEGIN SELECT RAISE(ABORT, 'injected history failure'); END`); err != nil {
				t.Fatal(err)
			}
			attempt := func() (project.Result, error) {
				switch direction {
				case "undo":
					return s.UndoProject(ctx, p.ID, "attempt", "test", before.Revision)
				case "redo":
					return s.RedoProject(ctx, p.ID, "attempt", "test", before.Revision)
				default:
					return s.SubmitProjectOps(ctx, p.ID, "attempt", "test", before.Revision, ops)
				}
			}
			if _, err := attempt(); err == nil || !strings.Contains(err.Error(), "injected history failure") {
				t.Fatalf("expected late history failure: %v", err)
			}
			after, err := s.GetProject(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("partial project write: %+v %v", after, err)
			}
			historyAfter, err := s.ProjectHistory(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(historyAfter, historyBefore) {
				t.Fatalf("partial history write: %+v %v", historyAfter, err)
			}
			if _, found, err := s.GetProjectTransaction(ctx, p.ID, "attempt"); err != nil || found {
				t.Fatalf("failed operation published result: %t %v", found, err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_generated_history`); err != nil {
				t.Fatal(err)
			}
			result, err := attempt()
			if err != nil || result.Revision != before.Revision+1 {
				t.Fatalf("retry failed: %+v %v", result, err)
			}
			if direction == "redo" && !reflect.DeepEqual(result.CreatedIDs, creation.CreatedIDs) {
				t.Fatal("retry changed original IDs")
			}
		})
	}
}

func openProjects(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seededProject(t *testing.T, ctx context.Context, s *Store) Project {
	t.Helper()
	p, err := s.CreateProject(ctx, "测试工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.SubmitProjectOps(ctx, p.ID, "op1", "test", 0, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{Name: "轨", Channel: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 1 {
		t.Fatalf("first edit revision: %d", res.Revision)
	}
	p, err = s.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProjectLifecycleAndIndependence(t *testing.T) {
	ctx := context.Background()
	s := openProjects(t)
	p := seededProject(t, ctx, s)

	items, err := s.ListProjects(ctx)
	if err != nil || len(items) != 1 || items[0].ID != p.ID || items[0].Revision != 1 {
		t.Fatalf("list: %+v %v", items, err)
	}

	// conversation bound to project; deleting the conversation keeps it
	conv, err := s.CreateConversation(ctx, DefaultConfigID, "", "对话")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionProject(ctx, conv.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	session, err := s.GetSession(ctx, conv.ID)
	if err != nil || session.ProjectID != p.ID {
		t.Fatalf("binding: %+v %v", session, err)
	}
	if err := s.DeleteConversation(ctx, conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProject(ctx, p.ID); err != nil {
		t.Fatalf("project must survive conversation deletion: %v", err)
	}

	// deleting the project clears bindings instead of breaking FKs
	conv2, err := s.CreateConversation(ctx, DefaultConfigID, "", "对话2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionProject(ctx, conv2.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	session, err = s.GetSession(ctx, conv2.ID)
	if err != nil || session.ProjectID != "" {
		t.Fatalf("binding must be cleared: %+v %v", session, err)
	}
	if err := s.SetSessionProject(ctx, conv2.ID, "missing"); err == nil {
		t.Fatal("binding to missing project must fail")
	}
}

func TestProjectTransactionReplayAndConflict(t *testing.T) {
	ctx := context.Background()
	s := openProjects(t)
	p := seededProject(t, ctx, s)

	track := p.State.Tracks[0].ID
	clipOps := []project.Op{{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: track, Start: 0, Length: 480}}}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "op2", "test", 1, clipOps); err != nil {
		t.Fatal(err)
	}

	// replay with the same opID returns the stored result and adds nothing —
	// including notes: a retried note.add must not duplicate them
	afterClip, err := s.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	noteOps := []project.Op{{
		Kind:   "note.add",
		ClipID: afterClip.State.Tracks[0].Clips[0].ID,
		Notes:  []project.NewNote{{Start: 0, Duration: 240, Pitch: 60, Velocity: 100}},
	}}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "op3", "test", 2, noteOps); err != nil {
		t.Fatal(err)
	}
	replay, err := s.SubmitProjectOps(ctx, p.ID, "op3", "retry", 2, noteOps)
	if err != nil {
		t.Fatal(err)
	}
	if replay.OpID != "op3" || replay.Revision != 3 {
		t.Fatalf("replay result: %+v", replay)
	}
	current, err := s.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.State.Tracks[0].Clips) != 1 {
		t.Fatalf("replay duplicated state: %+v", current.State.Tracks[0].Clips)
	}
	if len(current.State.Tracks[0].Clips[0].Notes) != 1 {
		t.Fatalf("replay duplicated notes: %+v", current.State.Tracks[0].Clips[0].Notes)
	}

	// stale base revision is rejected without any change
	if _, err := s.SubmitProjectOps(ctx, p.ID, "op-stale", "test", 1, []project.Op{
		{Kind: "project.rename", Title: "新名"},
	}); err == nil {
		t.Fatal("stale base revision must be rejected")
	}
	check, err := s.GetProject(ctx, p.ID)
	if err != nil || check.Revision != 3 || check.State.Title == "新名" {
		t.Fatalf("rejected write leaked: rev=%d %v", check.Revision, err)
	}

	// found via the durable result query (tool recorded nothing but the
	// project committed)
	res, err := s.SubmitProjectOps(ctx, p.ID, "op4", "test", 3, []project.Op{
		{Kind: "project.rename", Title: "确认"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := s.GetProjectTransaction(ctx, p.ID, "op4")
	if err != nil || !found || got.Revision != res.Revision {
		t.Fatalf("transaction lookup: found=%v %+v %v", found, got, err)
	}
	if _, found, _ := s.GetProjectTransaction(ctx, p.ID, "nope"); found {
		t.Fatal("unknown opID must not be found")
	}
}

func TestProjectUndoRedoPersistence(t *testing.T) {
	ctx := context.Background()
	s := openProjects(t)
	p := seededProject(t, ctx, s)

	if _, err := s.SubmitProjectOps(ctx, p.ID, "op2", "test", 1, []project.Op{
		{Kind: "project.rename", Title: "改名"},
	}); err != nil {
		t.Fatal(err)
	}

	// undo the rename
	if _, err := s.UndoProject(ctx, p.ID, "undo1", "test", 2); err != nil {
		t.Fatal(err)
	}
	afterUndo, err := s.GetProject(ctx, p.ID)
	if err != nil || afterUndo.Revision != 3 || afterUndo.State.Title != "测试工程" {
		t.Fatalf("undo: rev=%d title=%q %v", afterUndo.Revision, afterUndo.State.Title, err)
	}
	// replaying the same undo opID is idempotent
	if _, err := s.UndoProject(ctx, p.ID, "undo1", "test", 3); err != nil {
		t.Fatalf("undo replay: %v", err)
	}
	// redo
	if _, err := s.RedoProject(ctx, p.ID, "redo1", "test", 3); err != nil {
		t.Fatal(err)
	}
	afterRedo, err := s.GetProject(ctx, p.ID)
	if err != nil || afterRedo.Revision != 4 || afterRedo.State.Title != "改名" {
		t.Fatalf("redo: rev=%d title=%q %v", afterRedo.Revision, afterRedo.State.Title, err)
	}
	// redo again must fail (nothing left to redo)
	if _, err := s.RedoProject(ctx, p.ID, "redo2", "test", 4); err == nil {
		t.Fatal("second redo must fail")
	}
	// undo twice: rename, then the track.add
	if _, err := s.UndoProject(ctx, p.ID, "undo2", "test", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoProject(ctx, p.ID, "undo3", "test", 5); err != nil {
		t.Fatal(err)
	}
	empty, err := s.GetProject(ctx, p.ID)
	if err != nil || empty.Revision != 6 || len(empty.State.Tracks) != 0 {
		t.Fatalf("second undo: rev=%d tracks=%d %v", empty.Revision, len(empty.State.Tracks), err)
	}

	// reopening the store keeps state, revision and history (recovery proxy)
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.GetProject(ctx, p.ID)
	if err != nil || again.Revision != 6 || len(again.State.Tracks) != 0 || again.State.Title != "测试工程" {
		t.Fatalf("reopen: %+v %v", again, err)
	}
	history, err := reopened.ProjectHistory(ctx, p.ID)
	if err != nil || len(history) != 6 {
		t.Fatalf("history: %d %v", len(history), err)
	}
	if _, err := reopened.RedoProject(ctx, p.ID, "redo3", "test", 6); err != nil {
		t.Fatalf("redo after reopen: %v", err)
	}
	final, err := reopened.GetProject(ctx, p.ID)
	if err != nil || final.State.Title != "测试工程" || len(final.State.Tracks) != 1 {
		t.Fatalf("redo after reopen applied wrong entry: %+v", final.State)
	}
}

func TestProjectDeleteCascadeHistory(t *testing.T) {
	ctx := context.Background()
	s := openProjects(t)
	p := seededProject(t, ctx, s)
	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	history, err := s.ProjectHistory(ctx, p.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("history must cascade: %d %v", len(history), err)
	}
}

// The lightweight list and detail metadata must describe the same committed
// title as the music state, including after history traversal and reopening.
func TestProjectRenameProjectionPersistence(t *testing.T) {
	ctx := context.Background()
	s := openProjects(t)
	t.Cleanup(func() { s.Close() })
	p, err := s.CreateProject(ctx, "原名", 480)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(ctx, "另一个工程", 960)
	if err != nil {
		t.Fatal(err)
	}
	check := func(title string, revision int64) {
		t.Helper()
		item, e := s.GetProject(ctx, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		if item.Title != title || item.State.Title != title || item.Revision != revision {
			t.Errorf("detail at %d: title=%q state=%q revision=%d; want %q", revision, item.Title, item.State.Title, item.Revision, title)
		}
		items, e := s.ListProjects(ctx)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, summary := range items {
			if summary.ID == p.ID {
				found = true
				if summary.Title != title || summary.Revision != revision {
					t.Errorf("list at %d: %+v; want title %q", revision, summary, title)
				}
			}
		}
		if !found {
			t.Fatal("project missing from list")
		}
		untouched, e := s.GetProject(ctx, other.ID)
		if e != nil || !reflect.DeepEqual(untouched, other) {
			t.Fatalf("rename affected another project: %+v %v", untouched, e)
		}
	}
	// Reopen at every step: an in-memory projection repair cannot pass this.
	reopen := func() {
		t.Helper()
		path := s.path
		if e := s.Close(); e != nil {
			t.Fatal(e)
		}
		var e error
		s, e = Open(path)
		if e != nil {
			t.Fatal(e)
		}
	}
	check("原名", 0)
	finalTitle := strings.Repeat("曲", 200)
	if _, err = s.SubmitProjectOps(ctx, p.ID, "rename", "gui", 0, []project.Op{
		{Kind: "project.rename", Title: "中间名称"}, {Kind: "project.rename", Title: finalTitle},
	}); err != nil {
		t.Fatal(err)
	}
	check(finalTitle, 1)
	reopen()
	check(finalTitle, 1)
	if _, err = s.UndoProject(ctx, p.ID, "undo-rename", "cli", 1); err != nil {
		t.Fatal(err)
	}
	check("原名", 2)
	reopen()
	check("原名", 2)
	if _, err = s.RedoProject(ctx, p.ID, "redo-rename", "gui", 2); err != nil {
		t.Fatal(err)
	}
	check(finalTitle, 3)
	reopen()
	check(finalTitle, 3)
	// Old opIDs return their original results, never their old title/revision.
	if result, e := s.SubmitProjectOps(ctx, p.ID, "rename", "retry", 0, []project.Op{{Kind: "project.rename", Title: "不得覆盖"}}); e != nil || result.Revision != 1 {
		t.Fatalf("rename replay: %+v %v", result, e)
	}
	if result, e := s.UndoProject(ctx, p.ID, "undo-rename", "retry", 1); e != nil || result.Revision != 2 {
		t.Fatalf("undo replay: %+v %v", result, e)
	}
	if result, e := s.RedoProject(ctx, p.ID, "redo-rename", "retry", 2); e != nil || result.Revision != 3 {
		t.Fatalf("redo replay: %+v %v", result, e)
	}
	check(finalTitle, 3)
	// Empty titles are already accepted by project.rename: do not introduce a
	// separate title normalization policy in the storage projection.
	if _, err = s.SubmitProjectOps(ctx, p.ID, "empty-title", "gui", 3, []project.Op{{Kind: "project.rename", Title: ""}}); err != nil {
		t.Fatal(err)
	}
	check("", 4)
	reopen()
	check("", 4)
}

func TestProjectRenameFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"edit", "undo", "redo", "invalid-batch", "stale", "too-long"} {
		t.Run(kind, func(t *testing.T) {
			s := openProjects(t)
			p := seededProject(t, ctx, s)
			if kind == "undo" || kind == "redo" {
				if _, err := s.SubmitProjectOps(ctx, p.ID, "rename", "test", 1, []project.Op{{Kind: "project.rename", Title: "新名"}}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "redo" {
				if _, err := s.UndoProject(ctx, p.ID, "undo", "test", 2); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.GetProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			history, err := s.ProjectHistory(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "edit" || kind == "undo" || kind == "redo" {
				// Fail after UPDATE projects, at the history INSERT. Both the
				// metadata and the state/history changes must roll back together.
				if _, err = s.db.Exec(`CREATE TRIGGER reject_project_history BEFORE INSERT ON project_revisions BEGIN SELECT RAISE(ABORT, 'injected history failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			ops := []project.Op{{Kind: "project.rename", Title: "不得泄漏"}}
			base := before.Revision
			switch kind {
			case "invalid-batch":
				ops = append(ops, project.Op{Kind: "track.remove", TrackID: "t_missing"})
			case "stale":
				base--
			case "too-long":
				ops[0].Title = strings.Repeat("曲", 201)
			}
			attempt := func() (project.Result, error) {
				switch kind {
				case "undo":
					return s.UndoProject(ctx, p.ID, "attempt", "test", base)
				case "redo":
					return s.RedoProject(ctx, p.ID, "attempt", "test", base)
				default:
					return s.SubmitProjectOps(ctx, p.ID, "attempt", "test", base, ops)
				}
			}
			if _, err = attempt(); err == nil {
				t.Fatal("failure accepted")
			} else if (kind == "edit" || kind == "undo" || kind == "redo") && !strings.Contains(err.Error(), "injected history failure") {
				t.Fatalf("did not reach history insertion: %v", err)
			}
			after, err := s.GetProject(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed write changed project: before=%+v after=%+v %v", before, after, err)
			}
			afterHistory, err := s.ProjectHistory(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(history, afterHistory) {
				t.Fatalf("failed write changed history: %+v %v", afterHistory, err)
			}
			items, err := s.ListProjects(ctx)
			if err != nil || len(items) != 1 || items[0].Title != before.Title || items[0].Revision != before.Revision {
				t.Fatalf("failed write changed list: %+v %v", items, err)
			}
			if _, found, e := s.GetProjectTransaction(ctx, p.ID, "attempt"); e != nil || found {
				t.Fatalf("failed write published a result: %v %v", found, e)
			}
			if kind == "edit" || kind == "undo" || kind == "redo" {
				if _, err = s.db.Exec(`DROP TRIGGER reject_project_history`); err != nil {
					t.Fatal(err)
				}
				if result, e := attempt(); e != nil || result.Revision != before.Revision+1 {
					t.Fatalf("same ID could not commit after fault removed: %+v %v", result, e)
				}
			}
		})
	}
}
