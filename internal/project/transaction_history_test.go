package project

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAppliedHistoryMaterializesCreationIDs(t *testing.T) {
	st, err := Blank("历史", 480)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyTransaction(&st, []Op{
		{Kind: "track.add", Track: &TrackSpec{ID: "t_base"}},
		{Kind: "clip.add", Clip: &ClipSpec{ID: "c_base", TrackID: "t_base", Length: 1920}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(st)
	ops := []Op{
		{Kind: "track.add", Track: &TrackSpec{Name: "自动轨"}},
		{Kind: "clip.add", Clip: &ClipSpec{TrackID: "t_base", Length: 960}},
		{Kind: "note.add", ClipID: "c_base", Notes: []NewNote{{Duration: 120, Pitch: 60, Velocity: 100}, {ID: "n_removed", Duration: 240, Pitch: 64, Velocity: 80}}},
		{Kind: "control.add", ClipID: "c_base", Events: []EventSpec{{Kind: KindCC, Controller: 64, Value: 127}, {ID: "e_removed", Kind: KindPitchBend, Tick: 120, Value: 8192}}},
		{Kind: "note.remove", ClipID: "c_base", NoteIDs: []string{"n_removed"}},
		{Kind: "control.remove", ClipID: "c_base", EventIDs: []string{"e_removed"}},
	}
	requestBefore, _ := json.Marshal(ops)
	applied, inverse, result, err := ApplyTransactionWithHistory(&st, ops)
	if err != nil {
		t.Fatal(err)
	}
	requestAfter, _ := json.Marshal(ops)
	if string(requestAfter) != string(requestBefore) {
		t.Fatal("caller request mutated")
	}
	ids := []string{applied[0].Track.ID, applied[1].Clip.ID, applied[2].Notes[0].ID, applied[2].Notes[1].ID, applied[3].Events[0].ID, applied[3].Events[1].ID}
	if !reflect.DeepEqual(ids, result.CreatedIDs) {
		t.Fatalf("applied IDs %v differ from result %v", ids, result.CreatedIDs)
	}
	for _, id := range ids {
		if !ValidID(id) {
			t.Fatalf("invalid creation ID %q", id)
		}
	}
	if applied[2].Notes[1].ID != "n_removed" || applied[3].Events[1].ID != "e_removed" {
		t.Fatal("explicit IDs lost for objects removed later in the batch")
	}
	committed, _ := json.Marshal(st)
	if _, _, err := ApplyTransaction(&st, inverse); err != nil {
		t.Fatal(err)
	}
	restored, _ := json.Marshal(st)
	if string(restored) != string(before) {
		t.Fatal("inverse did not restore baseline")
	}
	if _, _, err := ApplyTransaction(&st, applied); err != nil {
		t.Fatal(err)
	}
	redone, _ := json.Marshal(st)
	if string(redone) != string(committed) {
		t.Fatal("applied history did not reproduce committed state")
	}

	// A late validation error must expose neither partial music nor a partial
	// operation set/result that could be mistaken for a committed history.
	failing := append(append([]Op{}, ops...), Op{Kind: "not-supported"})
	var baseline State
	if err := json.Unmarshal(before, &baseline); err != nil {
		t.Fatal(err)
	}
	forward, reverse, outcome, err := ApplyTransactionWithHistory(&baseline, failing)
	if err == nil || forward != nil || reverse != nil || !reflect.DeepEqual(outcome, Result{}) {
		t.Fatalf("partial outcome escaped: %v %v %+v %v", forward, reverse, outcome, err)
	}
	afterFailure, _ := json.Marshal(baseline)
	if string(afterFailure) != string(before) {
		t.Fatal("failed batch modified state")
	}
}
