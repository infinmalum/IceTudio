package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func TestProjectRecoveryReopenAndCommittedResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "music.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Store: store, DataDir: dir})
	p, err := svc.CreateProject("恢复工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.BeginProjectEdit(p.ID, "original-op", p.Revision, "edit"); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.GetProject(p.ID)
	if !reflect.DeepEqual(p, before) {
		t.Fatal("intention altered project")
	}
	if err = svc.BeginProjectEdit(p.ID, "original-op", p.Revision, "edit"); err != nil {
		t.Fatal(err)
	}
	if err = svc.BeginProjectEdit(p.ID, "original-op", p.Revision+1, "edit"); err == nil {
		t.Fatal("changed intent accepted")
	}
	if err = svc.BeginProjectEdit(p.ID, "original-op", p.Revision, "undo"); err == nil {
		t.Fatal("changed kind accepted")
	}
	if err = svc.BeginProjectEdit(p.ID, "new-op", p.Revision, "edit"); err == nil {
		t.Fatal("pending overwritten")
	}
	want, _ := svc.GetPendingProjectEdit()
	if want.Title != p.Title {
		t.Fatal("missing project title")
	}
	failedHost := New(Options{Store: store, DataDir: dir, InitError: errors.New("host lock unavailable")})
	if _, e := failedHost.GetPendingProjectEdit(); e == nil {
		t.Fatal("failed host read another owner's journal")
	}
	if e := failedHost.ClearPendingProjectEdit(p.ID, want.OpID); e == nil {
		t.Fatal("failed host cleared another owner's journal")
	}
	unsubmitted := New(Options{Store: store, DataDir: dir})
	if recovered, e := unsubmitted.GetPendingProjectEdit(); e != nil || !reflect.DeepEqual(recovered, want) {
		t.Fatalf("unsubmitted intention did not survive service reconstruction: %+v %v", recovered, e)
	}
	if _, found, e := svc.GetProjectTransaction(p.ID, want.OpID); e != nil || found {
		t.Fatal("unsubmitted marker became commit")
	}
	if _, err = svc.SubmitProjectOps(p.ID, want.OpID, "gui", p.Revision, []project.Op{{Kind: "project.rename", Title: "已提交"}}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reopened := New(Options{Store: store, DataDir: dir})
	got, err := reopened.GetPendingProjectEdit()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery after reopen: %+v %v", got, err)
	}
	result, found, err := reopened.GetProjectTransaction(got.ProjectID, got.OpID)
	if err != nil || !found || result.Revision != 1 {
		t.Fatalf("committed outcome: %+v %v %v", result, found, err)
	}
	if err = reopened.ClearPendingProjectEdit(p.ID, "stale-op"); err == nil {
		t.Fatal("stale clear accepted")
	}
	if err = reopened.ClearPendingProjectEdit(p.ID, got.OpID); err != nil {
		t.Fatal(err)
	}
	if err = reopened.ClearPendingProjectEdit(p.ID, got.OpID); err != nil {
		t.Fatal(err)
	}
	if got, err = reopened.GetPendingProjectEdit(); err != nil || got != nil {
		t.Fatalf("not cleared: %+v %v", got, err)
	}
	current, _ := reopened.GetProject(p.ID)
	if current.Title != "已提交" || current.State.Title != "已提交" || current.Revision != 1 {
		t.Fatal("query/clear replayed or altered music")
	}
}

func TestProjectRecoveryConcurrentBeginAndStaleClear(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	svc.dataDir = t.TempDir()
	p, _ := svc.CreateProject("race", 480)
	var count atomic.Int32
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			if svc.BeginProjectEdit(p.ID, project.NewID("op"), 0, "undo") == nil {
				count.Add(1)
			}
		}()
	}
	group.Wait()
	if count.Load() != 1 {
		t.Fatalf("got %d winners", count.Load())
	}
	original, err := svc.GetPendingProjectEdit()
	if err != nil || original == nil {
		t.Fatalf("missing winner: %v", err)
	}
	if info, err := os.Stat(filepath.Join(svc.dataDir, "pending-project-edit.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal permissions: %v %v", info, err)
	}
	if err = svc.ClearPendingProjectEdit(p.ID, original.OpID); err != nil {
		t.Fatal(err)
	}
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			if e := svc.ClearPendingProjectEdit(p.ID, original.OpID); e != nil {
				t.Error(e)
			}
		}()
	}
	group.Wait()
	if err = svc.BeginProjectEdit(p.ID, "new-op", 0, "redo"); err != nil {
		t.Fatal(err)
	}
	if err = svc.ClearPendingProjectEdit(p.ID, original.OpID); err == nil {
		t.Fatal("old clear removed new marker")
	}
	got, _ := svc.GetPendingProjectEdit()
	if got.OpID != "new-op" {
		t.Fatal("new marker changed")
	}
	if err = svc.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetPendingProjectEdit()
	if err != nil || got == nil {
		t.Fatal("project deletion erased unknown outcome")
	}
	if _, found, e := svc.GetProjectTransaction(p.ID, got.OpID); e != nil || found {
		t.Fatalf("deleted outcome: %v %v", found, e)
	}
	if err = svc.ClearPendingProjectEdit(p.ID, got.OpID); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRecoveryInvalidFilesArePreserved(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	svc.dataDir = t.TempDir()
	p, _ := svc.CreateProject("invalid", 480)
	path := filepath.Join(svc.dataDir, "pending-project-edit.json")
	for name, raw := range map[string]string{
		"truncated": "{", "empty": "", "missing fields": "{}", "future version": `{"version":2}`,
		"unknown field": `{"version":1,"replayOps":[]}`, "trailing data": `{} {}`, "oversize": string(bytes.Repeat([]byte{'x'}, 4097)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.GetPendingProjectEdit(); err == nil {
				t.Fatal("invalid record accepted")
			}
			if err := svc.BeginProjectEdit(p.ID, "op", 0, "edit"); err == nil {
				t.Fatal("invalid file overwritten")
			}
			if err := svc.ClearPendingProjectEdit(p.ID, "op"); err == nil {
				t.Fatal("invalid file cleared")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != raw {
				t.Fatal("invalid file not preserved")
			}
		})
	}
}

func TestProjectRecoveryMissingConfigAndFileSystemErrors(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	p, _ := svc.CreateProject("errors", 480)
	if _, err := svc.GetPendingProjectEdit(); err == nil {
		t.Fatal("empty data directory accepted")
	}
	file := filepath.Join(t.TempDir(), "not-directory")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.dataDir = file
	if err := svc.BeginProjectEdit(p.ID, "op", 0, "edit"); err == nil {
		t.Fatal("file parent accepted")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "keep" {
		t.Fatal("unrelated file altered")
	}
	svc.dataDir = t.TempDir()
	path := filepath.Join(svc.dataDir, "pending-project-edit.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetPendingProjectEdit(); err == nil {
		t.Fatal("directory accepted as record")
	}
	for _, kind := range []string{"", "external"} {
		if err := svc.BeginProjectEdit(p.ID, "op", 0, kind); err == nil {
			t.Fatal("unsupported kind accepted")
		}
	}
}
