package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNativeBuildTaskfilesAreValidAndFrameworkIndependent(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"Taskfile.yml", "build/Taskfile.yml", "build/linux/Taskfile.yml", "build/windows/Taskfile.yml", "build/darwin/Taskfile.yml", "build/linux/nfpm/nfpm.yaml", ".github/workflows/ci.yml"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(strings.ToLower(string(data)), "wails3") || strings.Contains(string(data), "github.com/wailsapp") {
			t.Fatal("obsolete framework build command", name)
		}
	}
}
func TestNoProductRuntimeOrPianoSourceImportsRetiredBindings(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, folder := range []string{"frontend/src", "frontend/e2e"} {
		if err := filepath.WalkDir(filepath.Join(root, folder), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, bad := range []string{"@wailsio/runtime", "bindings/icetudio", "/wails/runtime"} {
				if strings.Contains(string(b), bad) {
					t.Errorf("retired dependency %s in %s", bad, path)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
