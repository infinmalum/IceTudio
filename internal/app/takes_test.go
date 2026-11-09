package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"icetudio/internal/audioasset"
	"icetudio/internal/project"
)

func TestMIDITakeTempoAndControllerPreservation(t *testing.T) {
	st, _ := project.Blank("record", 480)
	st.Tempo = append(st.Tempo, project.TempoPoint{Tick: 480, USPerQuarter: 1000000})
	st.Tracks = []project.Track{{ID: "track"}}
	ops, err := midiTakeOps(&st, "track", 240, []audioasset.MIDIFrame{
		{Nanos: 0, Data: []byte{0x92, 60, 90}},
		{Nanos: int64(250 * time.Millisecond), Data: []byte{0xb2, 64, 127}},
		{Nanos: int64(750 * time.Millisecond), Data: []byte{0x82, 60, 0}},
		{Nanos: int64(750 * time.Millisecond), Data: []byte{0xe2, 0, 64}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = project.ApplyNewTransaction(&st, ops, nil); err != nil {
		t.Fatal(err)
	}
	c := st.Tracks[0].Clips[0]
	if c.Start != 240 || c.Notes[0].Duration != 480 || c.Controls[0].Tick != 240 || c.Controls[1].Value != 8192 || c.Controls[1].Channel != 2 {
		t.Fatalf("recorded note/control mapping: %+v", c)
	}
}

func TestRecoveredAudioTakeRejectsStaleRevisionAndCommitsOnce(t *testing.T) {
	_, store := newTestService(t, nil, nil)
	dir := t.TempDir()
	svc := New(Options{Store: store, DataDir: dir})
	p, err := svc.CreateProject("take recovery", 480)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := svc.SubmitProjectOps(p.ID, "setup", "gui", p.Revision, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "track"}}})
	if err != nil {
		t.Fatal(err)
	}
	take := audioasset.Take{ID: "take_recorded", Kind: "audio", ProjectID: p.ID, TrackID: "track", Revision: p.Revision, Tick: 480}
	takeDir := filepath.Join(dir, "audio-takes")
	if err = os.MkdirAll(takeDir, 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(take)
	if err = os.WriteFile(filepath.Join(takeDir, take.ID+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(svc.recordings.Path(take.ID), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SaveTake(p.ID, take.ID, setup.Revision, 480, false); err == nil {
		t.Fatal("stale take silently rebased")
	}
	first, err := svc.SaveTake(p.ID, take.ID, setup.Revision, 960, true)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.SaveTake(p.ID, take.ID, setup.Revision, 960, true)
	if err != nil || replayed.OpID != first.OpID || replayed.Revision != first.Revision {
		t.Fatalf("lost reply replay: %+v %v", replayed, err)
	}
	p, err = svc.GetProject(p.ID)
	if err != nil || len(p.State.Tracks[0].Audio) != 1 || p.State.Tracks[0].Audio[0].Frames != 2 || p.State.Tracks[0].Audio[0].Start != 960 {
		t.Fatalf("recovered audio take was duplicated or truncated: %v", err)
	}
}

func TestAudioCompCommitUndoAndRepeatableSources(t *testing.T) {
	_, store := newTestService(t, nil, nil)
	dir := t.TempDir()
	s := New(Options{Store: store, DataDir: dir})
	p, err := s.CreateProject("comp", 480)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.SubmitProjectOps(p.ID, "seed", "gui", p.Revision, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "track"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "audio-takes"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"take_a", "take_b"} {
		meta := audioasset.Take{ID: id, Kind: "audio", ProjectID: p.ID, TrackID: "track", Revision: tx.Revision, LoopFrames: 24000}
		body, _ := json.Marshal(meta)
		if err = os.WriteFile(filepath.Join(dir, "audio-takes", id+".json"), body, 0600); err != nil {
			t.Fatal(err)
		}
		raw := make([]byte, 48000*4)
		raw[0] = id[len(id)-1]
		if err = os.WriteFile(s.recordings.Path(id), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	parts := []CompPart{{TakeID: "take_a", Source: 24000, Frames: 12000, Tick: 0}, {TakeID: "take_b", Source: 0, Frames: 12000, Tick: 240}}
	ctx := context.Background()
	if _, err = s.PrepareComp(ctx, p.ID, tx.Revision-1, "track", parts); err == nil {
		t.Fatal("stale comp accepted")
	}
	overlap := append([]CompPart(nil), parts...)
	overlap[1].Tick = 239
	if _, err = s.PrepareComp(ctx, p.ID, tx.Revision, "track", overlap); err == nil {
		t.Fatal("overlap accepted")
	}
	ops, err := s.PrepareComp(ctx, p.ID, tx.Revision, "track", parts)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = s.SubmitProjectOps(p.ID, "takecomp", "gui", tx.Revision, ops)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.GetProject(p.ID)
	if err != nil || len(next.State.Tracks[0].Audio) != 2 || next.State.Tracks[0].Audio[0].Source != 24000 {
		t.Fatalf("comp persisted %v", err)
	}
	takes, err := s.AudioTakes(p.ID)
	if err != nil || len(takes) != 2 || takes[0].LoopFrames != 24000 {
		t.Fatalf("retained takes %+v %v", takes, err)
	}
	bundle := filepath.Join(dir, "comp.icezip")
	if err = s.ExportAudioBundle(ctx, p.ID, tx.Revision, bundle); err != nil {
		t.Fatal(err)
	}
	portable, err := s.ImportAudioBundle(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.AudioTakes(portable.ID)
	if err != nil || len(archived) != 2 || archived[0].LoopFrames != 24000 {
		t.Fatalf("archived takes %+v %v", archived, err)
	}
	rebuilt, err := s.PrepareComp(ctx, portable.ID, portable.Revision, "track", parts)
	if err != nil || len(rebuilt) != 2 || rebuilt[0].Kind != "audio.add" {
		t.Fatalf("portable recomp %v %v", rebuilt, err)
	}
	tx, err = s.UndoProject(p.ID, "undo_comp", "gui", tx.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next, err = s.GetProject(p.ID)
	if err != nil || len(next.State.AudioAssets) != 0 || len(next.State.Tracks[0].Audio) != 0 {
		t.Fatalf("undo comp %v", err)
	}
	if _, err = s.PrepareComp(ctx, p.ID, tx.Revision, "track", parts); err != nil {
		t.Fatal("undo lost original takes", err)
	}
}
