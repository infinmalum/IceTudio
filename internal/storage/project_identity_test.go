package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"icetudio/internal/project"
)

func identityState(t *testing.T) project.State {
	t.Helper()
	st, err := project.Blank("身份", 480)
	if err != nil {
		t.Fatal(err)
	}
	st.Tracks = []project.Track{{ID: "t_base", Clips: []project.Clip{{ID: "c_base", Length: 1920,
		Notes:    []project.Note{{ID: "n_base", Duration: 120, Pitch: 60, Velocity: 100}},
		Controls: []project.ControlEvent{{ID: "e_base", Kind: project.KindCC, Controller: 64, Value: 127}},
		Raw:      []project.RawEvent{{ID: "r_base", Tick: 240, Data: []byte{0xff, 0x06, 1, 'A'}}},
	}}}}
	return st
}

func assertIdentityReject(t *testing.T, s *Store, id, opID string, ops []project.Op) {
	t.Helper()
	ctx := context.Background()
	before, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ProjectHistory(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitProjectOps(ctx, id, opID, "undo", before.Revision, ops)
	if err == nil {
		t.Fatal("reused identity or injected restoration accepted")
	}
	after, err := s.GetProject(ctx, id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejection modified state: %+v %v", after, err)
	}
	afterHistory, err := s.ProjectHistory(ctx, id)
	if err != nil || !reflect.DeepEqual(history, afterHistory) {
		t.Fatalf("rejection modified history: %v", err)
	}
	if _, found, err := s.GetProjectTransaction(ctx, id, opID); err != nil || found {
		t.Fatalf("rejection published result: %t %v", found, err)
	}
}

func TestProjectIdentityReuseRejected(t *testing.T) {
	for _, kind := range []string{"track", "clip", "note", "control"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "identity.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			p, err := s.CreateProjectFromState(ctx, identityState(t))
			if err != nil {
				t.Fatal(err)
			}
			var add, remove project.Op
			switch kind {
			case "track":
				add = project.Op{Kind: "track.add", Track: &project.TrackSpec{ID: "t_new"}}
				remove = project.Op{Kind: "track.remove", TrackID: "t_new"}
			case "clip":
				add = project.Op{Kind: "clip.add", Clip: &project.ClipSpec{ID: "c_new", TrackID: "t_base", Length: 960}}
				remove = project.Op{Kind: "clip.remove", ClipID: "c_new"}
			case "note":
				add = project.Op{Kind: "note.add", ClipID: "c_base", Notes: []project.NewNote{{ID: "n_new", Duration: 120, Pitch: 64, Velocity: 100}}}
				remove = project.Op{Kind: "note.remove", ClipID: "c_base", NoteIDs: []string{"n_new"}}
			case "control":
				add = project.Op{Kind: "control.add", ClipID: "c_base", Events: []project.EventSpec{{ID: "e_new", Kind: project.KindPitchBend, Value: 8192}}}
				remove = project.Op{Kind: "control.remove", ClipID: "c_base", EventIDs: []string{"e_new"}}
			}
			created, err := s.SubmitProjectOps(ctx, p.ID, "create", "agent:test", 0, []project.Op{add})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SubmitProjectOps(ctx, p.ID, "remove", "gui", 1, []project.Op{remove}); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			s = reopened
			assertIdentityReject(t, s, p.ID, "reuse", []project.Op{{Kind: "project.rename", Title: "不应生效"}, add})
			replay, err := s.SubmitProjectOps(ctx, p.ID, "create", "changed-source", 0, []project.Op{add})
			if err != nil || !reflect.DeepEqual(replay, created) {
				t.Fatalf("original replay failed: %+v %v", replay, err)
			}
			// Undo deletion and redo it twice; restoration remains allowed.
			for i := 0; i < 4; i++ {
				current, _ := s.GetProject(ctx, p.ID)
				if i%2 == 0 {
					_, err = s.UndoProject(ctx, p.ID, fmt.Sprintf("undo-%d", i), "gui", current.Revision)
				} else {
					_, err = s.RedoProject(ctx, p.ID, fmt.Sprintf("redo-%d", i), "gui", current.Revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			assertIdentityReject(t, s, p.ID, "still-used", []project.Op{add})
		})
	}
}

func TestProjectIdentitySameBatchAndInjectedRestoration(t *testing.T) {
	for name, ops := range map[string][]project.Op{
		"remove then add":      {{Kind: "note.remove", ClipID: "c_base", NoteIDs: []string{"n_base"}}, {Kind: "note.add", ClipID: "c_base", Notes: []project.NewNote{{ID: "n_base", Duration: 120, Pitch: 64, Velocity: 100}}}},
		"create remove create": {{Kind: "track.add", Track: &project.TrackSpec{ID: "t_once"}}, {Kind: "track.remove", TrackID: "t_once"}, {Kind: "track.add", Track: &project.TrackSpec{ID: "t_once"}}},
		"injected clips":       {{Kind: "track.add", Track: &project.TrackSpec{ID: "t_attack"}, InvClips: []project.Clip{{ID: "c_base", Length: 100}}}},
		"injected notes":       {{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: "t_base", Length: 960}, InvNotes: []project.Note{{ID: "n_base", Duration: 120, Pitch: 60, Velocity: 100}}}},
		"injected controls":    {{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: "t_base", Length: 960}, InvControls: []project.ControlEvent{{ID: "e_base", Kind: project.KindCC}}}},
		"injected raw":         {{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: "t_base", Length: 960}, InvRaw: []project.RawEvent{{ID: "r_base", Data: []byte{0xf7}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			s := openProjects(t)
			p, err := s.CreateProjectFromState(context.Background(), identityState(t))
			if err != nil {
				t.Fatal(err)
			}
			assertIdentityReject(t, s, p.ID, "reject", ops)
		})
	}
}

func TestProjectIdentityImportedDescendantsStayReserved(t *testing.T) {
	for _, parent := range []string{"track", "clip"} {
		t.Run(parent, func(t *testing.T) {
			ctx := context.Background()
			s := openProjects(t)
			original := identityState(t)
			p, err := s.CreateProjectFromState(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			remove := project.Op{Kind: "track.remove", TrackID: "t_base"}
			if parent == "clip" {
				remove = project.Op{Kind: "clip.remove", ClipID: "c_base"}
			}
			if _, err := s.SubmitProjectOps(ctx, p.ID, "remove", "gui", 0, []project.Op{remove}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"c_base", "n_base", "e_base", "r_base"} {
				// Deliberately use a different type: one shared identity namespace.
				assertIdentityReject(t, s, p.ID, "reuse-"+id, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: id}}})
			}
			if _, err := s.UndoProject(ctx, p.ID, "restore", "gui", 1); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetProject(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(got.State, original) {
				t.Fatalf("nested restore changed events: %+v %v", got.State, err)
			}
		})
	}
}

