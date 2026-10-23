package mcptools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/skills"
)

func TestLocalMCPToolDiscoveryAndCall(t *testing.T) {
	hub, err := NewLocal(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	tools, err := hub.Definitions(context.Background())
	if err != nil || len(tools) != 2 || tools[0].Function.Name != "local__current_time" || tools[1].Function.Name != "local__read_file" {
		t.Fatalf("tools: %+v, %v", tools, err)
	}
	result, err := hub.Call(context.Background(), "local__current_time", "{}")
	if err != nil || !strings.Contains(result, "T") {
		t.Fatalf("call: %q, %v", result, err)
	}
	if _, err := hub.Call(context.Background(), "local__current_time", "broken"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestLoadAgentSkillThroughMCP(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "review-code")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: review-code\ndescription: Review code changes.\n---\nRead the diff."), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skills.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := NewLocal(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	tools, err := hub.Definitions(context.Background())
	if err != nil || len(tools) != 3 {
		t.Fatalf("tools: %+v, %v", tools, err)
	}
	result, err := hub.Call(context.Background(), "local__load_skill", `{"name":"review-code"}`)
	if err != nil || !strings.Contains(result, "Read the diff") {
		t.Fatalf("skill: %q, %v", result, err)
	}
}
