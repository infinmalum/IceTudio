package desktop

import (
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func TestMIDICreationTripletsBendAndMIDIExchange(t *testing.T) {
	edits, svc, p := testEdits(t)
	if err := edits.Check(); err != nil {
		t.Fatal(err)
	}
	tid, cid := "t_native", "c_native"
	length := int64(480 * 32)
	if _, err := edits.Execute(EditTarget{p.ID, p.Revision}, "edit", []project.Op{{Kind: "clip.update", ClipID: cid, ClipPatch: &project.ClipPatch{Length: &length}}}); err != nil {
		t.Fatal(err)
	}
	var err error
	p, err = svc.GetProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(kind string, v *midiInputs) {
		t.Helper()
		target := EditTarget{p.ID, p.Revision}
		f := &propertyForm{kind: kind, entry: newMusicDraft(target, p.Title, tid, cid, kind, ""), values: propertyValues{MIDI: v}}
		ops, err := midiFormOps(f, &p.State)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = edits.Execute(target, "edit", ops); err != nil {
			t.Fatal(err)
		}
		p, err = svc.GetProject(p.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	apply("midi_steps", &midiInputs{First: "1:1:0", Pitch: "36", Velocity: "81", Division: "3", Swing: "25", Probability: "100", Bars: "6", Steps: 7})
	c := p.State.Tracks[0].Clips[0]
	for i, want := range []int64{0, 200, 320} {
		n := c.Notes[i+1]
		if n.Start != want || n.Channel != 9 || n.Pitch != 36 {
			t.Fatalf("triplet/swing hit %d: %+v", i, n)
		}
	}
	apply("midi_control", &midiInputs{First: "1:3:0", Kind: "弯音", Value: "16000"})
	bend := p.State.Tracks[0].Clips[0].Controls[1]
	apply("midi_control", &midiInputs{First: "1:3:10", Kind: "弯音", Value: "8192", Event: bend.ID})
	updated := p.State.Tracks[0].Clips[0].Controls[1]
	if updated.ID != bend.ID || updated.Tick != 970 || updated.Value != 8192 {
		t.Fatalf("bend update: %+v", updated)
	}
	apply("midi_marker", &midiInputs{First: "1:3:0", Value: "副歌"})
	apply("midi_chords", &midiInputs{First: "1:1:0", Pitch: "72", Velocity: "90", Scale: "大调", Progression: strings.Repeat("I IV V I ", 8)})
	score, err := playback.Compile(playback.Snapshot{ID: p.ID, Revision: p.Revision, State: p.State})
	if err != nil || score.EndTick != length {
		t.Fatalf("playback compile: %v", err)
	}
	path := filepath.Join(t.TempDir(), "creation.mid")
	if err = svc.ExportProjectMIDI(p.ID, path); err != nil {
		t.Fatal(err)
	}
	out, err := svc.ImportProjectMIDI(path)
	if err != nil {
		t.Fatal(err)
	}
	hits, bends, markers, chords := 0, 0, 0, 0
	for _, tr := range out.State.Tracks {
		for _, clip := range tr.Clips {
			for _, n := range clip.Notes {
				if n.Channel == 0 && n.Pitch >= 72 {
					chords++
				}
				if n.Channel == 9 && n.Pitch == 36 {
					hits++
				}
			}
			for _, e := range clip.Controls {
				if e.Kind == project.KindPitchBend && e.Value == 8192 && clip.Start+e.Tick == 970 {
					bends++
				}
			}
			for _, r := range clip.Raw {
				if n, ok := project.MarkerName(r.Data); ok && n == "副歌" && clip.Start+r.Tick == 960 {
					markers++
				}
			}
		}
	}
	if hits != 18 || bends != 1 || markers != 1 || chords != 96 {
		t.Fatalf("MIDI lost musical events: hits=%d bends=%d markers=%d chords=%d", hits, bends, markers, chords)
	}
}
