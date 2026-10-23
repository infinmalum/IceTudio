package app

import (
	"context"
	"encoding/base64"
	"icetudio/internal/materials"
	"icetudio/internal/model"
	"testing"
)

func TestConversationAttachmentHistoryAndEvidence(t *testing.T) {
	check := func(messages []model.Message) (model.ChatResponse, error) {
		found := false
		for _, m := range messages {
			for _, f := range m.Files {
				if f.Name == "note.txt" && f.Text == "冻结附件正文" {
					found = true
				}
			}
		}
		if !found {
			t.Error("model lost attachment")
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "已分析材料"}}, nil
	}
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){check, check}}
	svc, store := newTestService(t, chat, &scriptTools{})
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "", materials.Upload{Name: "note.txt", Data: base64.StdEncoding.EncodeToString([]byte("冻结附件正文"))})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	history, err := svc.ListMessages(session.ID)
	if err != nil || len(history[0].Attachments) != 1 {
		t.Fatalf("%+v %v", history, err)
	}
	f := history[0].Attachments[0]
	if f.Text != "" || f.Data != "" || f.SHA256 == "" {
		t.Fatal("UI must receive metadata only", f)
	}
	files, err := store.RunInputFiles(context.Background(), run.ID)
	if err != nil || len(files) != 1 || files[0].Text != "冻结附件正文" {
		t.Fatalf("%+v %v", files, err)
	}
	snap, err := store.ReviewSnapshot(context.Background(), run.ID)
	if err != nil || len(snap.Attachments) != 1 {
		t.Fatalf("review binding lost attachments: %+v %v", snap, err)
	}
	next, err := svc.StartConversationTask(session.ID, "继续分析")
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, next.ID, "completed")
	if _, err := svc.StartConversationTask(session.ID, "bad", materials.Upload{Name: "bad", Data: "invalid"}); err == nil {
		t.Fatal("bad upload accepted")
	}
}
