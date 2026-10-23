package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// TestProjectThreeEntrypointsShareOneState is the P1 acceptance core: the
// GUI-facing service methods, the CLI-bound (identical) service methods and
// the simulated agent tool path all drive one authoritative state.
func TestProjectThreeEntrypointsShareOneState(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, nil, nil)

	proj, err := svc.CreateProject("同一工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(ctx, storage.DefaultConfigID, "", "工程对话")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(conv.ID, proj.ID); err != nil {
		t.Fatal(err)
	}

	// entrypoint 1: service submit (GUI path)
	res1, err := svc.SubmitProjectOps(proj.ID, "gui-op", "gui", 0, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{Name: "轨1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// entrypoint 2: the CLI binds over IPC to this same service, so the
	// same call shape applies (wired in the ipc package)
	res2, err := svc.SubmitProjectOps(proj.ID, "cli-op", "cli", res1.Revision, []project.Op{
		{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: res1.CreatedIDs[0], Start: 0, Length: 480}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Revision != res1.Revision+1 {
		t.Fatalf("revision sequence: %d then %d", res1.Revision, res2.Revision)
	}

	// entrypoint 3: simulated agent via the tool path
	profile, err := store.ResolveRunConfig(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookkeeping := bookkeepingTools{inner: emptyTools{}, svc: svc, profile: profile, sessionID: conv.ID, runID: "test-run"}
	defs, err := bookkeeping.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, d := range defs {
		found[d.Function.Name] = true
	}
	if !found["project__query"] || !found["project__edit"] {
		t.Fatalf("bound conversation must offer project tools: %v", found)
	}

	queryRaw, err := bookkeeping.Call(ctx, "project__query", `{"projectId":"`+proj.ID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var query struct {
		Revision int64          `json:"revision"`
		State    map[string]any `json:"state"`
	}
	if err := json.Unmarshal([]byte(queryRaw), &query); err != nil {
		t.Fatal(err)
	}
	if query.Revision != res2.Revision {
		t.Fatalf("tool query revision %d, want %d", query.Revision, res2.Revision)
	}

	// the agent edits with the same command shape and lands in the same state
	editArgs := map[string]any{
		"projectId":    proj.ID,
		"opId":         "agent-op",
		"baseRevision": query.Revision,
		"ops":          []map[string]any{{"kind": "project.rename", "title": "Agent 改名"}},
	}
	raw, _ := json.Marshal(editArgs)
	if _, err := bookkeeping.Call(ctx, "project__edit", string(raw)); err != nil {
		t.Fatal(err)
	}
	final, err := svc.GetProject(proj.ID)
	if err != nil || final.Title != "Agent 改名" || final.State.Title != "Agent 改名" || final.Revision != query.Revision+1 {
		t.Fatalf("agent edit not in shared state: %+v %v", final.State, err)
	}

	// a stale revision from the tool is rejected, not rebased
	editArgs["opId"], editArgs["baseRevision"] = "agent-op-2", query.Revision
	raw, _ = json.Marshal(editArgs)
	if _, err := bookkeeping.Call(ctx, "project__edit", string(raw)); err == nil {
		t.Fatal("stale base revision must be rejected")
	}

	// opID replay through the tool returns the original result
	editArgs["opId"], editArgs["baseRevision"] = "agent-op", query.Revision
	raw, _ = json.Marshal(editArgs)
	replay, err := bookkeeping.Call(ctx, "project__edit", string(raw))
	if err != nil {
		t.Fatalf("replay must succeed: %v", err)
	}
	var replayRes struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(replay), &replayRes); err != nil || replayRes.Revision != query.Revision+1 {
		t.Fatalf("replay result: %s %v", replay, err)
	}
	current, _ := svc.GetProject(proj.ID)
	if current.Revision != query.Revision+1 {
		t.Fatal("replay changed the revision")
	}

	// scoped query through the same tool: a wrong track yields nothing; the
	// real track with a narrowed tick window keeps its clip but filters by
	// region (revision stays authoritative).
	scopedRaw, err := bookkeeping.Call(ctx, "project__query", `{"projectId":"`+proj.ID+`","trackId":"t_missing"}`)
	if err != nil {
		t.Fatal(err)
	}
	var scoped struct {
		State struct {
			Tracks []map[string]any `json:"tracks"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(scopedRaw), &scoped); err != nil {
		t.Fatal(err)
	}
	if len(scoped.State.Tracks) != 0 {
		t.Fatalf("scoped query must filter tracks: %v", scoped.State.Tracks)
	}
	windowRaw, err := bookkeeping.Call(ctx, "project__query", `{"projectId":"`+proj.ID+`","trackId":"`+res1.CreatedIDs[0]+`","startTick":0,"endTick":100}`)
	if err != nil {
		t.Fatal(err)
	}
	var window struct {
		Revision int64 `json:"revision"`
		State    struct {
			Tracks []struct {
				ID    string `json:"id"`
				Clips []struct {
					Notes []map[string]any `json:"notes"`
				} `json:"clips"`
			} `json:"tracks"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(windowRaw), &window); err != nil {
		t.Fatal(err)
	}
	if window.Revision != current.Revision || len(window.State.Tracks) != 1 || len(window.State.Tracks[0].Clips) != 1 {
		t.Fatalf("window query: rev %d tracks %+v", window.Revision, window.State.Tracks)
	}
}

// TestProjectToolsRequireBinding pins the authorization rule: without a
// bound project the tools are not even defined; with a binding, other
// project IDs are refused.
func TestProjectToolsRequireBinding(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, nil, nil)
	proj, err := svc.CreateProject("未绑定", 480)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(ctx, storage.DefaultConfigID, "", "普通对话")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.ResolveRunConfig(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookkeeping := bookkeepingTools{inner: emptyTools{}, svc: svc, profile: profile, sessionID: conv.ID, runID: "test-run"}
	defs, err := bookkeeping.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if strings.HasPrefix(d.Function.Name, "project__") {
			t.Fatalf("unbound conversation must not offer %s", d.Function.Name)
		}
	}
	if err := svc.SetConversationProject(conv.ID, proj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := bookkeeping.Call(ctx, "project__query", `{"projectId":"other"}`); err == nil {
		t.Fatal("foreign projectId must be refused")
	}
	if _, err := bookkeeping.Call(ctx, "project__edit", `{"projectId":"other","opId":"x","baseRevision":0,"ops":[{"kind":"project.rename","title":"x"}]}`); err == nil {
		t.Fatal("foreign projectId must be refused")
	}
}

func TestProjectImportExportFiles(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	path := t.TempDir() + "/loop.mid"
	proj, err := svc.CreateProject("往返", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ExportProjectMIDI(proj.ID, path); err != nil {
		t.Fatal(err)
	}
	imported, err := svc.ImportProjectMIDI(path)
	if err != nil {
		t.Fatal(err)
	}
	if imported.PPQ != 480 || imported.State.Title != "往返" {
		t.Fatalf("imported: %+v", imported.State)
	}
	bad := t.TempDir() + "/bad.mid"
	if err := os.WriteFile(bad, []byte("not a midi file at all........"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportProjectMIDI(bad); err == nil {
		t.Fatal("invalid file must be rejected")
	}
	if err := svc.ExportProjectMIDI(proj.ID, t.TempDir()+"/missing-dir/out.mid"); err == nil {
		t.Fatal("export into missing directory must fail")
	}
}

// Notifications are hints to query committed state, never evidence of an
// attempted edit. A rejected batch must leave both revision and UI unchanged.
func TestProjectNotificationsArePostCommit(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	var events []Event
	off := svc.Subscribe(func(evt Event) {
		if evt.ProjectID == "" {
			return
		}
		if evt.Type == "project.changed" {
			item, err := svc.GetProject(evt.ProjectID)
			if err != nil || item.Revision < evt.Revision {
				t.Errorf("notification before commit: %+v %v", evt, err)
			}
		}
		events = append(events, evt)
	})
	defer off()
	p, err := svc.CreateProject("notify", 480)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SubmitProjectOps(p.ID, "edit", "cli", 0, []project.Op{{Kind: "project.rename", Title: "new"}})
	if err != nil {
		t.Fatal(err)
	}
	count := len(events)
	if _, err = svc.SubmitProjectOps(p.ID, "stale", "gui", 0, []project.Op{{Kind: "project.rename", Title: "stale"}}); err == nil {
		t.Fatal("expected conflict")
	}
	if len(events) != count {
		t.Fatal("rejected operation emitted change")
	}
	if _, err = svc.UndoProject(p.ID, "undo", "gui", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RedoProject(p.ID, "redo", "gui", 2); err != nil {
		t.Fatal(err)
	}
	if err = svc.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[4].Type != "project.deleted" || events[3].Revision != 3 {
		t.Fatalf("events: %+v", events)
	}
}

func TestProjectNoteEditsPreserveOtherEvents(t *testing.T) {
	svc, store := newTestService(t, nil, nil)
	state, _ := project.Blank("events", 480)
	state.Tracks = []project.Track{{ID: "t_events", Clips: []project.Clip{{ID: "c_events", Length: 1920,
		Notes:    []project.Note{{ID: "n_events", Start: 0, Duration: 480, Pitch: 60, Velocity: 100}},
		Controls: []project.ControlEvent{{ID: "e_cc", Kind: project.KindCC, Controller: 64, Value: 127}},
		Raw:      []project.RawEvent{{ID: "e_raw", Tick: 123, Data: []byte{0xff, 0x06, 0x01, 'A'}}},
	}}}}
	p, err := store.CreateProjectFromState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	start, duration, pitch, velocity := int64(480), int64(960), 64, 72
	_, err = svc.SubmitProjectOps(p.ID, "notes", "gui", 0, []project.Op{{Kind: "note.update", ClipID: "c_events", NoteDeltas: []project.NoteDelta{{ID: "n_events", Start: &start, Duration: &duration, Pitch: &pitch, Velocity: &velocity}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"edit", "undo", "redo"} {
		if direction == "undo" {
			_, err = svc.UndoProject(p.ID, "undo-events", "gui", 1)
		}
		if direction == "redo" {
			_, err = svc.RedoProject(p.ID, "redo-events", "gui", 2)
		}
		if err != nil {
			t.Fatal(err)
		}
		current, e := svc.GetProject(p.ID)
		if e != nil {
			t.Fatal(e)
		}
		got := current.State.Tracks[0].Clips[0]
		before, _ := json.Marshal(state.Tracks[0].Clips[0].Controls)
		after, _ := json.Marshal(got.Controls)
		if string(before) != string(after) {
			t.Fatalf("%s dropped controls", direction)
		}
		before, _ = json.Marshal(state.Tracks[0].Clips[0].Raw)
		after, _ = json.Marshal(got.Raw)
		if string(before) != string(after) {
			t.Fatalf("%s dropped raw events", direction)
		}
	}
}
