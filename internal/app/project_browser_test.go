package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"icetudio/internal/storage"
)

// Opt-in Chromium integration: actual React editor -> bridge-shaped test
// transport -> actual app service and SQLite transactions. This is not a
// native Wails or audio test. No model, credentials or external service is used.
func TestMusicEditorBrowser(t *testing.T) {
	if os.Getenv("ICETUDIO_MUSIC_BROWSER") != "1" {
		t.Skip("set ICETUDIO_MUSIC_BROWSER=1 for real Chromium/service integration")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".cache/browser/music")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Each run is isolated, without disturbing earlier test artifacts or app data.
	runDir, err := os.MkdirTemp(dir, "run-")
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(runDir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := New(Options{Store: store, DataDir: runDir})
	tokenBytes := make([]byte, 24)
	if _, err = rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(tokenBytes)
	methods := map[string]string{"GetPendingProjectEdit": "GetPendingProjectEdit", "BeginProjectEdit": "BeginProjectEdit", "ClearPendingProjectEdit": "ClearPendingProjectEdit", "ListProjects": "ListProjects", "GetProject": "GetProject", "CreateProject": "CreateProject", "DeleteProject": "DeleteProject", "SubmitProjectOps": "SubmitProjectOps", "UndoProject": "UndoProject", "RedoProject": "RedoProject", "GetProjectTransaction": "GetProjectTransaction", "ProjectHistory": "ProjectHistory"}
	for _, name := range []string{"GetTimeMapDraft", "SaveTimeMapDraft", "ClearTimeMapDraft"} {
		methods[name] = name
	}
	var mu sync.Mutex
	listeners := map[chan Event]bool{}
	off := svc.Subscribe(func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		for ch := range listeners {
			select {
			case ch <- e:
			default:
			}
		}
	})
	defer off()
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9393")
		w.Header().Set("Content-Type", "text/event-stream")
		ch := make(chan Event, 32)
		mu.Lock()
		listeners[ch] = true
		mu.Unlock()
		defer func() { mu.Lock(); delete(listeners, ch); mu.Unlock() }()
		fmt.Fprint(w, ": ready\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-ch:
				b, _ := json.Marshal(e)
				fmt.Fprintf(w, "data: %s\n\n", b)
				w.(http.Flusher).Flush()
			}
		}
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:9393")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-Test-Token")
		if r.Method == http.MethodOptions {
			return
		}
		if r.Header.Get("X-Test-Token") != token {
			http.Error(w, "forbidden", 403)
			return
		}
		var req struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		name, ok := methods[req.Method]
		if !ok {
			http.Error(w, "unknown test method", 400)
			return
		}
		method := reflect.ValueOf(svc).MethodByName(name)
		typ := method.Type()
		if len(req.Args) != typ.NumIn() {
			http.Error(w, "argument count", 400)
			return
		}
		args := make([]reflect.Value, typ.NumIn())
		for i := range args {
			v := reflect.New(typ.In(i))
			if err := json.Unmarshal(req.Args[i], v.Interface()); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			args[i] = v.Elem()
		}
		out := method.Call(args)
		if last := out[len(out)-1]; !last.IsNil() {
			http.Error(w, last.Interface().(error).Error(), 409)
			return
		}
		var value any
		if len(out) == 2 {
			value = out[0].Interface()
		} else if len(out) > 2 {
			values := []any{}
			for _, v := range out[:len(out)-1] {
				values = append(values, v.Interface())
			}
			value = values
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	cmd := exec.Command("node", filepath.Join(root, "frontend/e2e/music.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "ICETUDIO_TEST_URL="+server.URL, "ICETUDIO_TEST_TOKEN="+token, "ICETUDIO_TEST_RUN="+runDir)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	// Assert browser edits really reached persistent state and retained non-note data.
	items, err := svc.ListProjects()
	if err != nil || len(items) == 0 {
		t.Fatalf("projects: %v %v", items, err)
	}
	p, err := svc.GetProject(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Revision < 5 {
		t.Fatalf("editor did not commit real transactions: %d", p.Revision)
	}
}
