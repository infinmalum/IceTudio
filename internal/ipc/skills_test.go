package ipc

import (
	"testing"

	"icetudio/internal/storage"
)

func TestSkillsManageAdoptedCopiesAndEvaluationsOverIPC(t *testing.T) {
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
	if err := client.Call("skillDrafts.list", nil, &drafts); err != nil || len(drafts) != 1 {
		t.Fatal(drafts, err)
	}
	if err := client.Call("skillDrafts.apply", map[string]any{"id": drafts[0].ID, "revision": 1}, nil); err != nil {
		t.Fatal(err)
	}
	d, err := s.ListSkillDrafts()
	if err != nil || len(d) != 1 || d[0].Published.Name == "" {
		t.Fatal(d, err)
	}
	copyName := d[0].Published.Name

	var infos []map[string]any
	if err = client.Call("skills.list", nil, &infos); err != nil || len(infos) != 1 {
		t.Fatal(infos, err)
	}
	if infos[0]["name"] != copyName || infos[0]["disabled"] == true {
		t.Fatal(infos)
	}
	if err := client.Call("skills.setDisabled", map[string]any{"skillName": copyName, "disabled": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err = client.Call("skills.list", nil, &infos); err != nil || infos[0]["disabled"] != true {
		t.Fatal("disable not visible over IPC", infos, err)
	}
	if err := client.Call("skills.setDisabled", map[string]any{"skillName": copyName, "disabled": false}, nil); err != nil {
		t.Fatal(err)
	}

	page, err := s.ListChatMessages(c.ID, 0, 40)
	if err != nil {
		t.Fatal(err)
	}
	var messageID string
	for _, m := range page.Messages {
		if m.Role == "user" && m.Content != "" {
			messageID = m.ID
			break
		}
	}
	if messageID == "" {
		t.Fatal("no user message for feedback")
	}
	if err = client.Call("feedback.record", map[string]any{"sessionId": c.ID, "messageId": messageID, "note": "方法偏好"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("feedback.list", map[string]any{"sessionId": c.ID}, nil); err != nil {
		t.Fatal(err)
	}

	if err := client.Call("skillEvaluations.record", map[string]any{"skillName": copyName, "sessionId": c.ID, "outcome": "amazing"}, nil); err == nil {
		t.Fatal("invalid outcome accepted")
	}
	if err := client.Call("skillEvaluations.record", map[string]any{"skillName": copyName, "sessionId": c.ID, "outcome": "worked", "note": "贴合任务"}, nil); err != nil {
		t.Fatal(err)
	}
	var evals []storage.SkillEvaluation
	if err := client.Call("skillEvaluations.list", map[string]any{"skillName": copyName}, &evals); err != nil || len(evals) != 1 || evals[0].Note != "贴合任务" {
		t.Fatal(evals, err)
	}
}
