package project

import "testing"

func TestNoteDiffCountsBeyondTransportLimit(t *testing.T) {
	before := State{Tracks: []Track{{ID: "t1", Clips: []Clip{{ID: "c1", Start: 100, Notes: []Note{{ID: "n1", Pitch: 60}, {ID: "n2", Pitch: 62}}}}}}}
	after := State{Tracks: []Track{{ID: "t1", Clips: []Clip{{ID: "c1", Start: 100, Notes: []Note{{ID: "n1", Pitch: 61}, {ID: "n3", Pitch: 65}}}}}}}
	diff := DiffNotes(&before, &after, 1)
	if diff.Total != 3 || diff.Added != 1 || diff.Updated != 1 || diff.Removed != 1 || len(diff.Changes) != 1 {
		t.Fatalf("bad diff: %+v", diff)
	}
	if diff.Changes[0].Before.Pitch != 60 || diff.Changes[0].After.Pitch != 61 || diff.Changes[0].ClipStart != 100 {
		t.Fatal("unstable or incorrect first change")
	}
}
