package project

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSharedPatternIdentityHistoryAndDetach(t *testing.T) {
	st, _ := Blank("shared", 480)
	_, _, _, err := ApplyNewTransaction(&st, []Op{
		{Kind: "track.add", Track: &TrackSpec{ID: "a"}},
		{Kind: "track.add", Track: &TrackSpec{ID: "b"}},
		{Kind: "clip.add", Clip: &ClipSpec{ID: "source", TrackID: "a", Length: 480}},
		{Kind: "note.add", ClipID: "source", Notes: []NewNote{{ID: "original", Duration: 120, Pitch: 60, Velocity: 90}}},
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "link", IDs: []string{"source"}, Track: "b", Tick: 480, Repeat: 2}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := clone(&st)
	first := st.Tracks[1].Clips[0]
	if len(PatternMembers(&st, first.ID)) != 3 || first.Notes[0].ID == "original" {
		t.Fatal("instances lack independent event IDs")
	}
	pitch := 72
	applied, inverse, result, err := ApplyNewTransaction(&st, []Op{
		{Kind: "note.update", ClipID: first.ID, NoteDeltas: []NoteDelta{{ID: first.Notes[0].ID, Pitch: &pitch}}},
		{Kind: "control.add", ClipID: first.ID, Events: []EventSpec{{Kind: KindCC, Controller: 64, Value: 127}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CreatedIDs) != 3 {
		t.Fatalf("creation report: %+v", result.CreatedIDs)
	}
	for _, tr := range st.Tracks {
		for _, c := range tr.Clips {
			if c.Notes[0].Pitch != 72 || c.Controls[0].Value != 127 {
				t.Fatalf("content failed to propagate: %+v", c)
			}
		}
	}
	edited, _ := clone(&st)
	if _, _, err = ApplyTransaction(&st, inverse); err != nil || !reflect.DeepEqual(*initial, st) {
		t.Fatalf("undo shared: %v", err)
	}
	if _, _, err = ApplyTransaction(&st, applied); err != nil || !reflect.DeepEqual(*edited, st) {
		t.Fatalf("redo shared: %v", err)
	}
	_, inverse, _, err = ApplyNewTransaction(&st, []Op{
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "detach", IDs: []string{first.ID}}},
		{Kind: "control.remove", ClipID: first.ID, EventIDs: []string{st.Tracks[1].Clips[0].Controls[0].ID}},
	}, nil)
	if err != nil || len(st.Tracks[0].Clips[0].Controls) != 1 || len(st.Tracks[1].Clips[0].Controls) != 0 {
		t.Fatalf("detach: %v", err)
	}
	if _, _, err = ApplyTransaction(&st, inverse); err != nil || !reflect.DeepEqual(*edited, st) {
		t.Fatalf("undo detach: %v", err)
	}
}

func TestPatternAssetSurvivesLastPlacementAndSerializedUndo(t *testing.T) {
	st, _ := Blank("library", 480)
	_, _, _, err := ApplyNewTransaction(&st, []Op{
		{Kind: "track.add", Track: &TrackSpec{ID: "a"}},
		{Kind: "clip.add", Clip: &ClipSpec{ID: "c", TrackID: "a", Length: 480}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := clone(&st)
	applied, inv, result, err := ApplyNewTransaction(&st, []Op{
		{Kind: "clip.arrange", Arrangement: &ArrangementEdit{Action: "makePattern", IDs: []string{"c"}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assetID := result.CreatedIDs[0]
	raw, _ := json.Marshal(inv)
	if err = json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ApplyTransaction(&st, inv); err != nil || !reflect.DeepEqual(*base, st) {
		t.Fatalf("serialized inverse did not remove first asset: %v", err)
	}
	if _, _, err = ApplyTransaction(&st, applied); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = ApplyNewTransaction(&st, []Op{
		{Kind: "note.add", ClipID: "c", Notes: []NewNote{{Start: 120, Duration: 60, Pitch: 72, Velocity: 100}}},
		{Kind: "clip.remove", ClipID: "c"},
		{Kind: "pattern.place", PatternID: assetID, Arrangement: &ArrangementEdit{Track: "a", Tick: 480, Repeat: 2}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	clips := st.Tracks[0].Clips
	if len(clips) != 2 || clips[1].PatternID != assetID || clips[1].Notes[0].Pitch != 72 || clips[0].Notes[0].ID == clips[1].Notes[0].ID {
		t.Fatalf("placement did not use asset snapshot: %+v", clips)
	}
	if len(ScopeRegion(&st, []string{"a"}, nil).Patterns) != 0 {
		t.Fatal("scoped query exposed the project pattern library")
	}
}
