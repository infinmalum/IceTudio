//go:build linux && cgo

package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"icetudio/internal/audioasset"
	"icetudio/internal/project"
)

func TestAudioPCMReverseFadeExportAndNoOverwrite(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg unavailable")
	}
	dir := t.TempDir()
	raw := make([]byte, 4800*4)
	for i := 0; i < 4800; i++ {
		binary.LittleEndian.PutUint16(raw[i*4:], uint16(4000+i))
		binary.LittleEndian.PutUint16(raw[i*4+2:], uint16(8000+i))
	}
	source := filepath.Join(dir, "source.pcm")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	asset, err := audioasset.PublishPCM(dir, "ramp", source)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := project.Blank("audio", 480)
	clip := project.AudioClip{ID: "clip", Name: "ramp", AssetID: asset.ID, Start: 0, Source: 100, Frames: 1441, FadeIn: 2, Gain: 100, Reverse: true}
	st.AudioAssets = []project.AudioAsset{asset}
	st.Tracks = []project.Track{{ID: "track", Audio: []project.AudioClip{clip}}}
	m := New(dir, func(string) (Snapshot, error) { return Snapshot{ID: "p", State: st}, nil })
	defer m.Close()
	req := ExportRequest{ProjectID: "p", Bits: 16}
	path := filepath.Join(dir, "mix.wav")
	if err = m.Export(context.Background(), req, path); err != nil {
		t.Fatal(err)
	}
	wav, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 44+int(clip.Frames)*4 || binary.LittleEndian.Uint16(wav[44:]) != 0 ||
		binary.LittleEndian.Uint16(wav[52:]) != 5538 || binary.LittleEndian.Uint16(wav[len(wav)-4:]) != 4100 {
		t.Fatalf("wrong frame count, fade or reverse: %d, %v", len(wav), wav[44:56])
	}
	flac := filepath.Join(dir, "stem.flac")
	req.TrackID = "track"
	if err = m.Export(context.Background(), req, flac); err != nil {
		t.Fatal(err)
	}
	back, err := audioasset.Import(context.Background(), dir, flac)
	if err != nil || back.Frames != clip.Frames {
		t.Fatalf("FLAC roundtrip: %+v %v", back, err)
	}
	pcm, err := audioasset.Load(dir, back)
	if err != nil || pcm[4] != 5538 {
		t.Fatalf("decoded FLAC differs: %v", err)
	}
	if err = m.Export(context.Background(), req, path); err == nil {
		t.Fatal("overwrote existing output")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(wav, after) {
		t.Fatal("failed export changed old output")
	}
	req.Rate = 44100
	resampled := filepath.Join(dir, "44k.wav")
	if err = m.Export(context.Background(), req, resampled); err != nil {
		t.Fatal(err)
	}
	check, err := os.ReadFile(resampled)
	if err != nil || binary.LittleEndian.Uint32(check[24:]) != 44100 {
		t.Fatalf("WAV sample-rate setting: %v", err)
	}
	truncated := filepath.Join(dir, "truncated.wav")
	if err = os.WriteFile(truncated, wav[:len(wav)-4], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = audioasset.Import(context.Background(), dir, truncated); err == nil {
		t.Fatal("accepted truncated WAV")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelPath := filepath.Join(dir, "canceled.wav")
	if err = m.Export(ctx, req, cancelPath); err == nil {
		t.Fatal("published cancelled export")
	}
	if _, err = os.Stat(cancelPath); !os.IsNotExist(err) {
		t.Fatal("cancelled output is visible")
	}
}
