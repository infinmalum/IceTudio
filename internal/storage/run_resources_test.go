package storage

import (
	"context"
	"testing"

	"icetudio/internal/skills"
)

func TestFrozenWorkerSnapshotSurvivesSkillContentChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	root, err := s.CreateRuntimeConfig(ctx, "root", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	root.Data.Execution.WorkerSources = []WorkerSource{{Name: "writer", Configuration: &WorkerConfiguration{Name: "writer", EnabledMCPServers: []string{"local"}, ContextBudget: 8192}}}
	root.Data.AllSkills = true
	if _, err := s.UpdateRuntimeConfig(ctx, root); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, root.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	body := "old content"
	opts := CreateRunOptions{SnapshotSkills: func(bool, []string) ([]skills.Snapshot, error) {
		return []skills.Snapshot{{Name: "note", Content: body}}, nil
	}}
	run, _, err := s.CreateRunWithOptions(ctx, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	body = "new content"
	frozen, err := s.RunResources(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Workers["writer"].ID != root.ID || frozen.Root.Skills[0].Content != "old content" || frozen.Workers["writer"].Skills[0].Content != "old content" {
		t.Fatal("Skill change mutated running snapshot")
	}
	if err := s.SetRunStatus(ctx, run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.CreateRunWithOptions(ctx, c.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.RunResources(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Root.Skills[0].Content != "new content" || updated.Workers["writer"].Skills[0].Content != "new content" {
		t.Fatal("next run did not capture new Skill content")
	}
}

func TestWorkerProjectionEmptyIntersectionDeniesTools(t *testing.T) {
	root, source := DefaultRuntimeConfig(), DefaultRuntimeConfig()
	root.AllowedTools = []string{"local__read_file"}
	source.AllowedTools = []string{"local__list_directory"}
	child, err := WorkerProjection(root, source)
	if err != nil {
		t.Fatal(err)
	}
	if child.AllowsTool("local__read_file") || child.AllowsTool("local__list_directory") {
		t.Fatal("empty capability intersection allowed tools")
	}
}
