package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The retired Chromium facade is replaced by the real MyGo integration suite.
// Domain/permission/atomicity tests in this package remain always enabled;
// this opt-in suite additionally needs a desktop and prebuilt piano assets.
func TestMusicEditorNativeIntegration(t *testing.T) {
	if os.Getenv("ICETUDIO_MYGO_NATIVE") != "1" {
		t.Skip("requires actual MyGo/WebKit desktop; use npm run test:native")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", "frontend/e2e/native.mjs")
	command.Dir = root
	command.Env = os.Environ()
	out, err := command.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
}
