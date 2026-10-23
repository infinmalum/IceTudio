package ipc

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func TestProjectGeneratedIDsOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	client := mustConnect(t, startServer(t, svc))
	defer client.Close()
	var p storage.Project
	if err := client.Call("projects.create", map[string]any{"title": "IPC 稳定身份", "ppq": 480}, &p); err != nil {
		t.Fatal(err)
	}
	// Blank currently encodes no tracks as null; removing the only track
	// encodes an empty array. Compare the same empty collection shape here.
	empty := p.State
	empty.Tracks = []project.Track{}
	states := []project.State{empty}
	results := []project.Result{}
	edit := func(ops []project.Op) project.Result {
		t.Helper()
		var result project.Result
		if err := client.Call("projects.edit", map[string]any{"projectId": p.ID, "opId": fmt.Sprintf("create-%d", len(results)), "baseRevision": len(results), "ops": ops}, &result); err != nil {
			t.Fatal(err)
		}
		var next storage.Project // fresh decode: saved snapshots must not share reused JSON slice backing arrays
		if err := client.Call("projects.get", map[string]any{"id": p.ID}, &next); err != nil {
			t.Fatal(err)
		}
		p = next
		states = append(states, p.State)
		results = append(results, result)
		return result
	}
	track := edit([]project.Op{{Kind: "track.add", Track: &project.TrackSpec{Name: "轨"}}}).CreatedIDs[0]
	clip := edit([]project.Op{{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: track, Length: 1920}}}).CreatedIDs[0]
	note := edit([]project.Op{{Kind: "note.add", ClipID: clip, Notes: []project.NewNote{{Duration: 480, Pitch: 60, Velocity: 100}}}}).CreatedIDs[0]
	event := edit([]project.Op{{Kind: "control.add", ClipID: clip, Events: []project.EventSpec{{Kind: project.KindCC, Controller: 64, Value: 127}}}}).CreatedIDs[0]
	pitch := 72
	edit([]project.Op{{Kind: "note.update", ClipID: clip, NoteDeltas: []project.NoteDelta{{ID: note, Pitch: &pitch}}}, {Kind: "control.remove", ClipID: clip, EventIDs: []string{event}}})
	for cycle := 0; cycle < 2; cycle++ {
		for _, undo := range []bool{true, false} {
			method := "projects.redo"
			if undo {
				method = "projects.undo"
			}
			for step := 0; step < len(results); step++ {
				var result project.Result
				if err := client.Call(method, map[string]any{"projectId": p.ID, "opId": fmt.Sprintf("%d-%t-%d", cycle, undo, step), "baseRevision": p.Revision}, &result); err != nil {
					t.Fatal(err)
				}
				var next storage.Project
				if err := client.Call("projects.get", map[string]any{"id": p.ID}, &next); err != nil {
					t.Fatal(err)
				}
				p = next
				index := step + 1
				if undo {
					index = len(results) - step - 1
				}
				if !reflect.DeepEqual(p.State, states[index]) {
					t.Fatalf("%s step %d changed state/identity", method, step)
				}
				if !undo && !reflect.DeepEqual(result.CreatedIDs, results[step].CreatedIDs) {
					t.Fatalf("redo changed result IDs: %+v", result)
				}
			}
		}
	}
	var lookup struct {
		Found  bool           `json:"found"`
		Result project.Result `json:"result"`
	}
	if err := client.Call("projects.transaction", map[string]any{"projectId": p.ID, "opId": "create-2"}, &lookup); err != nil {
		t.Fatal(err)
	}
	if !lookup.Found || !reflect.DeepEqual(lookup.Result, results[2]) {
		t.Fatal("original result changed after undo/redo")
	}
	// Deleting a parent retires all of its identities. Ordinary edits cannot
	// masquerade as restoration by spoofing source, even across object types.
	var removed project.Result
	if err := client.Call("projects.edit", map[string]any{"projectId": p.ID, "opId": "remove-parent", "baseRevision": p.Revision, "ops": []project.Op{{Kind: "track.remove", TrackID: track}}}, &removed); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{track, clip, note, event} {
		opID := "reuse-" + id
		if err := client.Call("projects.edit", map[string]any{"projectId": p.ID, "opId": opID, "source": "undo", "baseRevision": removed.Revision, "ops": []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: id}}}}, nil); err == nil {
			t.Fatalf("IPC reused %s", id)
		}
		var unchanged storage.Project
		if err := client.Call("projects.get", map[string]any{"id": p.ID}, &unchanged); err != nil {
			t.Fatal(err)
		}
		if unchanged.Revision != removed.Revision || len(unchanged.State.Tracks) != 0 {
			t.Fatal("rejection changed deleted state")
		}
		if err := client.Call("projects.transaction", map[string]any{"projectId": p.ID, "opId": opID}, &lookup); err != nil || lookup.Found {
			t.Fatalf("rejection published result: %v", err)
		}
	}
	if err := client.Call("projects.undo", map[string]any{"projectId": p.ID, "opId": "restore-parent", "baseRevision": removed.Revision}, nil); err != nil {
		t.Fatal(err)
	}
	var restored storage.Project
	if err := client.Call("projects.get", map[string]any{"id": p.ID}, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.State, p.State) {
		t.Fatal("IPC restoration lost nested identities")
	}
}

