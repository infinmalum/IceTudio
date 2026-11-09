package ipc

import (
	"context"
	"encoding/json"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"strings"
	"testing"
)

type skillDraftChat struct{}

func (skillDraftChat) Chat(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	text := "原任务观察"
	if strings.Contains(messages[0].Content, "improver Skill") {
		p := storage.SkillProposal{Outcome: "reusable", Content: "---\nname: ipc-skill\ndescription: Review IPC tasks.\n---\nCheck the task output and report unknown results.\n", Summary: "IPC Skill候选", ExpectedChecks: []string{"未知结果不当作成功"}, EvidenceRefs: []string{"request"}}
		raw, _ := json.Marshal(p)
		text = string(raw)
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: text}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestSkillDraftOverIPC(t *testing.T) {
	s, _ := newTestHost(t, skillDraftChat{}, &blockingTools{})
	client := mustConnect(t, startServer(t, s))
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.StartConversationTask(c.ID, "观察一个任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.WaitIdle()
	if err := client.Call("skillDrafts.start", map[string]any{"sessionId": c.ID, "request": "创建复核步骤", "evidenceIds": []string{target.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	s.WaitIdle()
	var drafts []storage.SkillDraft
	if err := client.Call("skillDrafts.list", nil, &drafts); err != nil || len(drafts) != 1 || len(drafts[0].Evidence) != 1 || drafts[0].Evidence[0].RootRunID != target.ID {
		t.Fatal(drafts, err)
	}
	d := drafts[0]
	if err := client.Call("skillDrafts.apply", map[string]any{"id": d.ID}, nil); err == nil {
		t.Fatal("adopted without inspected revision")
	}
	if err := client.Call("skillDrafts.apply", map[string]any{"id": d.ID, "revision": 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("skillDrafts.withdraw", map[string]any{"id": d.ID, "revision": 2}, nil); err != nil {
		t.Fatal(err)
	}
	if err = client.Call("skillDrafts.start", map[string]any{"sessionId": c.ID, "request": "Skill", "agentId": "obsolete"}, nil); err == nil {
		t.Fatal("obsolete parameter accepted")
	}
}
