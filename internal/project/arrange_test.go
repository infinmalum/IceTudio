package project

import (
	"reflect"
	"testing"
)

func TestArrangementUndoRedoAndRawPreservation(t *testing.T) {
	st, _ := Blank("arrangement", 480)
	_, _, _, err := ApplyNewTransaction(&st, []Op{
		{Kind: "track.add", Track: &TrackSpec{ID: "a"}},
		{Kind: "track.add", Track: &TrackSpec{ID: "b"}},
		{Kind: "clip.add", Clip: &ClipSpec{ID: "clip", TrackID: "a", Start: 120, Length: 960}},
		{Kind: "note.add", ClipID: "clip", Notes: []NewNote{{ID: "note", Start: 240, Duration: 480, Pitch: 60, Velocity: 90}}},
		{Kind: "control.add", ClipID: "clip", Events: []EventSpec{{ID: "sustain", Kind: KindCC, Controller: 64, Value: 127, Tick: 480}}},
		{Kind: "marker.add", ClipID: "clip", Marker: &MarkerSpec{ID: "verse", Tick: 720, Name: "主歌"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := clone(&st)
	pitch := 61
	applied, inverse, res, err := ApplyNewTransaction(&st, []Op{
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "split", IDs: []string{"clip"}, Tick: 600}},
		{Kind: "note.update", ClipID: "clip", NoteDeltas: []NoteDelta{{ID: "note", Pitch: &pitch}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	left, right := st.Tracks[0].Clips[0], st.Tracks[0].Clips[1]
	if len(res.CreatedIDs) != 2 || left.Notes[0].Duration != 240 || right.Notes[0].Duration != 240 || right.Notes[0].ID == "note" || right.Controls[0].ID != "sustain" || right.Controls[0].Tick != 0 || right.Raw[0].ID != "verse" || right.Raw[0].Tick != 240 {
		t.Fatalf("split loses identity or time: %+v / %+v", left, right)
	}
	changed, _ := clone(&st)
	if _, _, err = ApplyTransaction(&st, inverse); err != nil || !reflect.DeepEqual(*original, st) {
		t.Fatalf("undo split: %v", err)
	}
	if _, _, err = ApplyTransaction(&st, applied); err != nil || !reflect.DeepEqual(*changed, st) {
		t.Fatalf("redo split: %v", err)
	}
	_, inverse, res, err = ApplyNewTransaction(&st, []Op{{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "copy", IDs: []string{right.ID}, Track: "b", Tick: 1920, Repeat: 2, Seed: "fixed"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	copies := st.Tracks[1].Clips
	if len(copies) != 2 || copies[1].Start != 2400 || copies[1].Raw[0].ID == right.Raw[0].ID || !reflect.DeepEqual(copies[1].Raw[0].Data, right.Raw[0].Data) {
		t.Fatalf("bad copies: %+v", copies)
	}
	if _, _, err = ApplyTransaction(&st, inverse); err != nil || !reflect.DeepEqual(*changed, st) {
		t.Fatalf("undo copy: %v", err)
	}
	prior := map[string]bool{res.CreatedIDs[0]: true}
	_, _, _, err = ApplyNewTransaction(&st, []Op{{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "copy", IDs: []string{right.ID}, Track: "b", Tick: 1920, Repeat: 2, Seed: "fixed"}}}, prior)
	if err == nil || !reflect.DeepEqual(*changed, st) {
		t.Fatal("previous identity was reused or rejection mutated state")
	}
	if _, _, _, err = ApplyNewTransaction(&st, inverse, nil); err == nil {
		t.Fatal("fresh edit accepted internal restore")
	}
}

func TestArrangementMoveMergeAndCropPreserveAbsoluteEvents(t *testing.T) {
	st, _ := Blank("move", 480)
	_, _, _, err := ApplyNewTransaction(&st, []Op{
		{Kind: "track.add", Track: &TrackSpec{ID: "first"}},
		{Kind: "track.add", Track: &TrackSpec{ID: "second", Channel: 7}},
		{Kind: "clip.add", Clip: &ClipSpec{ID: "clip_a", TrackID: "first", Start: 0, Length: 480}},
		{Kind: "clip.add", Clip: &ClipSpec{ID: "clip_b", TrackID: "second", Start: 960, Length: 480}},
		{Kind: "note.add", ClipID: "clip_a", Notes: []NewNote{{ID: "n", Start: 120, Duration: 360, Pitch: 60, Velocity: 90, Channel: 2}}},
		{Kind: "marker.add", ClipID: "clip_b", Marker: &MarkerSpec{ID: "marker", Tick: 120, Name: "section"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := clone(&st)
	_, inverse, _, err := ApplyNewTransaction(&st, []Op{
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "move", IDs: []string{"clip_a"}, Track: "second", Tick: 480}},
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "merge", IDs: []string{"clip_b", "clip_a"}}},
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "crop", IDs: []string{"clip_a"}, Tick: 720, End: 1081}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := st.Tracks[1].Clips[0]
	if len(st.Tracks[0].Clips) != 0 || len(st.Tracks[1].Clips) != 1 || c.ID != "clip_a" || c.Start != 720 || c.Length != 361 ||
		len(c.Notes) != 1 || c.Notes[0].ID != "n" || c.Notes[0].Channel != 2 || c.Notes[0].Start != 0 || c.Notes[0].Duration != 240 ||
		len(c.Raw) != 1 || c.Raw[0].ID != "marker" || c.Start+c.Raw[0].Tick != 1080 {
		t.Fatalf("unexpected result: %+v", c)
	}
	if _, _, err := ApplyTransaction(&st, inverse); err != nil || !reflect.DeepEqual(*original, st) {
		t.Fatalf("multi-operation undo: %v", err)
	}
}
