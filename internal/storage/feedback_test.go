package storage

import (
	"context"
	"path/filepath"
	"testing"

	"icetudio/internal/skills"
)

func TestFeedbackNotesLifecycleGuardsAndDraftBinding(t *testing.T) {
	ctx := context.Background()
	s, err := testOpen(filepath.Join(t.TempDir(), "feedback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.AddMessage(ctx, c.ID, "", RoleUser, "以后这类伴奏都更稀疏一些")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := s.AddMessage(ctx, c.ID, "", RoleAssistant, "好的")
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.SaveFeedbackNote(ctx, c.ID, user.ID, "", "长期偏好")
	if err != nil {
		t.Fatal(err)
	}
	if note.Quote != user.Content || note.State != "recorded" || note.ProjectID != "" || note.RunID != "" {
		t.Fatal(note)
	}
	if _, err = s.SaveFeedbackNote(ctx, c.ID, assistant.ID, "", ""); err == nil {
		t.Fatal("assistant message recorded as feedback")
	}
	if _, err = s.SaveFeedbackNote(ctx, "other-session", user.ID, "", ""); err == nil {
		t.Fatal("cross-session message recorded")
	}
	if _, err = s.SaveFeedbackNote(ctx, c.ID, "missing", "", ""); err == nil {
		t.Fatal("missing message recorded")
	}
	if _, err = s.SaveFeedbackNote(ctx, c.ID, user.ID, "", string(make([]byte, 2001))); err == nil {
		t.Fatal("oversized note accepted")
	}
	// Task linkage falls back to the active run only when the message has none.
	run, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{Component: "main"})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.SaveFeedbackNote(ctx, c.ID, user.ID, run.ID, "")
	if err != nil || linked.RunID != run.ID {
		t.Fatal(linked, err)
	}
	listed, err := s.ListFeedbackNotes(ctx, c.ID)
	if err != nil || len(listed) != 2 {
		t.Fatal(listed, err)
	}
	if err = s.DismissFeedbackNote(ctx, c.ID, note.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DismissFeedbackNote(ctx, c.ID, note.ID); err == nil {
		t.Fatal("double dismiss accepted")
	}
	if err = s.DismissFeedbackNote(ctx, "other-session", note.ID); err == nil {
		t.Fatal("cross-session dismiss accepted")
	}
	// Deleting the conversation cascades its notes; no cross-conversation memory survives.
	if err = s.SetRunStatus(ctx, run.ID, "failed", "fixture ended"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteConversation(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	listed, err = s.ListFeedbackNotes(ctx, c.ID)
	if err != nil || len(listed) != 0 {
		t.Fatal("cascade delete missing", listed, err)
	}
}

func TestDeclinedSkillDraftStoresFeedbackAndSatisfiesReview(t *testing.T) {
	ctx := context.Background()
	s, err := testOpen(filepath.Join(t.TempDir(), "declined.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.AddMessage(ctx, c.ID, "", RoleUser, "把这段旋律改成三连音")
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.SaveFeedbackNote(ctx, c.ID, user.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{Component: "improver"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveDraftEvidence(ctx, run.ID, []DraftEvidence{}); err != nil {
		t.Fatal(err)
	}
	declined := SkillProposal{Outcome: "one_off", Summary: "只影响这一个片段", Reasons: []string{"没有可复用的步骤"}}
	d, err := s.SaveDeclinedSkillDraft(ctx, run.ID, skills.Snapshot{}, "三连音请求", declined, []string{note.ID})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "declined" || len(d.FeedbackIDs) != 1 || len(d.Feedback) != 1 || d.Feedback[0].Quote != user.Content {
		t.Fatal(d)
	}
	if _, err = s.SaveDeclinedSkillDraft(ctx, run.ID, skills.Snapshot{}, "三连音请求", SkillProposal{Outcome: "one_off", Summary: "带内容", Content: "---\nname: x\ndescription: y\n---\nz", Reasons: []string{"r"}}, nil); err == nil {
		t.Fatal("declined proposal carried SKILL.md content")
	}
	if _, err = s.SaveSkillDraft(ctx, run.ID, skills.Snapshot{}, "r", SkillProposal{Outcome: "reusable", Summary: "s", Reasons: []string{"r"}}, nil); err == nil {
		t.Fatal("reusable proposal without content accepted")
	}
	if err = s.SetRunStatus(ctx, run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	run2, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{Component: "improver"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveDraftEvidence(ctx, run2.ID, []DraftEvidence{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveDeclinedSkillDraft(ctx, run2.ID, skills.Snapshot{}, "r", SkillProposal{Outcome: "one_off", Summary: "s", Reasons: []string{"r"}}, []string{note.ID, note.ID}); err != nil {
		t.Fatal("duplicate feedback id should dedupe", err)
	}
	if _, err = s.SaveDeclinedSkillDraft(ctx, run.ID, skills.Snapshot{}, "r", SkillProposal{Outcome: "one_off", Summary: "s", Reasons: []string{"r"}}, []string{"missing"}); err == nil {
		t.Fatal("unknown feedback accepted")
	}
	// A dismissed note can no longer seed a candidate.
	if err = s.DismissFeedbackNote(ctx, c.ID, note.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveDeclinedSkillDraft(ctx, run.ID, skills.Snapshot{}, "r", SkillProposal{Outcome: "one_off", Summary: "s", Reasons: []string{"r"}}, []string{note.ID}); err == nil {
		t.Fatal("dismissed feedback accepted")
	}
	// Declined rows persist with review-compatible state and hydrated feedback.
	drafts, err := s.ListSkillDrafts(ctx)
	if err != nil || len(drafts) != 2 || drafts[0].State != "declined" || len(drafts[0].Feedback) != 1 {
		t.Fatal(drafts, err)
	}
}

func TestSkillStatesAndEvaluations(t *testing.T) {
	ctx := context.Background()
	s, err := testOpen(filepath.Join(t.TempDir(), "states.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{Component: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSkillState(ctx, "comping-style", true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSkillState(ctx, "comping-style", false); err != nil {
		t.Fatal(err)
	}
	states, err := s.ListSkillStates(ctx)
	if err != nil || len(states) != 0 {
		t.Fatal("re-enable left disabled row", states, err)
	}
	if err = s.SetSkillState(ctx, "comping-style", true); err != nil {
		t.Fatal(err)
	}
	states, err = s.ListSkillStates(ctx)
	if err != nil || !states["comping-style"] {
		t.Fatal(states, err)
	}
	if err = s.SetSkillState(ctx, "", true); err == nil {
		t.Fatal("empty name accepted")
	}
	e, err := s.SaveSkillEvaluation(ctx, "comping-style", c.ID, run.ID, "worked", "更贴合速度")
	if err != nil {
		t.Fatal(err)
	}
	if e.Outcome != "worked" || e.RunID != run.ID {
		t.Fatal(e)
	}
	if _, err = s.SaveSkillEvaluation(ctx, "comping-style", c.ID, run.ID, "improved", ""); err == nil {
		t.Fatal("invalid outcome accepted")
	}
	if _, err = s.SaveSkillEvaluation(ctx, "comping-style", "missing", "", "worked", ""); err == nil {
		t.Fatal("missing session accepted")
	}
	other, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSkillEvaluation(ctx, "comping-style", other.ID, run.ID, "worked", ""); err == nil {
		t.Fatal("cross-session run accepted")
	}
	listed, err := s.ListSkillEvaluations(ctx, "comping-style")
	if err != nil || len(listed) != 1 {
		t.Fatal(listed, err)
	}
}
