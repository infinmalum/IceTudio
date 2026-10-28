package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestMCPRejectsNativeProjectNamespace(t *testing.T) {
	if err := (MCPServer{Name: "project", Command: "unused"}).validate(); err == nil {
		t.Fatal("MCP configuration accepted the native project namespace")
	}
}

func TestLoadDefaultsAndInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	got, err := Load(path)
	if err != nil || got.Model.Provider != "ollama" || got.Model.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("defaults: %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"model":{"provider":"other","baseUrl":"https://example.com"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if err := os.WriteFile(path, []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestLMStudioConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := Config{Model: Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "local-model"}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v, %v", info, err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"model":{"provider":"lmstudio"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil || got.Model.BaseURL != "http://127.0.0.1:1234/v1" {
		t.Fatalf("provider default: %+v, %v", got, err)
	}
}

func TestModelAddressMustBeLocal(t *testing.T) {
	for _, endpoint := range []string{
		"https://example.com/v1",
		"http://127.0.0.1.evil.com:1234/v1",
		"http://user@127.0.0.1:1234/v1",
		"http://127.0.0.1:1234/other",
	} {
		if err := (Model{Provider: "lmstudio", BaseURL: endpoint}).Validate(); err == nil {
			t.Errorf("accepted nonlocal/invalid endpoint %q", endpoint)
		}
	}
}

func TestPathsFollowRealExecutableInsteadOfUserOrWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "icetudio")
	if err := os.WriteFile(executable, []byte("test"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "unrelated-data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "unrelated-config"))
	paths, err := pathsForExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "data")
	if paths.Data != want || paths.Config != filepath.Join(want, "config.json") {
		t.Fatalf("unexpected paths: %+v", paths)
	}
	if err := EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(want, "skills")); err != nil || !info.IsDir() {
		t.Fatalf("missing skills directory: %v", err)
	}
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(executable, link); err == nil {
		linked, err := pathsForExecutable(link)
		if err != nil || linked != paths {
			t.Fatalf("symlink changed storage: %+v %v", linked, err)
		}
	}
	if _, err := pathsForExecutable("icetudio"); err == nil {
		t.Fatal("accepted relative executable path")
	}
}

func TestDefaultPathsUsesInstalledAppImage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("AppImage is Linux-specific")
	}
	image := filepath.Join(t.TempDir(), "IceTudio.AppImage")
	if err := os.WriteFile(image, nil, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPIMAGE", image)
	paths, err := DefaultPaths()
	if err != nil || paths.Data != filepath.Join(filepath.Dir(image), "data") {
		t.Fatalf("AppImage paths: %+v %v", paths, err)
	}
}

func TestDefaultPathsExplicitDataDirectory(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{filepath.Join(root, "user-data"), true},
		{"", false},
		{"relative/data", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("ICETUDIO_DATA_DIR", tc.value)
			paths, err := DefaultPaths()
			if !tc.valid {
				if err == nil {
					t.Fatalf("accepted invalid explicit path %q", tc.value)
				}
				return
			}
			if err != nil || paths.Data != tc.value || paths.Config != filepath.Join(tc.value, "config.json") {
				t.Fatalf("explicit path: %+v, %v", paths, err)
			}
		})
	}
}
