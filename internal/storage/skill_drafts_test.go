package storage

import (
	"context"
	"icetudio/internal/skills"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSkillDraftAtomicPublicationPersistenceAndSingleAdoption(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "skill.db")
	s, err := testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{Component: "improver"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveDraftEvidence(ctx, r.ID, []DraftEvidence{}); err != nil {
		t.Fatal(err)
	}
	proposal := SkillProposal{Content: "---\nname: review-notes\ndescription: Review notes on request.\n---\nRead the notes and list missing facts.\n", Summary: "笔记复核", EvidenceRefs: []string{"request"}}
	d, err := s.SaveSkillDraft(ctx, r.ID, skills.Snapshot{}, "复核笔记", proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplySkillDraft(ctx, d.ID, d.Revision); err == nil {
		t.Fatal("running generator adopted")
	}
	if err = s.SetRunStatus(ctx, r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.GetSkillDraft(ctx, d.ID)
	if err != nil || d.Proposal.Content != proposal.Content {
		t.Fatal(d, err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_skill BEFORE UPDATE ON skill_drafts WHEN NEW.state='applied' BEGIN SELECT RAISE(ABORT,'reject publication'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplySkillDraft(ctx, d.ID, d.Revision); err == nil {
		t.Fatal("publication failure ignored")
	}
	after, _ := s.GetSkillDraft(ctx, d.ID)
	if after.State != "draft" || after.Published.Name != "" {
		t.Fatal(after)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_skill`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var adopted atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ApplySkillDraft(ctx, d.ID, d.Revision); err == nil {
				adopted.Add(1)
			}
		}()
	}
	wg.Wait()
	if adopted.Load() != 1 {
		t.Fatal("duplicate adoption", adopted.Load())
	}
	d, _ = s.GetSkillDraft(ctx, d.ID)
	if d.Published.Name == "review-notes" || d.Published.SHA256 == "" {
		t.Fatal(d)
	}
	if err = s.WithdrawSkillDraft(ctx, d.ID, d.Revision); err != nil {
		t.Fatal(err)
	}
	d, _ = s.GetSkillDraft(ctx, d.ID)
	if d.State != "withdrawn" {
		t.Fatal(d)
	}
}
