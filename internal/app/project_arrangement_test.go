package app

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"icetudio/internal/project"
)

// The canvas sends only clip placement/length patches. Verify the retained
// service contract against real storage, including events absent from the UI.
func TestProjectArrangementPreservesEvents(t *testing.T) {
	svc, store := newTestService(t, nil, nil)
	state, _ := project.Blank("arrangement", 480)
	state.Tracks = []project.Track{{ID: "t_arr", Clips: []project.Clip{{ID: "c_arr", Length: 1920,
		Notes:    []project.Note{{ID: "n_arr", Start: 120, Duration: 360, Pitch: 60, Velocity: 100}},
		Controls: []project.ControlEvent{{ID: "e_arr", Tick: 720, Kind: project.KindCC, Controller: 64, Value: 127}},
		Raw:      []project.RawEvent{{ID: "r_arr", Tick: 960, Data: []byte{0xff, 0x06, 0x01, 'A'}}},
	}}}}
	p, err := store.CreateProjectFromState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	start, length := int64(480), int64(1440)
	_, err = svc.SubmitProjectOps(p.ID, "arr-edit", "gui", 0, []project.Op{{Kind: "clip.update", TrackID: "t_arr", ClipID: "c_arr", ClipPatch: &project.ClipPatch{Start: &start, Length: &length}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"edit", "undo", "redo"} {
		if phase == "undo" {
			_, err = svc.UndoProject(p.ID, "arr-undo", "gui", 1)
		} else if phase == "redo" {
			_, err = svc.RedoProject(p.ID, "arr-redo", "gui", 2)
		}
		if err != nil {
			t.Fatal(err)
		}
		got, e := svc.GetProject(p.ID)
		if e != nil {
			t.Fatal(e)
		}
		clip := got.State.Tracks[0].Clips[0]
		want := state.Tracks[0].Clips[0]
		if phase != "undo" {
			want.Start, want.Length = start, length
		}
		a, _ := json.Marshal(clip)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Fatalf("%s changed local events or placement: %s; want %s", phase, a, b)
		}
	}
	// Each event type independently prevents destructive shortening. No local
	// event is silently cut off, and a preceding placement patch is rolled back.
	for _, limit := range []int64{479, 720, 960} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			before, _ := svc.GetProject(p.ID)
			_, e := svc.SubmitProjectOps(p.ID, project.NewID("reject"), "gui", before.Revision, []project.Op{
				{Kind: "clip.update", ClipID: "c_arr", ClipPatch: &project.ClipPatch{Start: &length}},
				{Kind: "clip.update", ClipID: "c_arr", ClipPatch: &project.ClipPatch{Length: &limit}},
			})
			if e == nil {
				t.Fatal("destructive shortening accepted")
			}
			after, _ := svc.GetProject(p.ID)
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("rejected batch changed persisted project")
			}
		})
	}
}
