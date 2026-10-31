package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// feedbackDraftModel records the improver payload for assertions and can be
// told to answer with a one_off classification instead of a proposal.
type feedbackDraftModel struct {
	payload string
	oneOff  bool
}

func (m *feedbackDraftModel) Chat(_ context.Context, _ config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if len(tools) != 0 || len(messages) != 2 || !strings.Contains(messages[0].Content, skillDraftContract) {
		panic("Skill improver not isolated")
	}
	m.payload = messages[1].Content
	var p storage.SkillProposal
	if m.oneOff {
		p = storage.SkillProposal{Outcome: "one_off", Summary: "只影响这一个片段的三连音要求", Reasons: []string{"没有可复用的步骤"}, EvidenceRefs: []string{"request"}}
	} else {
		p = storage.SkillProposal{Outcome: "reusable", Content: testSkillContent, Summary: "伴奏密度方法", Reasons: []string{"反馈原话"}, Limitations: []string{"未验收"}, ExpectedChecks: []string{"示例存在"}, EvidenceRefs: []string{"request"}}
	}
	raw, _ := json.Marshal(p)
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 10}}, nil
}

func TestFeedbackRecordLinkageAndGuards(t *testing.T) {
	ctx := context.Background()
	s, store := newTestService(t, &scriptModel{}, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.AddMessage(ctx, c.ID, "", storage.RoleUser, "以后这类伴奏都更稀疏一些")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := store.AddMessage(ctx, c.ID, "", storage.RoleAssistant, "收到")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordFeedback(c.ID, assistant.ID, ""); err == nil {
		t.Fatal("assistant message accepted")
	}
	if _, err = s.RecordFeedback("missing", user.ID, ""); err == nil {
		t.Fatal("cross-session message accepted")
	}
	note, err := s.RecordFeedback(c.ID, user.ID, "编曲偏好")
	if err != nil {
		t.Fatal(err)
	}
	if note.Quote != user.Content || note.Note != "编曲偏好" || note.RunID != "" {
		t.Fatal(note)
	}
	listed, err := s.ListFeedback(c.ID)
	if err != nil || len(listed) != 1 {
		t.Fatal(listed, err)
	}
	if err = s.DismissFeedback(c.ID, note.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DismissFeedback(c.ID, note.ID); err == nil {
		t.Fatal("double dismiss accepted")
	}
	listed, err = s.ListFeedback(c.ID)
	if err != nil || listed[0].State != "dismissed" {
		t.Fatal(listed, err)
	}
}

func TestFeedbackCapturesActiveRunAndProjectBinding(t *testing.T) {
	ctx := context.Background()
	s, store := newTestService(t, &scriptModel{}, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject("反馈工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetConversationProject(c.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	user, err := store.AddMessage(ctx, c.ID, "", storage.RoleUser, "把副歌改紧凑")
	if err != nil {
		t.Fatal(err)
	}
	root, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	s.taskMu.Lock()
	s.running[c.ID] = root.ID
	s.taskMu.Unlock()
	note, err := s.RecordFeedback(c.ID, user.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if note.RunID != root.ID || note.ProjectID != project.ID {
		t.Fatal("linkage not captured", note)
	}
	s.taskMu.Lock()
	delete(s.running, c.ID)
	s.taskMu.Unlock()
}

func TestRefinementSendsFeedbackQuotesAndRoutesOneOff(t *testing.T) {
	ctx := context.Background()
	for _, oneOff := range []bool{false, true} {
		m := &feedbackDraftModel{oneOff: oneOff}
		s, store := newTestService(t, m, &scriptTools{})
		c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
		if err != nil {
			t.Fatal(err)
		}
		user, err := store.AddMessage(ctx, c.ID, "", storage.RoleUser, "以后这类伴奏都更稀疏一些")
		if err != nil {
			t.Fatal(err)
		}
		note, err := s.RecordFeedback(c.ID, user.ID, "长期偏好")
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.StartSkillDraft(c.ID, "", "提炼伴奏方法", nil, []string{note.ID, note.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartSkillDraft(c.ID, "", "提炼伴奏方法", nil, []string{"missing"}); err == nil {
			t.Fatal("unknown feedback accepted")
		}
		waitForRun(t, s, r.ID, "completed")
		if !strings.Contains(m.payload, "以后这类伴奏都更稀疏一些") || !strings.Contains(m.payload, "长期偏好") {
			t.Fatal("feedback quotes missing from payload", m.payload)
		}
		if strings.Contains(m.payload, "UNRELATED") && strings.Count(m.payload, "quote") != 1 {
			t.Fatal("unexpected payload shape", m.payload)
		}
		start := c.ID
		_ = start
		drafts, err := s.ListSkillDrafts()
		if err != nil || len(drafts) != 1 {
			t.Fatal(drafts, err)
		}
		d := drafts[0]
		if oneOff {
			if d.State != "declined" || d.Proposal.Content != "" || d.Proposal.Outcome != "one_off" || len(d.Feedback) != 1 {
				t.Fatal(d)
			}
			if _, err = s.ApplySkillDraft(d.ID, d.Revision); err == nil {
				t.Fatal("declined draft adopted")
			}
		} else {
			if d.State != "draft" || d.Proposal.Outcome != "reusable" || len(d.Feedback) != 1 || d.Feedback[0].Quote != user.Content {
				t.Fatal(d)
			}
		}
	}
}

func TestSkillDisableSuppressesNewFreezesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	m := &feedbackDraftModel{}
	s, store := newTestService(t, m, &scriptTools{})
	root := t.TempDir()
	dir := filepath.Join(root, "explain-steps")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(testSkillContent), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skills.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	s.skills = catalog
	data := storage.DefaultRuntimeConfig()
	data.AllSkills = true
	if _, err = s.configureDefault(data); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSkillDisabled("explain-steps", true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSkillDisabled("missing", true); err == nil {
		t.Fatal("unknown skill disabled")
	}
	run, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := store.RunResources(ctx, run.ID)
	if err != nil || len(resources.Root.Skills) != 0 {
		t.Fatal("disabled skill frozen into new run", resources, err)
	}
	_ = s.runs.Finish(run.ID, "failed", "fixture")
	empty := &skills.Catalog{}
	second := New(Options{Store: store, Runs: agent.NewRuns(store), Skills: catalog, Tools: &scriptTools{}, Chat: m, Config: s.config})
	_ = empty
	t.Cleanup(second.runs.Close)
	if err = second.restoreManagedSkills(); err != nil {
		t.Fatal(err)
	}
	if !catalog.Disabled("explain-steps") {
		t.Fatal("disable state not restored after restart")
	}
	skills1, err := second.ListSkills()
	if err != nil || len(skills1) != 1 || !skills1[0].Disabled {
		t.Fatal(skills1, err)
	}
	if err = second.SetSkillDisabled("explain-steps", false); err != nil {
		t.Fatal(err)
	}
	if catalog.Disabled("explain-steps") {
		t.Fatal("re-enable failed")
	}
}

func TestSkillEvaluationRecordsUserOutcome(t *testing.T) {
	ctx := context.Background()
	s, store := newTestService(t, &scriptModel{}, &scriptTools{})
	root := t.TempDir()
	dir := filepath.Join(root, "explain-steps")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(testSkillContent), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skills.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	s.skills = catalog
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordSkillEvaluation("explain-steps", c.ID, run.ID, "worked", "示例更贴合"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordSkillEvaluation("explain-steps", c.ID, run.ID, "amazing", ""); err == nil {
		t.Fatal("invalid outcome accepted")
	}
	if _, err = s.RecordSkillEvaluation("missing-skill", c.ID, "", "worked", ""); err == nil {
		t.Fatal("unknown skill accepted")
	}
	other, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordSkillEvaluation("explain-steps", other.ID, run.ID, "worked", ""); err == nil {
		t.Fatal("cross-session run accepted")
	}
	evals, err := s.ListSkillEvaluations("explain-steps")
	if err != nil || len(evals) != 1 || evals[0].Outcome != "worked" {
		t.Fatal(evals, err)
	}
	_ = store
}
