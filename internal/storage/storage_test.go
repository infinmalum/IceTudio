package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistenceAndRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "icetudio.db")
	store, err := testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.AddMessage(ctx, session.ID, "", RoleUser, "hello")
	if err != nil || first.Seq != 1 {
		t.Fatalf("first message: %+v, %v", first, err)
	}
	run, _, err := store.CreateRun(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddMessage(ctx, session.ID, run.ID, RoleUser, "again")
	if err != nil || second.Seq != 2 || second.RunID != run.ID {
		t.Fatalf("second message: %+v, %v", second, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions: %v, %v", info, err)
	}
	store, err = testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.ListSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("sessions: %+v, %v", sessions, err)
	}
	messages, err := store.ListMessages(ctx, session.ID)
	if err != nil || len(messages) != 2 || messages[0].Content != "hello" || messages[1].Seq != 2 {
		t.Fatalf("messages: %+v, %v", messages, err)
	}
	recovered, err := store.GetRun(ctx, run.ID)
	if err != nil || recovered.Status != "interrupted" {
		t.Fatalf("run: %+v, %v", recovered, err)
	}
	if _, err := store.AddMessage(ctx, "missing", "", RoleUser, "x"); err == nil {
		t.Fatal("message for missing session accepted")
	}
	if _, err := store.AddMessage(ctx, session.ID, "", "tool", "x"); err == nil {
		t.Fatal("free-form tool message accepted")
	}
}

func TestDoubleRunRejected(t *testing.T) {
	ctx := context.Background()
	store, err := testOpen(filepath.Join(t.TempDir(), "double.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateRun(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateRun(ctx, session.ID); err == nil || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("second active run accepted: %v", err)
	}
}
