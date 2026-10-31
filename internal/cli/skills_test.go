package cli

import (
	"testing"
	"time"

	"icetudio/internal/storage"
)

func TestSkillsCLIManageAdoptedCopiesAndEvaluations(t *testing.T) {
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
	if err != nil || len(drafts) != 1 {
		t.Fatal(drafts, err)
	}
	if code := dispatch(client, []string{"skill-drafts", "apply", drafts[0].ID, "--revision", "1"}); code != 0 {
		t.Fatal(code)
	}
	d, err := s.ListSkillDrafts()
	if err != nil || len(d) != 1 || d[0].Published.Name == "" {
		t.Fatal(d, err)
	}
	copyName := d[0].Published.Name

	if code := dispatch(client, []string{"skills", "list"}); code != 0 {
		t.Fatal(code)
	}
	var infos []map[string]any
	if err = client.Call("skills.list", nil, &infos); err != nil || len(infos) != 1 {
		t.Fatal(infos, err)
	}
	if infos[0]["name"] != copyName || infos[0]["disabled"] == true {
		t.Fatal(infos)
	}
	if code := dispatch(client, []string{"skills", "disable", copyName}); code != 0 {
		t.Fatal(code)
	}
	if err = client.Call("skills.list", nil, &infos); err != nil || infos[0]["disabled"] != true {
		t.Fatal("disable not visible over IPC", infos, err)
	}
	if code := dispatch(client, []string{"skills", "enable", copyName}); code != 0 {
		t.Fatal(code)
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
	if code := dispatch(client, []string{"skills", "feedback", c.ID}); code != 0 {
		t.Fatal(code)
	}

	if code := dispatch(client, []string{"skills", "evaluate", copyName, c.ID, "amazing"}); code == 0 {
		t.Fatal("invalid outcome accepted")
	}
	if code := dispatch(client, []string{"skills", "evaluate", copyName, c.ID, "worked", "--note", "贴合任务"}); code != 0 {
		t.Fatal(code)
	}
	if code := dispatch(client, []string{"skills", "evaluations", copyName}); code != 0 {
		t.Fatal(code)
	}
	evals, err := s.ListSkillEvaluations(copyName)
	if err != nil || len(evals) != 1 || evals[0].Note != "贴合任务" {
		t.Fatal(evals, err)
	}
}
