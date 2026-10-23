package app

import (
	"context"
	"icetudio/internal/storage"
	"strings"
	"testing"
)

func evidenceTarget(t *testing.T, s *Service, c storage.Conversation) (storage.Run, storage.ToolCall) {
	t.Helper()
	ctx := context.Background()
	r, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AddMessage(ctx, c.ID, r.ID, storage.RoleUser, "SELECTED_TASK_OBSERVATION"); err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.store.AddAssistantMessage(ctx, c.ID, r.ID, "", []storage.MessageToolCall{{ID: "unknown", Name: "remote__write", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.store.FinishToolCall(ctx, calls[0].ID, storage.CallExecutionUnknown, "", "响应丢失", storage.ScopeCredential); err != nil {
		t.Fatal(err)
	}
	if err = s.runs.Finish(r.ID, "failed", "未知效果"); err != nil {
		t.Fatal(err)
	}
	return r, calls[0]
}

// Task evidence stays context-scoped, ended-only and duplicate-free. The same
// storage rules now serve managed Skill drafts.
func TestSkillDraftRejectsInvalidTaskEvidence(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, &skillDraftModel{}, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := evidenceTarget(t, s, c)
	other, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSkillDraft(other.ID, "", "跨上下文", []string{target.ID}); err == nil {
		t.Fatal("cross context evidence allowed")
	}
	if _, err = s.StartSkillDraft(c.ID, "", "重复", []string{target.ID, target.ID}); err == nil {
		t.Fatal("duplicate evidence allowed")
	}
	r, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSkillDraft(c.ID, "", "运行中", []string{r.ID}); err == nil {
		t.Fatal("active evidence allowed")
	}
	_ = s.runs.Finish(r.ID, "failed", "fixture")
}

// A managed Skill carries source task scopes into independent frozen executions.
func TestManagedSkillCarriesSourceScopesAcrossReuse(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, &skillDraftModel{}, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AddMessage(ctx, c.ID, "", storage.RoleUser, "UNRELATED_HISTORY_SECRET"); err != nil {
		t.Fatal(err)
	}
	target, _ := evidenceTarget(t, s, c)
	r, err := s.StartSkillDraft(c.ID, "", "从选定任务提取方法", []string{target.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	skillDrafts, _ := s.ListSkillDrafts()
	copy, err := s.ApplySkillDraft(skillDrafts[0].ID, skillDrafts[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(copy.Scopes, ","), storage.ScopeCredential) {
		t.Fatal("Skill publication lost source scope", copy.Scopes)
	}
	clean := storage.DefaultRuntimeConfig()
	clean.AllSkills = false
	clean.EnabledSkills = []string{copy.Name}
	if _, err = s.configureDefault(clean); err != nil {
		t.Fatal(err)
	}
	independent, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, _, resolved, err := s.runs.StartPrepared(ctx, independent.ID, s.config.Model, s.skills.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := s.sessionScopes(ctx, independent.ID, resolved)
	if err != nil || !strings.Contains(strings.Join(scopes, ","), storage.ScopeCredential) {
		t.Fatal("Skill provenance lost on frozen execution", scopes, err)
	}
	_ = s.runs.Finish(run.ID, "failed", "fixture")
}
