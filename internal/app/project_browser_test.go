package app

import (
	"context"
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
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// Opt-in Chromium integration: actual React editor -> bridge-shaped test
// transport -> actual app service and SQLite transactions. This is not a
// native Wails or audio test. No remote model, credentials or external service
// is used; candidate runs use the script model below.
func TestMusicEditorBrowser(t *testing.T) {
	if os.Getenv("ICETUDIO_MUSIC_BROWSER") != "1" {
		t.Skip("set ICETUDIO_MUSIC_BROWSER=1 for real Chromium/service integration")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if retained := os.Getenv("ICETUDIO_MUSIC_ARTIFACT_DIR"); retained != "" {
		dir = retained
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
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
	runs := agent.NewRuns(store)
	defer runs.Close()
	catalog, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := config.DefaultModel("lmstudio")
	source.Name = "offline-browser-script"
	svc := New(Options{Store: store, DataDir: runDir, Runs: runs, Tools: emptyTools{}, Skills: catalog, Chat: browserPreviewChat{}, Config: config.Config{Model: source}})
	defer func() { runs.Close(); svc.WaitIdle() }()
	tokenBytes := make([]byte, 24)
	if _, err = rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(tokenBytes)
	methods := map[string]string{"GetPendingProjectEdit": "GetPendingProjectEdit", "BeginProjectEdit": "BeginProjectEdit", "ClearPendingProjectEdit": "ClearPendingProjectEdit", "ListProjects": "ListProjects", "GetProject": "GetProject", "CreateProject": "CreateProject", "DeleteProject": "DeleteProject", "SubmitProjectOps": "SubmitProjectOps", "UndoProject": "UndoProject", "RedoProject": "RedoProject", "GetProjectTransaction": "GetProjectTransaction", "ProjectHistory": "ProjectHistory"}
	for _, name := range []string{"GetTimeMapDraft", "SaveTimeMapDraft", "ClearTimeMapDraft"} {
		methods[name] = name
	}
	for _, name := range []string{"PreviewProjectEdit", "ApproveProjectEditPreview", "DenyToolCall"} {
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
		if req.Method == "SeedProjectPreview" {
			var projectID, clipID string
			if len(req.Args) != 2 || json.Unmarshal(req.Args[0], &projectID) != nil || json.Unmarshal(req.Args[1], &clipID) != nil {
				http.Error(w, "invalid seed args", 400)
				return
			}
			conv, err := svc.CreateConversation(storage.DefaultConfigID, "", "浏览器候选")
			if err == nil {
				err = svc.SetConversationProject(conv.ID, projectID)
			}
			input, _ := json.Marshal(map[string]string{"projectId": projectID, "clipId": clipID})
			if err == nil {
				_, err = svc.StartConversationTask(conv.ID, string(input), &ProjectScopeSelection{Clips: []string{clipID}})
			}
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				calls, e := svc.ListToolCalls(conv.ID)
				if e != nil {
					http.Error(w, e.Error(), 409)
					return
				}
				for _, call := range calls {
					if call.ToolName == "project__transform" && call.State == storage.CallPending {
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(call)
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			http.Error(w, "candidate did not reach approval", 500)
			return
		}
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

// Script model only; the browser still uses the real run loop, frozen scope,
// query/transform tools, approvals, recorder and SQLite transaction service.
type browserPreviewChat struct{}

func (browserPreviewChat) Chat(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	last := messages[len(messages)-1]
	if last.Role == "user" {
		var a struct {
			ProjectID string `json:"projectId"`
		}
		if err := json.Unmarshal([]byte(last.Content), &a); err != nil {
			return model.ChatResponse{}, err
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall("preview-query", "project__query", fmt.Sprintf(`{"projectId":%q}`, a.ProjectID))}}}, nil
	}
	if last.Role == "tool" && last.ToolCallID == "preview-query" {
		var q struct {
			ProjectID string        `json:"projectId"`
			Revision  int64         `json:"revision"`
			State     project.State `json:"state"`
		}
		if err := json.Unmarshal([]byte(last.Content), &q); err != nil {
			return model.ChatResponse{}, err
		}
		if len(q.State.Tracks) == 0 || len(q.State.Tracks[0].Clips) == 0 {
			return model.ChatResponse{}, fmt.Errorf("no clip in frozen scope")
		}
		raw := transformPayload(q.ProjectID, q.Revision, project.NewID("op"), fmt.Sprintf(`{"clipIds":[%q]}`, q.State.Tracks[0].Clips[0].ID), `{"transpose":{"semitones":1}}`, false)
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall("preview-transform", "project__transform", raw)}}}, nil
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "按实际工具结果完成，不重放。"}}, nil
}
