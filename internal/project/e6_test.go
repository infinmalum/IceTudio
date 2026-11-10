package project

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestE6RackAndFrozenStateSurviveHistoryAndPortableReopen(t *testing.T) {
	st, err := Blank("E6", 480)
	if err != nil {
		t.Fatal(err)
	}
	st.Tracks = []Track{{ID: "track", Clips: []Clip{}}, {ID: "second", Clips: []Clip{}}}
	p := &PluginState{Format: "clap", Class: "test", Name: "Test", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		State: []byte{3, 2, 1}, Parameters: []PluginParameter{{ID: 1, Name: "Cutoff", Default: .25, Value: .75}}}
	ch := MixChannel{ID: "track", Gain: 1, Effects: []MixEffect{{ID: "slot", Kind: "plugin", Wet: 1, Plugin: p}},
		Synth: &SynthPatch{Preset: "fm-bell", Tone: .5, Release: .25},
		Rack: &MixRack{Transpose: 12, Velocity: 1.25, Macros: []MixMacro{{Name: "Tone", Value: .4,
			Bindings: []MacroBinding{{Target: "slot", Parameter: PluginParameterKey(1), Min: .1, Max: .6}, {Target: SynthMacroTarget, Parameter: "tone", Max: 1}}}}},
		FrozenAssetID: "frozen"}
	copy, err := (MixChannel{ID: "second", Gain: 1}).LoadRack(ch.SaveRack("Chain"))
	if err != nil {
		t.Fatal(err)
	}
	if copy.FrozenAssetID != "" || copy.Effects[0].ID == "slot" || copy.Rack.Macros[0].Bindings[0].Target != copy.Effects[0].ID ||
		copy.Rack.Macros[0].Bindings[1].Target != SynthMacroTarget || copy.MacroValue(copy.Effects[0].ID, PluginParameterKey(1), 0) < .299 {
		t.Fatalf("rack copy or remap incorrect: %+v", copy)
	}
	asset := AudioAsset{ID: "frozen", Name: "Freeze", Digest: p.SHA256, Frames: 1024}
	mix := Mixer{Channels: []MixChannel{ch, copy}, FavoriteSounds: []string{"fm-bell"}, RackPresets: []RackPreset{ch.SaveRack("Chain")},
		Lanes: []Automation{{ID: "lane", Target: "slot", Parameter: PluginParameterKey(1), Points: []AutomationPoint{{Tick: 100, Value: .2}}}}}
	applied, inv, _, err := ApplyNewTransaction(&st, []Op{{Kind: "audio.asset", AudioAsset: &asset}, {Kind: "mixer.set", Mixer: &mix}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(st)
	var reopened State
	if err := json.Unmarshal(raw, &reopened); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePortable(&reopened); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened.Mixer, st.Mixer) {
		t.Fatal("mixer state changed on reopen")
	}
	if _, _, err := ApplyTransaction(&st, inv); err != nil || st.Mixer != nil || len(st.AudioAssets) != 0 {
		t.Fatalf("freeze did not undo atomically: %v", err)
	}
	if _, _, err := ApplyTransaction(&st, applied); err != nil || !reflect.DeepEqual(reopened.Mixer, st.Mixer) {
		t.Fatalf("freeze and rack did not redo: %v", err)
	}
	bad := st.Mixer.Copy()
	bad.Channels[0].Rack.Macros[0].Bindings[0].Target = "missing"
	if _, _, err := ApplyTransaction(&st, []Op{{Kind: "mixer.set", Mixer: &bad}}); err == nil || !reflect.DeepEqual(reopened.Mixer, st.Mixer) {
		t.Fatal("dangling macro binding was accepted or mutated the project")
	}
}
