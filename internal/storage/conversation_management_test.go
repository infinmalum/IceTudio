package storage

import (
	"context"
	"testing"
)

func TestConversationNamingDeletionAndSharedContext(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a, err := s.CreateConversation(ctx, DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateConversation(ctx, DefaultConfigID, a.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, a.ID, "", RoleUser, "  整理\n这些材料  "); err != nil {
		t.Fatal(err)
	}
	named, err := s.GetConversation(ctx, a.ID)
	if err != nil || named.Title != "整理 这些材料" {
		t.Fatal(named, err)
	}
	if _, err = s.RenameConversation(ctx, a.ID, "我的报告"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, a.ID, "", RoleUser, "another"); err != nil {
		t.Fatal(err)
	}
	named, _ = s.GetConversation(ctx, a.ID)
	if named.Title != "我的报告" {
		t.Fatal("overwrote user title")
	}
	if _, err = s.RenameConversation(ctx, a.ID, " "); err == nil {
		t.Fatal("empty title accepted")
	}
	if err = s.DeleteConversation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetConversation(ctx, a.ID); err == nil {
		t.Fatal("not deleted")
	}
	if _, err = s.GetConversation(ctx, b.ID); err != nil {
		t.Fatal("shared conversation removed", err)
	}
}

func TestConversationDeletionRejectsActiveAndRemovesFinishedTaskGraph(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "fixed", fixedData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteConversation(ctx, c.ID); err == nil {
		t.Fatal("deleted running task")
	}
	child, err := createPlannedRun(t, s, c, root, p.Data.Execution.Stages[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, child.SessionID, child.ID, RoleAssistant, "交付"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, child.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, root.ID, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteConversation(ctx, c.ID); err != nil {
		t.Fatal("could not remove graph", err)
	}
	if _, err = s.GetSession(ctx, child.SessionID); err == nil {
		t.Fatal("orphan child session")
	}
	if _, err = s.GetRun(ctx, child.ID); err == nil {
		t.Fatal("orphan child run")
	}
}

func TestConversationDeletionWithContinuationInput(t *testing.T) {
	s, _, r := waitingFixed(t)
	ctx := context.Background()
	if _, _, err := s.ContinueFixedRun(ctx, r.ID, "collect", "answer", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunStatus(ctx, r.ID, "interrupted", "closed"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(ctx, r.SessionID); err != nil {
		t.Fatal("continuation references blocked deletion", err)
	}
}

func TestChatMessagePaginationStableOrderAndToolExclusion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	c, err := s.CreateConversation(ctx, DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err = s.AddMessage(ctx, c.ID, "", RoleUser, "message"); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ChatMessages(ctx, c.ID, 0, 5)
	if err != nil || len(page) != 5 || page[0].Seq != 8 || page[4].Seq != 12 {
		t.Fatal(page, err)
	}
	older, err := s.ChatMessages(ctx, c.ID, page[0].Seq, 5)
	if err != nil || len(older) != 5 || older[0].Seq != 3 || older[4].Seq != 7 {
		t.Fatal(older, err)
	}
	all, err := s.ListMessages(ctx, c.ID)
	if err != nil || len(all) != 12 {
		t.Fatal("CLI history changed", all, err)
	}
	if _, err = s.ChatMessages(ctx, c.ID, -1, 5); err == nil {
		t.Fatal("invalid boundary accepted")
	}
}

func TestDeletionRetainsUnknownNativeRequest(t *testing.T) {
	s, c, root := searchRoot(t, 5)
	ctx := context.Background()
	if _, err := s.BeginNativeSearch(ctx, root.ID, "openai"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(ctx, c.ID); err == nil {
		t.Fatal("unknown native request erased")
	}
}