// Keep a distinct assertion for the error text used by IPC/UI callers.
func TestProjectIdentityReuseExplainsNextStep(t *testing.T) {
	s := openProjects(t)
	ctx := context.Background()
	p, err := s.CreateProject(ctx, "说明", 480)
	if err != nil {
		t.Fatal(err)
	}
	add := []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t_used"}}}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "create", "gui", 0, add); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoProject(ctx, p.ID, "undo", "gui", 1); err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitProjectOps(ctx, p.ID, "reuse", "gui", 2, add)
	if err == nil || !strings.Contains(err.Error(), "已使用") || !strings.Contains(err.Error(), "撤销") {
		t.Fatalf("missing usable explanation: %v", err)
	}
}

func TestProjectIdentityUndoBranchFailureAndIsolation(t *testing.T) {
	ctx := context.Background()
	s := openProjects(t)
	p, err := s.CreateProjectFromState(ctx, identityState(t))
	if err != nil {
		t.Fatal(err)
	}
	add := []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t_once"}}}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "create", "gui", 0, add); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoProject(ctx, p.ID, "undo", "gui", 1); err != nil {
		t.Fatal(err)
	}
	assertIdentityReject(t, s, p.ID, "undone-reuse", add)
	if _, err := s.SubmitProjectOps(ctx, p.ID, "branch", "gui", 2, []project.Op{{Kind: "project.rename", Title: "新分支"}}); err != nil {
		t.Fatal(err)
	}
	assertIdentityReject(t, s, p.ID, "branch-reuse", add)
	if _, err := s.RedoProject(ctx, p.ID, "invalid-redo", "gui", 3); err == nil {
		t.Fatal("new branch retained old redo")
	}
	other, err := s.CreateProject(ctx, "另一个工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitProjectOps(ctx, other.ID, "same-id", "gui", 0, add); err != nil {
		t.Fatalf("another project affected: %v", err)
	}

	// Validation failure must not reserve either a new explicit identity or
	// a new opID. Correcting the batch can use both as originally intended.
	retry := []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t_retry"}}}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "retry", "gui", 3, append(append([]project.Op{}, retry...), project.Op{Kind: "invalid"})); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "stale", "gui", 0, retry); err == nil {
		t.Fatal("stale batch accepted")
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_identity_history BEFORE INSERT ON project_revisions BEGIN SELECT RAISE(ABORT, 'identity write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "retry", "gui", 3, retry); err == nil {
		t.Fatal("faulted history write accepted")
	}
	got, _ := s.GetProject(ctx, p.ID)
	if got.Revision != 3 || len(got.State.Tracks) != 1 {
		t.Fatal("failed edit modified project")
	}
	if _, found, err := s.GetProjectTransaction(ctx, p.ID, "retry"); err != nil || found {
		t.Fatalf("failure published result: %t %v", found, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_identity_history`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "retry", "gui", 3, retry); err != nil {
		t.Fatalf("failed attempt reserved ID: %v", err)
	}
	assertIdentityReject(t, s, p.ID, "same-batch-cross-type", []project.Op{{Kind: "track.remove", TrackID: "t_retry"}, {Kind: "clip.add", Clip: &project.ClipSpec{ID: "t_retry", TrackID: "t_base", Length: 960}}})
}

func TestProjectIdentityConcurrentCreation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	p, err := s.CreateProject(ctx, "并发", 480)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i, store := range []*Store{s, other} {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			<-start
			_, err := store.SubmitProjectOps(ctx, p.ID, fmt.Sprintf("create-%d", i), "gui", 0, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t_shared"}}})
			errors <- err
		}(i, store)
	}
	close(start)
	wg.Wait()
	close(errors)
	successes := 0
	for err := range errors {
		if err == nil {
			successes++
		}
	}
	got, err := s.GetProject(ctx, p.ID)
	if err != nil || successes != 1 || got.Revision != 1 || len(got.State.Tracks) != 1 {
		t.Fatalf("concurrent result: %d %+v %v", successes, got, err)
	}
	if _, err := s.SubmitProjectOps(ctx, p.ID, "remove", "gui", 1, []project.Op{{Kind: "track.remove", TrackID: "t_shared"}}); err != nil {
		t.Fatal(err)
	}
	// A second Store with the current revision must observe the committed
	// history, rather than relying on an in-memory identity cache.
	assertIdentityReject(t, other, p.ID, "reuse-from-other-store", []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t_shared"}}})
}
