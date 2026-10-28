package cli

import (
	"context"
	"encoding/json"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"strings"
	"testing"
	"time"
)

type cliSkillModel struct{}

func (cliSkillModel) Chat(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	text := "原任务观察"
	if strings.Contains(messages[0].Content, "improver Skill") {
		p := storage.SkillProposal{Content: "---\nname: cli-skill\ndescription: Review CLI tasks.\n---\nCheck the task output and report unknown results.\n", Summary: "CLI Skill候选", ExpectedChecks: []string{"未知结果不当作成功"}, EvidenceRefs: []string{"request"}}
		raw, _ := json.Marshal(p)
		text = string(raw)
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: text}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestSkillDraftCLI(t *testing.T) {
	client, s := newCLIHost(t, cliSkillModel{}, time.Second)
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.StartConversationTask(c.ID, "观察一个任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.WaitIdle()
	if code := dispatch(client, []string{"skill-drafts", "generate", c.ID, "创建复核步骤", "--evidence", target.ID}); code != 0 {
		t.Fatal(code)
	}
	s.WaitIdle()
	drafts, err := s.ListSkillDrafts()
	if err != nil || len(drafts) != 1 || len(drafts[0].Evidence) != 1 || drafts[0].Evidence[0].RootRunID != target.ID {
		t.Fatal(drafts, err)
	}
	d := drafts[0]
	if code := dispatch(client, []string{"skill-drafts", "show", d.ID}); code != 0 {
		t.Fatal(code)
	}
	if code := dispatch(client, []string{"skill-drafts", "apply", d.ID}); code == 0 {
		t.Fatal("adopted without inspected revision")
	}
	if code := dispatch(client, []string{"skill-drafts", "apply", d.ID, "--revision", "1"}); code != 0 {
		t.Fatal(code)
	}
	if code := dispatch(client, []string{"skill-drafts", "withdraw", d.ID, "--revision", "2"}); code != 0 {
		t.Fatal(code)
	}
	if err = client.Call("skillDrafts.start", map[string]any{"sessionId": c.ID, "request": "Skill", "agentId": "obsolete"}, nil); err == nil {
		t.Fatal("obsolete parameter accepted")
	}
}
