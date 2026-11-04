package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func testEdits(t *testing.T) (*Edits, *app.Service, storage.Project) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	state, _ := project.Blank("Native", 480)
	state.Tracks = []project.Track{{ID: "t_native", Name: "Keys", Clips: []project.Clip{{ID: "c_native", Length: 1920, Notes: []project.Note{{ID: "n_native", Duration: 480, Pitch: 60, Velocity: 90}}, Controls: []project.ControlEvent{{ID: "e_native", Kind: project.KindCC, Controller: 64, Value: 127}}, Raw: []project.RawEvent{{ID: "r_native", Tick: 960, Data: []byte{0xff, 0x06, 0x01, 'A'}}}}}}}
	p, err := store.CreateProjectFromState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(app.Options{Store: store, DataDir: dir})
	return NewEdits(svc), svc, p
}
func TestNativeEditsShareTransactionsUndoAndConflicts(t *testing.T) {
	e, svc, p := testEdits(t)
	target := EditTarget{p.ID, p.Revision}
	muted := true
	ops := []project.Op{{Kind: "track.update", TrackID: "t_native", TrackPatch: &project.TrackPatch{Muted: &muted}}}
	if _, err := e.Execute(target, "edit", ops); err == nil {
		t.Fatal("write before recovery check")
	}
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(target, "edit", ops)
	if err != nil || result.Revision != 1 {
		t.Fatalf("edit: %+v %v", result, err)
	}
	got, _ := svc.GetProject(p.ID)
	if !got.State.Tracks[0].Muted || len(got.State.Tracks[0].Clips[0].Controls) != 1 {
		t.Fatal("track edit altered events")
	}
	// An edit through the existing service shares history with the new Native UI.
	if _, err = svc.SubmitProjectOps(p.ID, "other-entry", "gui", 1, []project.Op{{Kind: "project.rename", Title: "Other entry"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(target, "edit", ops); err == nil {
		t.Fatal("silently rebased stale gesture")
	}
	if s := e.State(); s.Pending != nil || !s.Ready || s.Busy {
		t.Fatalf("completed local rejection left blocked state: %+v", s)
	}
	if _, err = e.Execute(EditTarget{p.ID, 2}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetProject(p.ID)
	if got.Title != "Native" || !got.State.Tracks[0].Muted {
		t.Fatal("did not undo other entry")
	}
	if _, err = e.Execute(EditTarget{p.ID, 3}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetProject(p.ID)
	if got.State.Tracks[0].Muted {
		t.Fatal("did not undo Native edit")
	}
	if _, err = e.Execute(EditTarget{p.ID, 4}, "redo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetProject(p.ID)
	if !got.State.Tracks[0].Muted {
		t.Fatal("did not redo Native edit")
	}
	if pending, err := svc.GetPendingProjectEdit(); err != nil || pending != nil {
		t.Fatalf("journal retained after success: %+v %v", pending, err)
	}
}

type uncertainEdits struct {
	editBackend
	failQuery bool
	failClear bool
	failBegin bool
	calls     int
}

func (b *uncertainEdits) BeginProjectEdit(id, op string, rev int64, kind string) error {
	if b.failBegin {
		return errors.New("disk write failed")
	}
	return b.editBackend.BeginProjectEdit(id, op, rev, kind)
}
func (b *uncertainEdits) SubmitProjectOps(id, op, source string, rev int64, ops []project.Op) (project.Result, error) {
	b.calls++
	result, err := b.editBackend.SubmitProjectOps(id, op, source, rev, ops)
	if err != nil {
		return result, err
	}
	return project.Result{}, errors.New("test lost acknowledgement after commit")
}
func (b *uncertainEdits) GetProjectTransaction(id, op string) (project.Result, bool, error) {
	if b.failQuery {
		return project.Result{}, false, errors.New("test query unavailable")
	}
	return b.editBackend.GetProjectTransaction(id, op)
}
func (b *uncertainEdits) ClearPendingProjectEdit(id, op string) error {
	if b.failClear {
		return errors.New("test cleanup failed")
	}
	return b.editBackend.ClearPendingProjectEdit(id, op)
}

func TestNativeEditsRecoverCommitWithoutReplay(t *testing.T) {
	e, svc, p := testEdits(t)
	fault := &uncertainEdits{editBackend: svc, failQuery: true}
	e.backend = fault
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	_, err := e.Execute(EditTarget{p.ID, 0}, "edit", []project.Op{{Kind: "project.rename", Title: "Saved"}})
	if err == nil || e.State().Pending == nil {
		t.Fatal("unknown commit was discarded")
	}
	before := e.State().Pending
	if _, err = e.Execute(EditTarget{p.ID, 1}, "undo", nil); err == nil {
		t.Fatal("overwrote unresolved intention")
	}
	restarted := NewEdits(svc)
	if err = restarted.Check(); err != nil {
		t.Fatal(err)
	}
	if restarted.State().Pending.OpID != before.OpID {
		t.Fatal("replaced original operation on recovery")
	}
	result, found, err := restarted.Query()
	if err != nil || !found || result.Revision != 1 {
		t.Fatalf("query: %+v %v %v", result, found, err)
	}
	got, _ := svc.GetProject(p.ID)
	if got.Revision != 1 || got.Title != "Saved" || fault.calls != 1 || restarted.State().Pending != nil {
		t.Fatal("query replayed edit or failed cleanup")
	}
}
func TestNativeEditsNeverSendWithoutJournalAndKeepCleanupFailures(t *testing.T) {
	e, svc, p := testEdits(t)
	fault := &uncertainEdits{editBackend: svc, failBegin: true}
	e.backend = fault
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	ops := []project.Op{{Kind: "project.rename", Title: "Saved"}}
	if _, err := e.Execute(EditTarget{p.ID, 0}, "edit", ops); err == nil || fault.calls != 0 {
		t.Fatal("sent music after failed intention write")
	}
	fault.failBegin = false
	fault.failClear = true
	result, err := e.Execute(EditTarget{p.ID, 0}, "edit", ops)
	if err != nil || result.Revision != 1 || e.State().Pending == nil || !strings.HasPrefix(e.State().Message, "已保存") {
		t.Fatalf("lost committed conclusion: %+v %v %+v", result, err, e.State())
	}
	fault.failClear = false
	if _, found, err := e.Query(); err != nil || !found || e.State().Pending != nil {
		t.Fatalf("cleanup retry: %v %v", found, err)
	}
	// An intention with no durable transaction must remain uncertain after query.
	if err = svc.BeginProjectEdit(p.ID, "original-missing", 1, "edit"); err != nil {
		t.Fatal(err)
	}
	if err = e.Check(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := e.Query(); err != nil || found || e.State().Pending == nil {
		t.Fatal("absence treated as permission to replay or clear")
	}
	snapshot := e.State()
	snapshot.Pending.OpID = "mutated"
	if e.State().Pending.OpID != "original-missing" {
		t.Fatal("caller mutated recovery identity")
	}
}

type blockedEdits struct {
	editBackend
	started chan struct{}
	release chan struct{}
}

func (b *blockedEdits) SubmitProjectOps(id, op, source string, rev int64, ops []project.Op) (project.Result, error) {
	close(b.started)
	<-b.release
	return b.editBackend.SubmitProjectOps(id, op, source, rev, ops)
}
func TestDesktopCoordinatorSerializesCommands(t *testing.T) {
	e, svc, p := testEdits(t)
	blocked := &blockedEdits{editBackend: svc, started: make(chan struct{}), release: make(chan struct{})}
	e.backend = blocked
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.Execute(EditTarget{p.ID, 0}, "edit", []project.Op{{Kind: "project.rename", Title: "First"}})
		done <- err
	}()
	<-blocked.started
	if _, err := e.Execute(EditTarget{p.ID, 0}, "undo", nil); err == nil {
		t.Error("concurrent second write entered single journal slot")
	}
	if err := e.Check(); err == nil {
		t.Error("recovery check raced submission")
	}
	close(blocked.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
