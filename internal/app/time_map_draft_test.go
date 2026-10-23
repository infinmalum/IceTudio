package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func mapDraftPayload(t *testing.T, id string) string {
	t.Helper()
	s := timeMapSnapshot{Version: 1, ProjectID: id, Title: "草稿工程", PPQ: 480, BaseRevision: 0,
		Tempo: []project.TempoPoint{{Tick: 0, USPerQuarter: 500001}}, Meter: []project.MeterPoint{{Tick: 0, Numerator: 4, Denominator: 4}}}
	s.Draft.Tempo = []timeMapTempoRow{{Key: 0, Tick: "0", BPM: "", OriginalBPM: "119.99976000048", OriginalUS: 500001}, {Key: 9, Tick: "0", BPM: "bad", OriginalBPM: "120", OriginalUS: 500000}}
	s.Draft.Meter = []timeMapMeterRow{{Key: 0, Tick: "0", Numerator: "4", Denominator: "4"}, {Key: 12, Tick: "", Numerator: "", Denominator: "8"}}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTimeMapDraftPersistenceCASAndBarrier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "music.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Store: store, DataDir: dir})
	p, err := svc.CreateProject("原工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	payload := mapDraftPayload(t, p.ID)
	if r, err := svc.GetTimeMapDraft(); err != nil || r != nil {
		t.Fatalf("initial record: %+v %v", r, err)
	}
	if err := svc.SaveTimeMapDraft("", "save-a", payload); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTimeMapDraft("", "save-a", payload); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTimeMapDraft("", "save-b", payload); err == nil {
		t.Fatal("stale save accepted")
	}
	if err := svc.SaveTimeMapDraft("save-a", "save-a", strings.Replace(payload, "草稿工程", "改名", 1)); err == nil {
		t.Fatal("changed intent token accepted")
	}
	other, err := svc.CreateProject("另一工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTimeMapDraft("save-a", "other", mapDraftPayload(t, other.ID)); err == nil {
		t.Fatal("other project overwrote draft")
	}
	want, _ := svc.GetTimeMapDraft()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc = New(Options{Store: store, DataDir: dir})
	got, err := svc.GetTimeMapDraft()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if got.Payload != payload {
		t.Fatal("empty/invalid strings or original tempo precision changed")
	}
	current, _ := svc.GetProject(p.ID)
	if !reflect.DeepEqual(current, p) {
		t.Fatal("draft wrote music")
	}
	if err := svc.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetTimeMapDraft(); err != nil || got.Payload != payload {
		t.Fatalf("deleted project lost draft: %+v %v", got, err)
	}
	if err := svc.ClearTimeMapDraft("wrong", "clear-b"); err == nil {
		t.Fatal("stale clear accepted")
	}
	if err := svc.ClearTimeMapDraft("save-a", "clear-b"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearTimeMapDraft("save-a", "clear-b"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTimeMapDraft("", "late-initial", payload); err == nil {
		t.Fatal("late initial save resurrected discarded draft")
	}
	if err := svc.SaveTimeMapDraft("save-a", "late-update", payload); err == nil {
		t.Fatal("late update resurrected discarded draft")
	}
	if got, err := svc.GetTimeMapDraft(); err != nil || got.Token != "clear-b" || got.Payload != "" {
		t.Fatalf("clear barrier: %+v %v", got, err)
	}
	if err := svc.SaveTimeMapDraft("clear-b", "new-project", mapDraftPayload(t, other.ID)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearTimeMapDraft("save-a", "old-clear"); err == nil {
		t.Fatal("late clear removed new draft")
	}
	if info, err := os.Stat(filepath.Join(dir, "time-map-draft.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}

func TestTimeMapDraftConcurrentCAS(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	svc.dataDir = t.TempDir()
	p, _ := svc.CreateProject("并发", 480)
	payload := mapDraftPayload(t, p.ID)
	var wg sync.WaitGroup
	var winners atomic.Int32
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if svc.SaveTimeMapDraft("", project.NewID("checkpoint"), payload) == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d CAS winners", winners.Load())
	}
	r, err := svc.GetTimeMapDraft()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearTimeMapDraft(r.Token, "cleared"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTimeMapDraft("cleared", "new", payload); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearTimeMapDraft(r.Token, "stale"); err == nil {
		t.Fatal("old clear removed current record")
	}
	failed := New(Options{Store: svc.store, DataDir: svc.dataDir, InitError: errors.New("host lock unavailable")})
	if _, err := failed.GetTimeMapDraft(); err == nil {
		t.Fatal("failed host read draft")
	}
	if err := failed.SaveTimeMapDraft("new", "failed", payload); err == nil {
		t.Fatal("failed host wrote draft")
	}
	if err := failed.ClearTimeMapDraft("new", "failed"); err == nil {
		t.Fatal("failed host cleared draft")
	}
}

func TestTimeMapDraftInvalidDataIsPreserved(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	svc.dataDir = t.TempDir()
	p, _ := svc.CreateProject("无效", 480)
	payload := mapDraftPayload(t, p.ID)
	path := filepath.Join(svc.dataDir, "time-map-draft.json")
	for name, raw := range map[string]string{
		"truncated": "{", "unknown": `{"version":1,"token":"x","payload":"","ops":[]}`,
		"future": `{"version":2,"token":"x","payload":""}`, "bad payload": `{"version":1,"token":"x","payload":"{}"}`,
		"trailing": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.GetTimeMapDraft(); err == nil {
				t.Fatal("invalid record accepted")
			}
			if err := svc.SaveTimeMapDraft("", "save", payload); err == nil {
				t.Fatal("invalid record overwritten")
			}
			if err := svc.ClearTimeMapDraft("x", "clear"); err == nil {
				t.Fatal("invalid record cleared")
			}
			got, _ := os.ReadFile(path)
			if string(got) != raw {
				t.Fatal("invalid file modified")
			}
		})
	}
	// Use a separate directory rather than deleting the invalid record.
	svc.dataDir = t.TempDir()
	if err := svc.SaveTimeMapDraft("", "valid", payload); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"oversize": strings.Repeat("x", maxTimeMapPayload+1), "bad base": strings.Replace(payload, `"usPerQuarter":500001`, `"usPerQuarter":0`, 1),
		"duplicate key": strings.Replace(payload, `"key":9`, `"key":0`, 1), "future payload": strings.Replace(payload, `"version":1`, `"version":2`, 1),
		"long string": strings.Replace(payload, `"bpm":""`, `"bpm":"`+strings.Repeat("x", 129)+`"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := svc.SaveTimeMapDraft("valid", "bad", bad); err == nil {
				t.Fatal("invalid payload accepted")
			}
			got, err := svc.GetTimeMapDraft()
			if err != nil || got.Token != "valid" || got.Payload != payload {
				t.Fatalf("valid draft lost: %+v %v", got, err)
			}
		})
	}
}

func TestTimeMapDraftFileErrors(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	p, _ := svc.CreateProject("路径", 480)
	payload := mapDraftPayload(t, p.ID)
	if _, err := svc.GetTimeMapDraft(); err == nil {
		t.Fatal("unconfigured data dir accepted")
	}
	file := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.dataDir = file
	if err := svc.SaveTimeMapDraft("", "x", payload); err == nil {
		t.Fatal("file parent accepted")
	}
	svc.dataDir = t.TempDir()
	path := filepath.Join(svc.dataDir, "time-map-draft.json")
	if err := os.Symlink(file, path); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetTimeMapDraft(); err == nil {
		t.Fatal("symlink followed")
	}
	if err := svc.SaveTimeMapDraft("", "x", payload); err == nil {
		t.Fatal("symlink overwritten")
	}
	got, _ := os.ReadFile(file)
	if string(got) != "keep" {
		t.Fatal("unrelated file modified")
	}
	svc.dataDir = t.TempDir()
	if err := os.Mkdir(filepath.Join(svc.dataDir, "time-map-draft.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetTimeMapDraft(); err == nil {
		t.Fatal("directory accepted")
	}
	svc.dataDir = t.TempDir()
	path = filepath.Join(svc.dataDir, "time-map-draft.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxTimeMapFile + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	if _, err := svc.GetTimeMapDraft(); err == nil {
		t.Fatal("oversize file accepted")
	}
	if err := svc.SaveTimeMapDraft("", "save", payload); err == nil {
		t.Fatal("oversize file overwritten")
	}
	if err := svc.ClearTimeMapDraft("old", "clear"); err == nil {
		t.Fatal("oversize file cleared")
	}
	if info, err := os.Stat(path); err != nil || info.Size() != maxTimeMapFile+1 {
		t.Fatalf("oversize file changed: %v %v", info, err)
	}
}
