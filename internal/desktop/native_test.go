package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"icetudio/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// Explicit opt-in: starts an actual Native/WebKit window using only a fresh
// disposable data directory. The binary must be built before running it.
func TestMyGoNativeReadOnlyProject(t *testing.T) { verifyNativeProject(t, false) }
func TestMyGoNativeEditRecovery(t *testing.T)    { verifyNativeProject(t, true) }

func verifyNativeProject(t *testing.T, edit bool) {
	if os.Getenv("ICETUDIO_MYGO_NATIVE") != "1" {
		t.Skip("requires actual desktop and built bin/icetudio-mygo")
	}
	binary, err := filepath.Abs("../../bin/icetudio-mygo")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "icetudio.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := project.Blank("真实工程只读验证", 480)
	state.Tracks = []project.Track{{ID: "t_native", Name: "Native Track", Clips: []project.Clip{
		{ID: "c_first", Name: "First", Length: 1920, Notes: []project.Note{{ID: "n_first", Duration: 240, Pitch: 60, Velocity: 90}}},
		{ID: "c_second", Name: "Second", Start: 1920, Length: 1920, Notes: []project.Note{{ID: "n_second", Duration: 240, Pitch: 64, Velocity: 90}, {ID: "n_third", Start: 480, Duration: 240, Pitch: 67, Velocity: 80}, {ID: "n_fourth", Start: 960, Duration: 240, Pitch: 72, Velocity: 70}}, Controls: []project.ControlEvent{{ID: "e_pedal", Kind: project.KindCC, Controller: 64, Value: 127}}},
	}}}
	before, err := store.CreateProjectFromState(context.Background(), state)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}

	svc := app.New(app.Options{Store: store, DataDir: dir})
	conversation, err := svc.CreateConversation("", "", "中文历史验收")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.AddMessage(context.Background(), conversation.ID, "", storage.RoleUser, "请保留工程与对话各自的历史。"); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.AddMessage(context.Background(), conversation.ID, "", storage.RoleAssistant, "### 中文 Native 消息\n\n| 轨道 | 操作 |\n| --- | --- |\n| 钢琴 | 保留 |\n\n```go\nservice.GetProject(id)\n```\n\n![不自动下载图片](https://example.invalid/private.png)"); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	if edit {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == "GET" {
				fmt.Fprint(w, `{"data":[{"id":"native-fixture"}]}`)
				return
			}
			var req struct {
				Stream   bool `json:"stream"`
				Messages []struct {
					Role string `json:"role"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid fixture request", 400)
				return
			}
			toolReply := false
			for _, m := range req.Messages {
				if m.Role == "tool" {
					toolReply = true
				}
			}
			message := map[string]any{"role": "assistant", "content": "已通过本地测试模型完成修改"}
			finish := "stop"
			if !toolReply {
				args := fmt.Sprintf(`{"projectId":%q,"opId":"native-agent-op","baseRevision":%d,"ops":[{"kind":"note.update","clipId":"c_second","noteDeltas":[{"id":"n_second","velocity":99}]}]}`, before.ID, before.Revision)
				message["content"] = ""
				message["tool_calls"] = []any{map[string]any{"id": "native-tool-call", "index": 0, "type": "function", "function": map[string]any{"name": "project__edit", "arguments": args}}}
				finish = "tool_calls"
			}
			if req.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				chunk, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
			}
		}))
		defer server.Close()
		source := config.DefaultModel("lmstudio")
		source.BaseURL = server.URL + "/v1"
		source.Name = "native-fixture"
		if err := config.Save(filepath.Join(dir, "config.json"), config.Config{Model: source}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ICETUDIO_MYGO_LOCAL_MODEL_FIXTURE", "1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	evidence := filepath.Join(dir, "evidence")
	args := []string{"--data", dir, "--smoke-dir", evidence}
	if edit {
		args = append(args, "--smoke-edit")
	}
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("native probe: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(evidence, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Initial           pageSnapshot `json:"initial"`
		After             pageSnapshot `json:"afterNativeSelection"`
		Mounted           int          `json:"mountedNotes"`
		EditVerified      bool         `json:"editFlowVerified"`
		PanelsVerified    bool         `json:"nativePanelsVerified"`
		AgentEditVerified bool         `json:"nativeAgentEditVerified"`
		ComposerVerified  bool         `json:"nativeComposerVerified"`
		HistoryVerified   bool         `json:"nativeHistoryReadVerified"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Initial.Clip != "c_first" || result.After.Clip != "c_second" || result.After.Notes != "3" || result.Mounted != 3 || result.Initial.Generation == result.After.Generation {
		t.Fatalf("Native -> WebView context mismatch: %s", data)
	}
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.GetProject(context.Background(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if edit {
		if !result.EditVerified || !result.HistoryVerified || !result.ComposerVerified || !result.PanelsVerified || !result.AgentEditVerified || !reflect.DeepEqual(before.State, after.State) || after.Revision != before.Revision+8 {
			t.Fatalf("edit/undo/recovery mismatch: revision %d -> %d, result %s", before.Revision, after.Revision, data)
		}
	} else if !reflect.DeepEqual(before, after) {
		t.Fatal("read-only Native probe altered project, events or revision")
	}
	if _, err = os.Stat(filepath.Join(dir, "pending-project-edit.json")); !os.IsNotExist(err) {
		t.Fatalf("read-only probe created edit recovery marker: %v", err)
	}
	if edit && os.Getenv("ICETUDIO_MYGO_EVIDENCE_DIR") != "" {
		dest := os.Getenv("ICETUDIO_MYGO_EVIDENCE_DIR")
		if !filepath.IsAbs(dest) {
			t.Fatal("evidence destination must be absolute")
		}
		if err := os.MkdirAll(dest, 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"native.png", "piano.png", "native-properties.png", "agent-compose.png", "native-settings.png", "native-approval.png", "native-64-tracks.png", "dense.json", "result.json"} {
			b, err := os.ReadFile(filepath.Join(evidence, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if edit {
		t.Log("actual MyGo/WebKit: Native project creation/properties/time map/draft clear/MIDI roundtrip, velocity/undo, transform/ack, shared undo, lost ack recovery without replay, pending-close cancel, unchanged music including CC, local-model approval/ghost, 1k/10k offscreen edits and readonly bridge isolation verified")
	} else {
		t.Log("actual MyGo window: native selection, typed event, GridSound DOM replacement and unchanged SQLite project verified")
	}
}
