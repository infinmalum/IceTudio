//go:build linux && cgo

package playback

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"icetudio/internal/audioasset"
	"icetudio/internal/pluginhost"
	"icetudio/internal/project"
)

// Run a compiled copy of this test beside the real scanner and runtime
// helpers. The fixture path contains the maintainer-approved plugin matrix.
func TestProductionPluginRuntime(t *testing.T) {
	root := os.Getenv("ICETUDIO_PLUGIN_RUNTIME_TEST")
	if root == "" {
		t.Skip("requires isolated Linux helpers and the approved local CLAP/VST3 fixtures")
	}
	ctx := context.Background()
	dir := t.TempDir()
	catalog := pluginhost.New(dir)
	for _, folder := range []string{"dexed", "surge"} {
		if err := catalog.Scan(ctx, filepath.Join(root, folder), nil); err != nil {
			t.Fatal(err)
		}
	}
	modules, err := catalog.List()
	if err != nil || len(modules) != 6 {
		t.Fatalf("scan returned %d modules: %v", len(modules), err)
	}
	for _, module := range modules {
		if module.Status != pluginhost.Ready || len(module.Classes) != 1 {
			t.Fatalf("fixture module unavailable: %+v", module)
		}
		class := module.Classes[0]
		t.Run(module.Format+"/"+class.Name, func(t *testing.T) {
			identity := pluginhost.Identity{Format: module.Format, Class: class.ID, SHA256: module.SHA256}
			worker, err := catalog.Open(ctx, identity, class.Kind, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			plugin := &project.PluginState{Format: identity.Format, Class: identity.Class, SHA256: identity.SHA256, Name: class.Name, State: worker.State}
			for _, p := range worker.Parameters {
				plugin.Parameters = append(plugin.Parameters, project.PluginParameter{ID: p.ID, Name: p.Name, Default: p.Value, Value: p.Value})
			}
			worker.Close()
			fx := project.MixEffect{ID: "slot", Kind: "plugin", Wet: 1, Plugin: plugin}
			ch := project.MixChannel{ID: "track", Gain: 1}
			if class.Kind == "instrument" {
				ch.Instrument = &fx
			} else {
				ch.Effects = []project.MixEffect{fx}
			}
			st, _ := project.Blank("runtime", 480)
			st.Tracks = []project.Track{{ID: "track", Clips: []project.Clip{{ID: "clip", Length: 480, Notes: []project.Note{{ID: "note", Duration: 360, Pitch: 60, Velocity: 102}}}}}}
			st.Mixer = &project.Mixer{Channels: []project.MixChannel{ch}}
			s, err := Compile(Snapshot{State: st})
			if err != nil {
				t.Fatal(err)
			}
			s.catalog = catalog
			n := mixerTestNative(t)
			if err := n.install(s, 0, 0, 0, false, true); err != nil {
				t.Fatal(err)
			}
			left, _ := n.render(12000)
			var energy float64
			for _, sample := range left {
				energy += float64(sample) * float64(sample)
			}
			if issues := n.pluginIssues(); len(issues) != 0 || energy < 1e-6 || math.IsNaN(energy) {
				t.Fatalf("offline signal energy=%g issues=%v", energy, issues)
			}
			t.Logf("offline signal energy=%g frames of latency=%d state bytes=%d", energy, n.mixerLatency(), len(plugin.State))
			n.offline = false // Same mixer with the nonblocking device policy.
			if err := n.install(s, 0, 0, 0, false, true); err != nil {
				t.Fatal(err)
			}
			if n.mixerLatency() < pluginhost.LiveLead {
				t.Fatal("transport lead missing from PDC")
			}
			energy = 0
			for i := 0; i < 48; i++ {
				l, _ := n.render(128)
				for _, v := range l {
					energy += float64(v) * float64(v)
				}
				time.Sleep(2 * time.Millisecond)
			}
			if issues := n.pluginIssues(); len(issues) != 0 || energy < 1e-6 || math.IsNaN(energy) {
				t.Fatalf("paced nonblocking signal energy=%g issues=%v", energy, issues)
			}
			n.workers[0].Terminate()
			n.render(256)
			if issues := n.pluginIssues(); len(issues) != 1 {
				t.Fatalf("process exit did not surface an instance failure: %v", issues)
			}
			m := New(dir, func(string) (Snapshot, error) { return Snapshot{ID: "p", State: st}, nil })
			defer m.Close()
			request := ExportRequest{ProjectID: "p", Bits: 16}
			output := filepath.Join(t.TempDir(), "plugin.wav")
			if err := m.Export(ctx, request, output); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(output)
			if err != nil || len(saved) <= 44 {
				t.Fatalf("WAV missing: %v", err)
			}
			freeze := filepath.Join(t.TempDir(), "freeze.wav")
			if err := m.ExportTrackFreeze(ctx, "p", 0, "track", freeze); err != nil {
				t.Fatal(err)
			}
			asset, err := audioasset.Import(ctx, dir, freeze)
			if err != nil {
				t.Fatal(err)
			}
			n.offline = true
			plugin.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			if err := n.install(s, 0, 0, 0, false, true); err == nil {
				t.Fatal("offline renderer accepted an unresolved plugin")
			}
			if err := m.Export(ctx, request, output); err == nil {
				t.Fatal("export accepted a missing plugin")
			}
			unchanged, _ := os.ReadFile(output)
			if !bytes.Equal(unchanged, saved) {
				t.Fatal("failed render replaced existing file")
			}
			ch.FrozenAssetID = asset.ID
			st.AudioAssets = []project.AudioAsset{asset}
			st.Mixer.SetChannel(ch)
			if err := m.Export(ctx, request, filepath.Join(t.TempDir(), "without-plugin.wav")); err != nil {
				t.Fatalf("frozen audio depends on the unavailable plugin: %v", err)
			}

		})
	}
}

func TestProductionPluginPDC(t *testing.T) {
	root := os.Getenv("ICETUDIO_PLUGIN_RUNTIME_TEST")
	if root == "" {
		t.Skip("requires compiled CLAP and VST3 latency fixtures")
	}
	fixtures := t.TempDir()
	clap, err := os.ReadFile(filepath.Join(root, "build", "latency-fixture.clap"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(fixtures, "latency-fixture.clap"), clap, 0600); err != nil {
		t.Fatal(err)
	}
	const vst3 = "latency-vst3.vst3"
	if err = os.CopyFS(filepath.Join(fixtures, vst3), os.DirFS(filepath.Join(root, "build", vst3))); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := pluginhost.New(dir)
	if err := c.Scan(context.Background(), fixtures, nil); err != nil {
		t.Fatal(err)
	}
	modules, err := c.List()
	if err != nil || len(modules) != 2 {
		t.Fatalf("latency catalog: %v %v", modules, err)
	}
	for _, module := range modules {
		t.Run(module.Format, func(t *testing.T) {
			if module.Status != pluginhost.Ready || len(module.Classes) != 1 {
				t.Fatalf("scan: %+v", module)
			}
			p := &project.PluginState{Format: module.Format, SHA256: module.SHA256, Class: module.Classes[0].ID, Name: module.Classes[0].Name}
			if module.Format == "vst3" {
				p.Parameters = []project.PluginParameter{{ID: 100, Name: "Latency", Value: 1}}
			}
			st := project.State{Tracks: []project.Track{{ID: "a"}, {ID: "b"}}, Mixer: &project.Mixer{
				Channels: []project.MixChannel{{ID: "a", Gain: 1, Effects: []project.MixEffect{{ID: "fx", Kind: "plugin", Wet: 1, Plugin: p}}}},
			}}
			s := mixerTestScore(t, st)
			s.catalog = c
			impulse := make([]int16, 1024)
			impulse[0], impulse[1] = 8192, 8192
			s.Audio = []audioRegion{testPCM(0, "a", impulse), testPCM(1, "b", impulse)}
			n := mixerTestNative(t)
			if err := n.install(s, 0, 0, 0, false, true); err != nil {
				t.Fatal(err)
			}
			l, r := n.render(512)
			if issues := n.pluginIssues(); len(issues) != 0 {
				t.Fatal(issues)
			}
			at := 192 + pluginhost.OfflineLead
			if n.mixerLatency() != at || l[at] < .49 || r[at] < .49 || l[0] != 0 || l[64+pluginhost.OfflineLead] != 0 {
				t.Fatalf("PDC expected only aligned impulse at %d: latency=%d %g %g %g %g", at, n.mixerLatency(), l[at], r[at], l[0], l[64+pluginhost.OfflineLead])
			}
		})
	}
}
