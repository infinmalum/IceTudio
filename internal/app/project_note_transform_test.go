package app

import (
	"context"
	"encoding/json"
	"icetudio/internal/project"
	"testing"
)

func TestPrepareNoteTransformSelectionAndTransactions(t *testing.T) {
	svc, store := newTestService(t, nil, nil)
	state, _ := project.Blank("transform", 480)
	state.Tracks = []project.Track{{ID: "t_gui", Clips: []project.Clip{{ID: "c_gui", Length: 1920, Notes: []project.Note{{ID: "n_one", Start: 130, Duration: 120, Pitch: 60, Velocity: 90}, {ID: "n_two", Start: 250, Duration: 120, Pitch: 127, Velocity: 80}}, Controls: []project.ControlEvent{{ID: "e_gui", Kind: project.KindCC, Controller: 64, Value: 127}}}}}}
	p, err := store.CreateProjectFromState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	spec := func(raw string) project.TransformSpec {
		var v project.TransformSpec
		if e := json.Unmarshal([]byte(raw), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	tr := spec(`{"transpose":{"semitones":1}}`)
	ops, err := svc.PrepareNoteTransform(p.ID, 0, "c_gui", []string{"n_one"}, tr)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := svc.GetProject(p.ID)
	if before.Revision != 0 || before.State.Tracks[0].Clips[0].Notes[0].Pitch != 60 {
		t.Fatal("compiler wrote project")
	}
	for _, ids := range [][]string{nil, {"n_one", "n_one"}, {"n_missing"}, {"n_one", "n_two"}} {
		if _, e := svc.PrepareNoteTransform(p.ID, 0, "c_gui", ids, tr); e == nil {
			t.Fatalf("accepted invalid/partial selection %v", ids)
		}
	}
	if _, e := svc.PrepareNoteTransform(p.ID, 0, "c_missing", []string{"n_one"}, tr); e == nil {
		t.Fatal("missing clip accepted")
	}
	if _, err = svc.SubmitProjectOps(p.ID, "gui-transform", "gui", 0, ops); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.GetProject(p.ID)
	clip := after.State.Tracks[0].Clips[0]
	if clip.Notes[0].Pitch != 61 || clip.Notes[1].Pitch != 127 || len(clip.Controls) != 1 {
		t.Fatal("selection or events changed")
	}
	if _, e := svc.PrepareNoteTransform(p.ID, 0, "c_gui", []string{"n_one"}, tr); e == nil {
		t.Fatal("stale compile accepted")
	}
	if _, e := svc.SubmitProjectOps(p.ID, "late-transform", "gui", 0, ops); e == nil {
		t.Fatal("stale submit accepted")
	}
	if _, e := svc.UndoProject(p.ID, "undo-transform", "gui", 1); e != nil {
		t.Fatal(e)
	}
	restored, _ := svc.GetProject(p.ID)
	if restored.State.Tracks[0].Clips[0].Notes[0].Pitch != 60 {
		t.Fatal("undo failed")
	}
	quant, err := svc.PrepareNoteTransform(p.ID, 2, "c_gui", []string{"n_one"}, spec(`{"quantize":{"unit":"1/16","strength":100}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, e := svc.SubmitProjectOps(p.ID, "quantize", "gui", 2, quant); e != nil {
		t.Fatal(e)
	}
	got, _ := svc.GetProject(p.ID)
	notes := got.State.Tracks[0].Clips[0].Notes
	if notes[0].Start != 120 || notes[1].Start != 250 || notes[0].Duration != 120 || notes[0].Velocity != 90 {
		t.Fatal("quantization modified unrelated data")
	}
}
