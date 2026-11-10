//go:build linux && cgo

package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"icetudio/internal/project"
)

func TestE6SoundPresetsPreviewAndExport(t *testing.T) {
	for _, preset := range project.SoundPresets() {
		t.Run(preset.ID, func(t *testing.T) {
			dir := t.TempDir()
			patch := &project.SynthPatch{Preset: preset.ID, Tone: preset.Tone, Release: preset.Release}
			pcm, err := SynthPreview(context.Background(), *patch)
			if err != nil || !hasSample(pcm) {
				t.Fatalf("silent preview: %v", err)
			}
			st, _ := project.Blank("Built-in", 480)
			st.Tracks = []project.Track{{ID: "track", Clips: []project.Clip{{ID: "clip", Length: 480, Notes: []project.Note{{ID: "note", Duration: 360, Pitch: 60, Velocity: 100}}}}}}
			st.Mixer = &project.Mixer{Channels: []project.MixChannel{{ID: "track", Gain: 1, Synth: patch,
				Rack: &project.MixRack{Transpose: 12, Velocity: 1}}}}
			m := New(dir, func(string) (Snapshot, error) { return Snapshot{State: st}, nil })
			defer m.Close()
			out := filepath.Join(dir, "render.wav")
			if err := m.Export(context.Background(), ExportRequest{ProjectID: "p", Bits: 16}, out); err != nil {
				t.Fatal(err)
			}
			audio, err := os.ReadFile(out)
			if err != nil || len(audio) < 44 || !hasSample(audio[44:]) {
				t.Fatalf("silent render: %v", err)
			}
		})
	}
}

func hasSample(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return true
		}
	}
	return false
}
