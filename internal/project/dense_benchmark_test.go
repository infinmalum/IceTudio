package project

import (
	"fmt"
	"reflect"
	"testing"
)

func BenchmarkDenseNoteUpdate(b *testing.B) {
	state, err := Blank("benchmark", 480)
	if err != nil {
		b.Fatal(err)
	}
	notes := make([]Note, 10000)
	deltas := make([]NoteDelta, len(notes))
	velocity := 99
	for i := range notes {
		id := fmt.Sprintf("n_%d", i)
		notes[i] = Note{ID: id, Start: int64(i%64) * 120, Duration: 120, Pitch: 60, Velocity: 100}
		deltas[i] = NoteDelta{ID: id, Velocity: &velocity}
	}
	state.Tracks = []Track{{ID: "t_1", Clips: []Clip{{ID: "c_1", Length: 7680, Notes: notes}}}}
	op := Op{Kind: "note.update", ClipID: "c_1", NoteDeltas: deltas}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ApplyTransaction(&state, []Op{op}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDenseNoteUpdateIsAtomicAndReversible(t *testing.T) {
	state, err := Blank("dense", 480)
	if err != nil {
		t.Fatal(err)
	}
	notes := make([]Note, 1024)
	deltas := make([]NoteDelta, len(notes))
	velocity, secondVelocity := 80, 60
	for i := range notes {
		id := fmt.Sprintf("n_%d", i)
		notes[i] = Note{ID: id, Start: int64(i%64) * 120, Duration: 120, Pitch: 60, Velocity: 100}
		deltas[i] = NoteDelta{ID: id, Velocity: &velocity}
	}
	state.Tracks = []Track{{ID: "t_1", Clips: []Clip{{ID: "c_1", Length: 7680, Notes: notes}}}}
	before, err := clone(&state)
	if err != nil {
		t.Fatal(err)
	}
	deltas = append(deltas, NoteDelta{ID: "n_0", Velocity: &secondVelocity})
	inverse, _, err := ApplyTransaction(&state, []Op{{Kind: "note.update", ClipID: "c_1", NoteDeltas: deltas}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Tracks[0].Clips[0].Notes[0].Velocity != 60 || state.Tracks[0].Clips[0].Notes[1023].Velocity != 80 {
		t.Fatal("dense update missed or reordered notes")
	}
	if _, _, err := ApplyTransaction(&state, inverse); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&state, before) {
		t.Fatal("undo did not restore the full state after repeated deltas")
	}
	bad := append(append([]NoteDelta{}, deltas...), NoteDelta{ID: "n_missing", Velocity: &velocity})
	if _, _, err := ApplyTransaction(&state, []Op{{Kind: "note.update", ClipID: "c_1", NoteDeltas: bad}}); err == nil {
		t.Fatal("missing note accepted")
	}
	if !reflect.DeepEqual(&state, before) {
		t.Fatal("failed dense update changed state")
	}
}
