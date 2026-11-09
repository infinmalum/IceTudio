package project

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMixerAtomicHistoryAndPortableState(t *testing.T) {
	st := &State{PPQ: 480, Tempo: []TempoPoint{{Tick: 0, USPerQuarter: 500000}}, Meter: []MeterPoint{{Tick: 0, Numerator: 4, Denominator: 4}}}
	if _, _, err := ApplyTransaction(st, []Op{{Kind: "track.add", Track: &TrackSpec{ID: "t1"}}}); err != nil {
		t.Fatal(err)
	}
	m := Mixer{
		Channels: []MixChannel{{ID: "bus1", BusName: "Return", Gain: 1}, {ID: "t1", Gain: .75, Effects: []MixEffect{{ID: "fx1", Kind: "compressor", Amount: .5, Wet: 1}}}},
		Routes:   []MixRoute{{From: "t1", To: "bus1", Kind: "send", Gain: .25}},
		Lanes:    []Automation{{ID: "lane1", Target: "fx1", Parameter: "wet", Points: []AutomationPoint{{Tick: 0, Value: .5}, {Tick: 480, Value: 1}}}},
	}
	applied, inv, result, err := ApplyNewTransaction(st, []Op{{Kind: "mixer.set", Mixer: &m}}, nil)
	if err != nil || !reflect.DeepEqual(result.CreatedIDs, []string{"bus1", "fx1", "lane1"}) {
		t.Fatalf("commit %+v %v", result, err)
	}
	raw, _ := json.Marshal(st)
	var reopened State
	if err = json.Unmarshal(raw, &reopened); err != nil {
		t.Fatal(err)
	}
	if err = ValidatePortable(&reopened); err != nil || !reflect.DeepEqual(reopened.Mixer, st.Mixer) {
		t.Fatalf("portable %v", err)
	}
	cycle := m.Copy()
	cycle.Routes = append(cycle.Routes, MixRoute{From: "bus1", To: "t1", Kind: "sidechain", Gain: 1})
	if _, _, err = ApplyTransaction(st, []Op{{Kind: "project.rename", Title: "must not commit"}, {Kind: "mixer.set", Mixer: &cycle}}); err == nil || st.Title != "" {
		t.Fatalf("cycle committed: %v", err)
	}
	if _, _, err = ApplyTransaction(st, inv); err != nil || st.Mixer != nil {
		t.Fatalf("undo %v", err)
	}
	if _, _, err = ApplyTransaction(st, applied); err != nil || !reflect.DeepEqual(st.Mixer, &m) {
		t.Fatalf("redo %v", err)
	}
	rm, _, err := ApplyTransaction(st, []Op{{Kind: "track.remove", TrackID: "t1"}})
	if err != nil || len(st.Mixer.Routes) != 0 || len(st.Mixer.Lanes) != 0 || len(st.Mixer.Channels) != 1 {
		t.Fatalf("remove dependent objects: %v", err)
	}
	if _, _, err = ApplyTransaction(st, rm); err != nil || !reflect.DeepEqual(st.Mixer, &m) {
		t.Fatalf("track restore %v", err)
	}
	if ScopeRegion(st, []string{"t1"}, nil).Mixer != nil {
		t.Fatal("scoped projection leaked graph")
	}
	collision := m.Copy()
	collision.Lanes[0].ID = "t1"
	if _, _, err = ApplyTransaction(st, []Op{{Kind: "mixer.set", Mixer: &collision}}); err == nil {
		t.Fatal("accepted colliding ID")
	}
}
