package audioasset

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type endlessPCM struct{}

func (endlessPCM) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 1
	}
	time.Sleep(time.Millisecond)
	return len(p), nil
}

func TestTakeCrashWriter(t *testing.T) {
	dir := os.Getenv("ICETUDIO_TEST_TAKE_CRASH")
	if dir == "" {
		return
	}
	r := NewRecorder(dir)
	f, err := os.Create(r.Path("take_test"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = recordPCM(endlessPCM{}, f, nil, func(frames int64, _ int) {
		if frames >= 24000 {
			_ = os.WriteFile(filepath.Join(dir, "take-ready"), []byte{1}, 0600)
		}
	})
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
}
func TestTakeRecoveryAfterProcessKill(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(dir)
	if err := os.MkdirAll(r.dir(), 0700); err != nil {
		t.Fatal(err)
	}
	take := Take{ID: "take_test", ProjectID: "project", TrackID: "track", Kind: "audio", Revision: 3, Tick: 480}
	manifest, _ := json.Marshal(take)
	if err := os.WriteFile(filepath.Join(r.dir(), "take_test.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTakeCrashWriter$")
	cmd.Env = append(os.Environ(), "ICETUDIO_TEST_TAKE_CRASH="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "take-ready")); err == nil {
			ready = true
			break
		}
	}
	killErr := cmd.Process.Kill()
	waitErr := cmd.Wait()
	if !ready || killErr != nil || waitErr == nil {
		t.Fatalf("capture did not reach fsynced boundary: %t %v %v", ready, killErr, waitErr)
	}
	restarted := NewRecorder(dir)
	pending, err := restarted.List()
	if err != nil || len(pending) != 1 || pending[0].Frames < 24000 {
		t.Fatalf("take unavailable after kill: %+v %v", pending, err)
	}
	got, asset, err := restarted.Recover(take.ID)
	if err != nil || got != take || asset.Frames != pending[0].Frames {
		t.Fatalf("take recovery failed: %+v %v", asset, err)
	}
}
