package app

import (
	"icetudio/internal/project"
	"testing"
)

func TestArrangementScopeRequiresBothTracksAndEverySource(t *testing.T) {
	st := project.State{Tracks: []project.Track{
		{ID: "a", Clips: []project.Clip{{ID: "ca"}}},
		{ID: "b", Clips: []project.Clip{{ID: "cb"}}},
	}}
	edit := &project.ArrangementEdit{Action: "move", IDs: []string{"ca"}, Track: "b"}
	ops := []project.Op{{Kind: "clip.arrange", Arrangement: edit}}
	if err := checkScopeOps(ProjectScope{Tracks: []string{"a"}}, ops, &st); err == nil {
		t.Fatal("destination escaped frozen scope")
	}
	if err := checkScopeOps(ProjectScope{Tracks: []string{"a", "b"}}, ops, &st); err != nil {
		t.Fatal(err)
	}
	edit.Track = ""
	edit.IDs = append(edit.IDs, "cb")
	if err := checkScopeOps(ProjectScope{Clips: []string{"ca"}}, ops, &st); err == nil {
		t.Fatal("second source escaped frozen scope")
	}
	edit.IDs = edit.IDs[:1]
	edit.Action = "split"
	if err := checkScopeOps(ProjectScope{Clips: []string{"ca"}}, ops, &st); err == nil {
		t.Fatal("clip scope created another clip")
	}
}

func TestSharedPatternBodyRejectsPartialFrozenScope(t *testing.T) {
	st := project.State{Tracks: []project.Track{
		{ID: "a", Clips: []project.Clip{{ID: "ca", PatternID: "p"}}},
		{ID: "b", Clips: []project.Clip{{ID: "cb", PatternID: "p"}}},
	}}
	ops := []project.Op{{Kind: "note.add", ClipID: "ca", Notes: []project.NewNote{{Duration: 120, Pitch: 60, Velocity: 90}}}}
	for _, scope := range []ProjectScope{{Tracks: []string{"a"}}, {Clips: []string{"ca"}}} {
		if err := checkScopeOps(scope, ops, &st); err == nil {
			t.Fatal("shared edit escaped scope")
		}
	}
	if err := checkScopeOps(ProjectScope{Clips: []string{"ca", "cb"}}, ops, &st); err != nil {
		t.Fatal(err)
	}
}
