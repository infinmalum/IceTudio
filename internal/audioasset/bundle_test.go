package audioasset

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"icetudio/internal/project"
)

func TestPortableBundleRoundtripAndAssetIntegrity(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw := filepath.Join(dir, "sound")
	if err := os.WriteFile(raw, []byte{0, 8, 0, 12, 0, 16, 0, 1}, 0600); err != nil {
		t.Fatal(err)
	}
	asset, err := PublishPCM(dir, "sound", raw)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := project.Blank("portable", 480)
	st.AudioAssets = []project.AudioAsset{asset}
	st.Tracks = []project.Track{{ID: "track", Audio: []project.AudioClip{{ID: "audio", Name: "sound", AssetID: asset.ID, Frames: 2, Gain: 100}}}}
	path := filepath.Join(dir, "portable.icezip")
	if err = ExportBundle(ctx, dir, path, st); err != nil {
		t.Fatal(err)
	}
	newDir := t.TempDir()
	restored, err := ImportBundle(ctx, newDir, path)
	if err != nil || !reflect.DeepEqual(st, restored) {
		t.Fatalf("bundle roundtrip %v", err)
	}
	if err = Verify(newDir, asset); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(Root(dir), asset.Digest+".pcm"), []byte{0, 1, 0, 1, 0, 1, 0, 1}, 0600); err != nil {
		t.Fatal(err)
	}
	if err = ExportBundle(ctx, dir, filepath.Join(dir, "bad.icezip"), st); err == nil {
		t.Fatal("exported corrupt asset")
	}
}

func TestDecoderRejectsOversizeAndInvalidFLACMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversize.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate((512 << 20) + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	if _, err = Import(context.Background(), dir, path); err == nil {
		t.Fatal("accepted oversized source")
	}
	flac := filepath.Join(dir, "bad.flac")
	bad := make([]byte, 42)
	copy(bad, "fLaC")
	bad[7] = 34 // STREAMINFO length; remaining metadata declares a zero sample rate.
	if err = os.WriteFile(flac, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Import(context.Background(), dir, flac); err == nil {
		t.Fatal("accepted invalid FLAC stream info")
	}
}
