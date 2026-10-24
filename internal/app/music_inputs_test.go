package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"icetudio/internal/storage"
)

func inputsPayload(entries ...map[string]any) string {
	raw, err := json.Marshal(map[string]any{"version": 1, "entries": entries})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestMusicInputsPersistenceCASAndClear(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Store: store, DataDir: dir})
	entry := map[string]any{"key": `["p1","t1","c1","片段长度（tick）"]`, "raw": "", "label": "片段长度（tick）", "title": "工程", "target": map[string]any{"id": "p1", "revision": 3}, "trackID": "t1", "clipID": "c1"}
	if r, err := svc.GetMusicInputs(); err != nil || r != nil {
		t.Fatalf("initial record: %+v %v", r, err)
	}
	if err := svc.SaveMusicInputs("", "save-a", inputsPayload(entry)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveMusicInputs("save-a", "save-a", inputsPayload(entry)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveMusicInputs("", "save-b", inputsPayload(entry)); err == nil {
		t.Fatal("stale token accepted")
	}
	record, err := svc.GetMusicInputs()
	if err != nil || record.Token != "save-a" {
		t.Fatalf("record: %+v %v", record, err)
	}
	if err := svc.ClearMusicInputs("save-a", "clear-a"); err != nil {
		t.Fatal(err)
	}
	if r, err := svc.GetMusicInputs(); err != nil || r != nil {
		t.Fatalf("cleared record: %+v %v", r, err)
	}
	if err := svc.ClearMusicInputs("save-a", "clear-b"); err != nil {
		t.Fatalf("clear of absent record: %v", err)
	}
	// A delayed save from before the clear must not resurrect silently.
	if err := svc.SaveMusicInputs("save-a", "save-late", inputsPayload(entry)); err == nil {
		t.Fatal("delayed save after clear accepted")
	}
}

func TestMusicInputsValidationAndBounds(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Store: store, DataDir: dir})
	if err := svc.SaveMusicInputs("", "t1", ""); err == nil {
		t.Fatal("empty payload accepted")
	}
	if err := svc.SaveMusicInputs("", "t1", `{"version":2,"entries":[]}`); err == nil {
		t.Fatal("unknown version accepted")
	}
	if err := svc.SaveMusicInputs("", "t1", `{"version":1,"entries":[{"key":"","raw":"","label":"","title":"","target":{"id":"p","revision":0}}]}`); err == nil {
		t.Fatal("empty key accepted")
	}
	if err := svc.SaveMusicInputs("", "t1", inputsPayload(make([]map[string]any, 257)...)); err == nil {
		t.Fatal("oversize entries accepted")
	}
	if err := svc.SaveMusicInputs("", strings.Repeat("x", 129), inputsPayload()); err == nil {
		t.Fatal("invalid token accepted")
	}
	if err := svc.SaveMusicInputs("", "t1", inputsPayload()); err != nil {
		t.Fatal(err)
	}
	// Reload keeps the record; corrupted files are preserved, not deleted.
	if _, err := svc.GetMusicInputs(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "music-inputs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetMusicInputs(); err == nil {
		t.Fatal("corrupt record accepted")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("corrupt record file deleted")
	}
	if err := svc.SaveMusicInputs("", "t2", inputsPayload()); err == nil {
		t.Fatal("save over unreadable record accepted")
	}
}

func TestMusicInputsConcurrentCAS(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Store: store, DataDir: dir})
	if err := svc.SaveMusicInputs("", "base", inputsPayload()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	accepted := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := "writer-" + strings.Repeat(string(rune('a'+i)), 2)
			if err := svc.SaveMusicInputs("base", token, inputsPayload()); err == nil {
				accepted <- token
			}
		}(i)
	}
	wg.Wait()
	close(accepted)
	if len(accepted) != 1 {
		t.Fatalf("concurrent writers accepted: %d", len(accepted))
	}
	var winner string
	for token := range accepted {
		winner = token
	}
	record, err := svc.GetMusicInputs()
	if err != nil || record.Token != winner {
		t.Fatalf("winner not persisted: %+v %v", record, err)
	}
}
