package desktop

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNativeTimeMapUsesExistingDraftAndAtomicHistory(t *testing.T) {
	e, svc, p := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	// Preserve an exact imported tempo that has a repeating BPM decimal.
	p.State.Tempo[0].USPerQuarter = 500001
	f := newTimeMap(p)
	ops, err := compileTimeMap(f.snapshot)
	if err != nil || len(ops) != 0 {
		t.Fatal("unchanged imported tempo rounded", err)
	}
	f.snapshot.Draft.Tempo = append(f.snapshot.Draft.Tempo, tempoInput{Key: 1, Tick: "720", BPM: "90", OriginalBPM: "120", OriginalUS: 500000})
	f.snapshot.Draft.Meter = append(f.snapshot.Draft.Meter, meterInput{Key: 1, Tick: "720", Numerator: "7", Denominator: "8"})
	data, _ := json.Marshal(f.snapshot)
	if err := svc.SaveTimeMapDraft("", "native_map", string(data)); err != nil {
		t.Fatal(err)
	}
	r, err := svc.GetTimeMapDraft()
	if err != nil {
		t.Fatal(err)
	}
	var recovered timeMapSnapshot
	if err := json.Unmarshal([]byte(r.Payload), &recovered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recovered, f.snapshot) {
		t.Fatal("Native checkpoint changed existing format")
	}
	ops, err = compileTimeMap(recovered)
	if err != nil || len(ops) != 2 {
		t.Fatalf("compile: %v", err)
	}
	if _, err := e.Execute(EditTarget{p.ID, 0}, "edit", ops); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetProject(p.ID)
	if len(got.State.Tempo) != 2 || got.State.Tempo[0].USPerQuarter != 500001 || got.State.Tempo[1].USPerQuarter != 666667 || got.State.Meter[1].Tick != 720 || !reflect.DeepEqual(got.State.Tracks, p.State.Tracks) {
		t.Fatal("atomic map corrupted imported data")
	}
	if _, err := e.Execute(EditTarget{p.ID, 0}, "edit", ops); err == nil {
		t.Fatal("restored map rebased")
	}
	if err := svc.ClearTimeMapDraft("native_map", "cleared_map"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveTimeMapDraft("", "stale_map", string(data)); err == nil {
		t.Fatal("clear barrier allowed late write")
	}
	if _, err := e.Execute(EditTarget{p.ID, 1}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetProject(p.ID)
	if len(got.State.Tempo) != 1 || len(got.State.Meter) != 1 {
		t.Fatal("shared undo did not undo complete map")
	}
}
func TestNativeTimeMapRejectsInvalidInputWithoutChangingDraft(t *testing.T) {
	cases := []func(*timeMapSnapshot){
		func(s *timeMapSnapshot) { s.Draft.Tempo[0].BPM = "NaN" },
		func(s *timeMapSnapshot) { s.Draft.Tempo[0].BPM = "0" },
		func(s *timeMapSnapshot) { s.Draft.Tempo[0].Tick = "1" },
		func(s *timeMapSnapshot) { s.Draft.Tempo[0].Tick = "0.5" },
		func(s *timeMapSnapshot) { s.Draft.Meter[0].Denominator = "3" },
		func(s *timeMapSnapshot) { s.Draft.Meter[0].Numerator = "256" },
		func(s *timeMapSnapshot) { s.Draft.Tempo = append(s.Draft.Tempo, s.Draft.Tempo[0]) },
	}
	for _, change := range cases {
		p := fixture()
		s := newTimeMap(p).snapshot
		change(&s)
		before, _ := json.Marshal(s)
		if _, err := compileTimeMap(s); err == nil {
			t.Fatal("invalid map accepted")
		}
		after, _ := json.Marshal(s)
		if string(before) != string(after) {
			t.Fatal("validation changed raw input")
		}
	}
}
func TestNativeTransportRequestFreezesTargetAndRejectsFractions(t *testing.T) {
	f := &propertyForm{entry: newMusicDraft(EditTarget{"p_music", 3}, "", "", "", "transport", ""), values: propertyValues{Position: "1234567", Loop: true, LoopStart: "960", LoopEnd: "1920"}}
	r, err := transportRequest(f)
	if err != nil || r.ProjectID != "p_music" || r.Revision != 3 || r.Tick != 1234567 || r.LoopStart != 960 {
		t.Fatal("transport target changed", err)
	}
	for _, raw := range []string{"-1", "0.5", "NaN", "9223372036854775808"} {
		f.values.Position = raw
		if _, err := transportRequest(f); err == nil {
			t.Fatal("invalid position accepted")
		}
	}
	f.values.Position = "0"
	f.values.LoopEnd = "960"
	if _, err := transportRequest(f); err == nil {
		t.Fatal("empty loop accepted")
	}
}