// TestProjectsOverIPC verifies the CLI surface drives the same project
// service: create, edit with opID, replay returning the original result,
// undo, and result lookup — all over the wire.
func TestProjectsOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)
	client := mustConnect(t, dataDir)
	defer client.Close()

	var created struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		PPQ      int    `json:"ppq"`
		Revision int64  `json:"revision"`
	}
	if err := client.Call("projects.create", map[string]any{"title": "IPC 工程", "ppq": 480}, &created); err != nil {
		t.Fatal(err)
	}
	if created.PPQ != 480 || created.Revision != 0 {
		t.Fatalf("created: %+v", created)
	}

	var trackResult struct {
		Revision  int64    `json:"revision"`
		CreatedID []string `json:"createdIds"`
	}
	edit := map[string]any{
		"projectId":    created.ID,
		"opId":         "ipc-op-1",
		"baseRevision": 0,
		"source":       "cli",
		"ops":          []map[string]any{{"kind": "track.add", "track": map[string]any{"name": "轨", "channel": 0}}},
	}
	if err := client.Call("projects.edit", edit, &trackResult); err != nil {
		t.Fatal(err)
	}
	if trackResult.Revision != 1 || len(trackResult.CreatedID) != 1 {
		t.Fatalf("edit result: %+v", trackResult)
	}

	// replay: same opID, original result, no duplicate track
	if err := client.Call("projects.edit", edit, &trackResult); err != nil {
		t.Fatal(err)
	}
	if trackResult.Revision != 1 {
		t.Fatalf("replay revision: %+v", trackResult)
	}

	var undoResult struct {
		Revision int64 `json:"revision"`
	}
	if err := client.Call("projects.undo", map[string]any{
		"projectId": created.ID, "opId": "ipc-undo-1", "baseRevision": 1,
	}, &undoResult); err != nil {
		t.Fatal(err)
	}
	if undoResult.Revision != 2 {
		t.Fatalf("undo revision: %+v", undoResult)
	}

	var lookup struct {
		Found  bool            `json:"found"`
		Result json.RawMessage `json:"result"`
	}
	if err := client.Call("projects.transaction", map[string]any{"projectId": created.ID, "opId": "ipc-op-1"}, &lookup); err != nil {
		t.Fatal(err)
	}
	if !lookup.Found {
		t.Fatal("committed opID must be found")
	}

	// export round-trips through the host
	out := filepath.Join(t.TempDir(), "ipc.mid")
	if err := client.Call("projects.export", map[string]any{"projectId": created.ID, "path": out}, nil); err != nil {
		t.Fatal(err)
	}
	var imported struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := client.Call("projects.import", map[string]any{"path": out}, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Title != "IPC 工程" {
		t.Fatalf("imported title: %+v", imported)
	}

	// scoped read: rebuild content (batch edits), then a track+tick window
	// must return fewer notes than the full read.
	var trackAdd struct {
		Revision  int64    `json:"revision"`
		CreatedID []string `json:"createdIds"`
	}
	if err := client.Call("projects.edit", map[string]any{
		"projectId": created.ID, "opId": "ipc-op-2", "baseRevision": 2, "source": "cli",
		"ops": []map[string]any{{"kind": "track.add", "track": map[string]any{"name": "轨2", "channel": 0}}},
	}, &trackAdd); err != nil {
		t.Fatal(err)
	}
	trackID := trackAdd.CreatedID[0]
	var clipAdd struct {
		Revision  int64    `json:"revision"`
		CreatedID []string `json:"createdIds"`
	}
	if err := client.Call("projects.edit", map[string]any{
		"projectId": created.ID, "opId": "ipc-op-3", "baseRevision": trackAdd.Revision, "source": "cli",
		"ops": []map[string]any{{"kind": "clip.add", "clip": map[string]any{"trackId": trackID, "name": "c", "start": 0, "length": 3840}}},
	}, &clipAdd); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("projects.edit", map[string]any{
		"projectId": created.ID, "opId": "ipc-op-4", "baseRevision": clipAdd.Revision, "source": "cli",
		"ops": []map[string]any{{"kind": "note.add", "clipId": clipAdd.CreatedID[0], "notes": []map[string]any{
			{"start": 0, "duration": 480, "pitch": 60, "velocity": 100},
			{"start": 960, "duration": 480, "pitch": 62, "velocity": 100},
			{"start": 2400, "duration": 480, "pitch": 64, "velocity": 100},
		}}},
	}, new(struct {
		Revision int64 `json:"revision"`
	})); err != nil {
		t.Fatal(err)
	}
	var full, scoped struct {
		State struct {
			Tracks []struct {
				Clips []struct {
					Notes []map[string]any `json:"notes"`
				} `json:"clips"`
			} `json:"tracks"`
		} `json:"state"`
	}
	if err := client.Call("projects.query", map[string]any{"projectId": created.ID}, &full); err != nil {
		t.Fatal(err)
	}
	if len(full.State.Tracks) != 1 || len(full.State.Tracks[0].Clips[0].Notes) != 3 {
		t.Fatalf("full query: %+v", full.State.Tracks)
	}
	if err := client.Call("projects.query", map[string]any{
		"projectId": created.ID, "trackId": trackID, "startTick": 480, "endTick": 1920,
	}, &scoped); err != nil {
		t.Fatal(err)
	}
	notes := scoped.State.Tracks[0].Clips[0].Notes
	if len(notes) != 1 || notes[0]["pitch"].(float64) != 62 {
		t.Fatalf("scoped query must keep only the overlapping note: %+v", notes)
	}
}

func TestProjectRenameOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, &chatScript{}, &blockingTools{})
	dataDir := startServer(t, svc)
	client := mustConnect(t, dataDir)
	defer client.Close()
	var p storage.Project
	if err := client.Call("projects.create", map[string]any{"title": "IPC 原名", "ppq": 480}, &p); err != nil {
		t.Fatal(err)
	}
	for i, step := range []struct{ method, title string }{
		{"edit", "IPC 新名"}, {"undo", "IPC 原名"}, {"redo", "IPC 新名"},
	} {
		params := map[string]any{"projectId": p.ID, "opId": "title-" + step.method, "baseRevision": i}
		if step.method == "edit" {
			params["ops"] = []map[string]any{{"kind": "project.rename", "title": "中间名"}, {"kind": "project.rename", "title": step.title}}
		}
		if err := client.Call("projects."+step.method, params, nil); err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"projects.get", "projects.query"} {
			var got storage.Project
			key := "id"
			if method == "projects.query" {
				key = "projectId"
			}
			if err := client.Call(method, map[string]any{key: p.ID}, &got); err != nil {
				t.Fatal(err)
			}
			if got.Title != step.title || got.State.Title != step.title || got.Revision != int64(i+1) {
				t.Fatalf("%s after %s: title=%q state=%q revision=%d", method, step.method, got.Title, got.State.Title, got.Revision)
			}
		}
		var items []storage.ProjectSummary
		if err := client.Call("projects.list", nil, &items); err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].Title != step.title || items[0].Revision != int64(i+1) {
			t.Fatalf("list after %s: %+v", step.method, items)
		}
	}
	path := filepath.Join(t.TempDir(), "renamed.mid")
	if err := client.Call("projects.export", map[string]any{"projectId": p.ID, "path": path}, nil); err != nil {
		t.Fatal(err)
	}
	var imported storage.Project
	if err := client.Call("projects.import", map[string]any{"path": path}, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Title != "IPC 新名" || imported.State.Title != imported.Title {
		t.Fatalf("MIDI did not keep renamed title: %+v", imported)
	}
}
