package mcptools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestClassifierClassifiesBuiltinAndExternalTools(t *testing.T) {
	classifier := Classifier{}
	cases := []struct {
		name        string
		arguments   string
		effect      Effect
		sensitivity Sensitivity
	}{
		{"local__current_time", "{}", EffectPure, SensPublic},
		{"local__load_skill", `{"name":"x"}`, EffectRead, SensWorkspace},
		{"project__query", "{}", EffectRead, SensWorkspace},
		{"project__select", "{}", EffectRead, SensWorkspace},
		{"project__edit", "{}", EffectWrite, SensUnknown},
		{"project__transform", "{}", EffectWrite, SensUnknown},
		{"project__unregistered", "{}", EffectExternal, SensUnknown},
		{"myskills__search", "{}", EffectExternal, SensUnknown},
		{"totally_unknown", "{}", EffectUnknown, SensUnknown},
	}
	for _, item := range cases {
		effect, sensitivity := classifier.Classify(item.name, item.arguments)
		if effect != item.effect || sensitivity != item.sensitivity {
			t.Fatalf("%s: got %s/%s, want %s/%s", item.name, effect, sensitivity, item.effect, item.sensitivity)
		}
	}
}

func TestClassifierReadFilePathSensitivity(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "project")
	protected := filepath.Join(home, ".ssh")
	classifier := Classifier{Workspace: []string{workspace}, Protected: []string{protected}}
	cases := []struct {
		path        string
		sensitivity Sensitivity
	}{
		{filepath.Join(workspace, "main.go"), SensWorkspace},
		{filepath.Join(workspace, "sub", "doc.md"), SensWorkspace},
		{filepath.Join(protected, "id_rsa"), SensCredential},
		{filepath.Join(home, "notes", "todo.txt"), SensUnknown},
		{filepath.Join(home, "project", ".env"), SensCredential},
		{filepath.Join(home, "server.pem"), SensCredential},
		{filepath.Join(home, "tokens.txt"), SensUnknown},
	}
	for _, item := range cases {
		_, sensitivity := classifier.Classify("local__read_file", `{"path":`+quoteJSON(item.path)+`}`)
		if sensitivity != item.sensitivity {
			t.Fatalf("%s: got %s, want %s", item.path, sensitivity, item.sensitivity)
		}
	}
	// A symlink pointing into a protected directory keeps the protected
	// classification (the resolved target decides).
	if err := os.MkdirAll(filepath.Join(workspace, "leak"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(protected, filepath.Join(workspace, "leak", "ssh")); err != nil {
		t.Fatal(err)
	}
	_, sensitivity := classifier.Classify("local__read_file", `{"path":"`+filepath.Join(workspace, "leak", "ssh", "id_rsa")+`"}`)
	if sensitivity != SensCredential {
		t.Fatalf("symlink into protected dir: got %s", sensitivity)
	}
	// Malformed arguments are unknown, never auto-approved.
	_, sensitivity = classifier.Classify("local__read_file", "broken")
	if sensitivity != SensUnknown {
		t.Fatalf("broken args: got %s", sensitivity)
	}
}

func TestReadFileTool(t *testing.T) {
	hub, err := NewLocal(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("hello icetudio"), 0600); err != nil {
		t.Fatal(err)
	}
	content, err := hub.Call(t.Context(), "local__read_file", `{"path":`+quoteJSON(file)+`}`)
	if err != nil || !strings.Contains(content, "hello icetudio") {
		t.Fatalf("read: %q %v", content, err)
	}
	if _, err := hub.Call(t.Context(), "local__read_file", `{"path":"`+filepath.Join(dir, "missing.txt")+`"}`); err == nil {
		t.Fatal("missing file accepted")
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, make([]byte, maxReadFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Call(t.Context(), "local__read_file", `{"path":`+quoteJSON(big)+`}`); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("oversized file accepted: %v", err)
	}
	binary := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binary, []byte{0x00, 0x01, 0x02}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Call(t.Context(), "local__read_file", `{"path":`+quoteJSON(binary)+`}`); err == nil || !strings.Contains(err.Error(), "文本") {
		t.Fatalf("binary file accepted: %v", err)
	}
}

func TestBuildEnvAllowlist(t *testing.T) {
	t.Setenv("ICETUDIO_SECRET_TOKEN", "do-not-leak")
	t.Setenv("ICETUDIO_VISIBLE", "fine")

	env := BuildEnv(nil)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "ICETUDIO_SECRET_TOKEN") {
		t.Fatalf("secret inherited by default: %v", env)
	}
	if !strings.Contains(joined, "PATH=") || !strings.Contains(joined, "HOME=") {
		t.Fatalf("default allowlist must keep PATH and HOME: %v", env)
	}

	env = BuildEnv([]string{"ICETUDIO_VISIBLE", "ICETUDIO_MISSING_VAR"})
	joined = strings.Join(env, "\n")
	if !strings.Contains(joined, "ICETUDIO_VISIBLE=fine") {
		t.Fatalf("allowlisted variable not passed: %v", env)
	}
	if strings.Contains(joined, "ICETUDIO_SECRET_TOKEN") || strings.Contains(joined, "ICETUDIO_MISSING_VAR") {
		t.Fatalf("non-allowlisted variables leaked: %v", env)
	}
}
