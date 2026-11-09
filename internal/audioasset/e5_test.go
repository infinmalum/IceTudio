package audioasset

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/project"
)

func toneWAV(t *testing.T, name string, freq float64, frames int) {
	t.Helper()
	raw := make([]byte, 44+frames*FrameBytes)
	copy(raw, "RIFF")
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(raw)-8))
	copy(raw[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 2)
	binary.LittleEndian.PutUint32(raw[24:], 48000)
	binary.LittleEndian.PutUint32(raw[28:], 192000)
	binary.LittleEndian.PutUint16(raw[32:], 4)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	copy(raw[36:], "data")
	binary.LittleEndian.PutUint32(raw[40:], uint32(frames*FrameBytes))
	for i := 0; i < frames; i++ {
		v := uint16(int16(12000 * math.Sin(2*math.Pi*freq*float64(i)/48000)))
		binary.LittleEndian.PutUint16(raw[44+i*4:], v)
		binary.LittleEndian.PutUint16(raw[46+i*4:], v)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMaterialReferenceRelinkAndBundle(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg is unavailable")
	}
	ctx := context.Background()
	data, src := t.TempDir(), t.TempDir()
	path := filepath.Join(src, "tone.wav")
	toneWAV(t, path, 440, 48000)
	lib := NewLibrary(data)
	if n, err := lib.Scan(ctx, src); n != 1 || err != nil {
		t.Fatalf("scan %d %v", n, err)
	}
	if n, err := lib.Scan(ctx, src); n != 0 || err != nil {
		t.Fatalf("repeat scan %d %v", n, err)
	}
	items, err := lib.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("items %v %v", items, err)
	}
	item := items[0]
	item.Tags, item.BPM, item.Favorite = "lead, demo", 120, true
	if err = lib.Update(item); err != nil {
		t.Fatal(err)
	}
	reopen, err := NewLibrary(data).List()
	if err != nil || len(reopen) != 1 || reopen[0] != item {
		t.Fatalf("metadata reopen %+v %v", reopen, err)
	}
	result, err := lib.Analyze(ctx, item.ID)
	if err != nil || !result.Pitched || math.Abs(result.MIDI-69) > .08 {
		t.Fatalf("analysis %+v %v", result, err)
	}
	asset, err := lib.Prepare(ctx, item.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(Root(data), asset.Digest+".pcm")); !os.IsNotExist(err) {
		t.Fatalf("reference wrote permanent PCM: %v", err)
	}
	if got, err := Load(data, asset); len(got) != 96000 || err != nil {
		t.Fatalf("reference %d %v", len(got), err)
	}
	moved := filepath.Join(src, "renamed.wav")
	if err = os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(data, asset); err == nil || !strings.Contains(err.Error(), "定位") {
		t.Fatalf("missing source %v", err)
	}
	wrong := filepath.Join(src, "wrong.wav")
	toneWAV(t, wrong, 330, 48000)
	if _, err = Reference(ctx, data, wrong, &asset); err == nil {
		t.Fatal("wrong source accepted")
	}
	if _, err = Reference(ctx, data, moved, &asset); err != nil {
		t.Fatal(err)
	}
	st := project.State{PPQ: 480, Tempo: []project.TempoPoint{{USPerQuarter: 500000}},
		Meter: []project.MeterPoint{{Numerator: 4, Denominator: 4}}, AudioAssets: []project.AudioAsset{asset},
		Tracks: []project.Track{{ID: "sampler", Sampler: &project.Sampler{Zones: []project.SampleZone{{AssetID: asset.ID, Frames: asset.Frames,
			Root: 69, Low: 60, High: 80, MinVelocity: 1, MaxVelocity: 127, Sustain: 100, Gain: 100}}}}}}
	bundle := filepath.Join(data, "portable.icezip")
	if err = ExportBundle(ctx, data, bundle, st); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(moved); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	imported, err := ImportBundle(ctx, destination, bundle)
	if err != nil || imported.Tracks[0].Sampler.Zones[0].AssetID != asset.ID {
		t.Fatalf("import %v", err)
	}
	if _, err = Load(destination, imported.AudioAssets[0]); err != nil {
		t.Fatal(err)
	}
}

func TestPolishCorrectsPitchAndPreservesSource(t *testing.T) {
	out, err := exec.Command("ffmpeg", "-hide_banner", "-filters").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "rubberband") {
		t.Skip("FFmpeg Rubber Band filter is unavailable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "detuned.wav")
	toneWAV(t, path, 430, 96000)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := Import(ctx, dir, path)
	if err != nil {
		t.Fatal(err)
	}
	clip := project.AudioClip{ID: "c", AssetID: source.ID, Frames: source.Frames, Gain: 100}
	next, err := PreparePolish(ctx, dir, source, clip, Polish{Percent: 125, Correct: true, Target: 69})
	if err != nil {
		t.Fatal(err)
	}
	if next.Frames != 120000 || next.Derivation == nil || next.Derivation.SourceDigest != source.Digest || project.ValidateAudioAsset(next) != nil {
		t.Fatalf("derived metadata %+v", next)
	}
	pcm, err := Load(dir, next)
	if err != nil {
		t.Fatal(err)
	}
	pitch := Analyze(pcm)
	if !pitch.Pitched || math.Abs(pitch.MIDI-69) > .25 {
		t.Fatalf("pitch %+v", pitch)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("original WAV changed")
	}
	if err = Verify(dir, source); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = PreparePolish(canceled, dir, source, clip, Polish{Percent: 100, Target: -1}); err == nil {
		t.Fatal("canceled job succeeded")
	}
}

func TestOnsetsAndTempoOnSyntheticLoop(t *testing.T) {
	buf := make([]int16, 48000*4*2)
	for at := 0; at < len(buf)/2; at += 24000 {
		for i := 0; i < 600; i++ {
			buf[(at+i)*2] = 20000
			buf[(at+i)*2+1] = 20000
		}
	}
	if hits := Onsets(buf); len(hits) != 8 {
		t.Fatalf("onsets %v", hits)
	}
	if a := Analyze(buf); a.BPM != 120 || a.Key != "" {
		t.Fatalf("loop %+v", a)
	}
}
