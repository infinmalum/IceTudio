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

const testSkillContent = "---\nname: explain-steps\ndescription: Explain a concept for beginners.\n---\n# Steps\nAsk for the topic. Give one example. Verify that the example matches the explanation.\n"

type skillDraftModel struct {
	bad   string
	calls int
}

func (m *skillDraftModel) Chat(_ context.Context, _ config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	m.calls++
	if len(tools) != 0 || len(messages) != 2 || !strings.Contains(messages[0].Content, skillDraftContract) {
		panic("Skill improver not isolated")
	}
	if strings.Contains(messages[1].Content, "UNRELATED_HISTORY_SECRET") {
		panic("implicit history sent")
	}
	p := storage.SkillProposal{Content: testSkillContent, Summary: "加入分步解释", Reasons: []string{"用户需求"}, Limitations: []string{"未验收，请检查私人信息"}, ExpectedChecks: []string{"回答包含一个相关示例"}, EvidenceRefs: []string{"request"}}
	if m.bad == "yaml" {
		p.Content = "not a SKILL.md"
	}
	if m.bad == "reference" {
		p.EvidenceRefs = []string{"call:invented"}
	}
	raw, _ := json.Marshal(p)
	answer := model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 10}}
	if m.bad == "tools" {
		answer.Message.ToolCalls = []model.ToolCall{extToolCall("bad", "local__exec", "{}")}
	}
	return answer, nil
}
func TestSkillDraftCopyPublicationFrozenContentRestart(t *testing.T) {
	ctx := context.Background()
	m := &skillDraftModel{}
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
	data.AllSkills = false
	data.EnabledSkills = []string{"explain-steps"}
	p, err := s.store.CreateRuntimeConfig(ctx, "草稿引用配置", data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMessage(ctx, c.ID, "", storage.RoleUser, "UNRELATED_HISTORY_SECRET"); err != nil {
		t.Fatal(err)
	}
	oldRoot, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	oldResources, err := store.RunResources(ctx, oldRoot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runs.Finish(oldRoot.ID, "failed", "fixture ended"); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartSkillDraft(c.ID, "explain-steps", "改进解释方法", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	drafts, err := s.ListSkillDrafts()
	if err != nil || len(drafts) != 1 || len(drafts[0].Impact) == 0 || len(drafts[0].AllSkillsImpact) == 0 {
		t.Fatal(drafts, err)
	}
	d := drafts[0]
	before, _ := catalog.Load("explain-steps")
	if before != testSkillContent || len(catalog.Items()) != 1 {
		t.Fatal("generation changed catalog")
	}
	proposal := d.Proposal
	proposal.Content = strings.Replace(testSkillContent, "Give one example.", "Give two examples.", 1)
	edited, err := s.EditSkillDraft(d.ID, d.Revision, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplySkillDraft(d.ID, d.Revision); err == nil {
		t.Fatal("stale Skill candidate adopted")
	}
	copy, err := s.ApplySkillDraft(d.ID, edited.Revision)
	if err != nil || copy.Name == "explain-steps" {
		t.Fatal(copy, err)
	}
	loaded, err := catalog.Load(copy.Name)
	if err != nil || loaded != copy.Content {
		t.Fatal(loaded, err)
	}
	original, _ := catalog.Load("explain-steps")
	if original != before {
		t.Fatal("shared source overwritten")
	}
	current, _ := s.GetRuntimeConfig(p.ID)
	if current.Revision != p.Revision || current.Data.EnabledSkills[0] != "explain-steps" {
		t.Fatal("references switched", current)
	}
	retained, _ := store.RunResources(ctx, oldRoot.ID)
	if retained.Root.Skills[0].SHA256 != oldResources.Root.Skills[0].SHA256 {
		t.Fatal("old snapshot changed")
	}
	current.Data.EnabledSkills = []string{copy.Name}
	current, err = s.UpdateRuntimeConfig(current)
	if err != nil {
		t.Fatal(err)
	}
	run, _, _, err := s.runs.StartPrepared(ctx, c.ID, s.config.Model, s.skills.Snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := store.RunResources(ctx, run.ID)
	if err != nil || resources.Root.Skills[0].SHA256 != copy.SHA256 {
		t.Fatal(resources, err)
	}
	_ = s.runs.Finish(run.ID, "failed", "fixture")
	empty, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second := New(Options{Store: store, Runs: agent.NewRuns(store), Skills: empty, Tools: &scriptTools{}, Chat: m, Config: s.config})
	t.Cleanup(second.runs.Close)
	if err = second.checkReady(); err != nil {
		t.Fatal(err)
	}
	if got, err := empty.Load(copy.Name); err != nil || got != copy.Content {
		t.Fatal("published copy not restored", err)
	}
}
func TestSkillDraftInvalidOutputAndSourceConflict(t *testing.T) {
	for _, bad := range []string{"yaml", "reference", "tools"} {
		t.Run(bad, func(t *testing.T) {
			m := &skillDraftModel{bad: bad}
			s, _ := newTestService(t, m, &scriptTools{})
			c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.StartSkillDraft(c.ID, "", "新建", nil)
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, s, r.ID, "failed")
			d, err := s.ListSkillDrafts()
			if err != nil || len(d) != 0 {
				t.Fatal(d, err)
			}
		})
	}
}

func TestSkillDraftSourceChangeAndWithdrawal(t *testing.T) {
	s, _ := newTestService(t, &skillDraftModel{}, &scriptTools{})
	root := t.TempDir()
	dir := filepath.Join(root, "explain-steps")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte(testSkillContent), 0600); err != nil {
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
	r, err := s.StartSkillDraft(c.ID, "explain-steps", "改善", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	drafts, _ := s.ListSkillDrafts()
	d := drafts[0]
	if err = os.WriteFile(file, []byte(strings.Replace(testSkillContent, "Give one example.", "Give three examples.", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplySkillDraft(d.ID, d.Revision); err == nil {
		t.Fatal("changed source ignored")
	}
	if err = s.DiscardSkillDraft(d.ID, d.Revision); err != nil {
		t.Fatal(err)
	}
	r, err = s.StartSkillDraft(c.ID, "", "新建", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, r.ID, "completed")
	drafts, _ = s.ListSkillDrafts()
	d = drafts[0]
	copy, err := s.ApplySkillDraft(d.ID, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.store.GetSkillDraft(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := storage.BlankRuntimeConfig()
	data.EnabledSkills = []string{copy.Name}
	if _, err = s.configureDefault(data); err != nil {
		t.Fatal(err)
	}
	if err = s.WithdrawSkillDraft(d.ID, d.Revision); err == nil {
		t.Fatal("withdrawal broke explicit reference")
	}
	data.EnabledSkills = nil
	if _, err = s.configureDefault(data); err != nil {
		t.Fatal(err)
	}
	if err = s.WithdrawSkillDraft(d.ID, d.Revision); err != nil {
		t.Fatal(err)
	}
	if catalog.Installed(copy.Name) {
		t.Fatal("withdrawn copy still available")
	}
	if err = s.restoreManagedSkills(); err != nil {
		t.Fatal(err)
	}
	if catalog.Installed(copy.Name) {
		t.Fatal("withdrawn copy restored")
	}
}
