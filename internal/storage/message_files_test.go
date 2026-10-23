package storage

import (
	"context"
	"encoding/base64"
	"icetudio/internal/materials"
	"path/filepath"
	"testing"
)

func TestAttachmentPersistenceAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "files.db")
	s, err := testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, "files")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRun(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := materials.Prepare([]materials.Upload{{Name: "p.pdf", Data: base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\nimmutable"))}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.AddMessage(ctx, session.ID, run.ID, RoleUser, "analyze", files...)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.MessageFiles(ctx, m.ID)
	if err != nil || len(got) != 1 || got[0].Data != files[0].Data {
		t.Fatalf("%+v %v", got, err)
	}
	history, err := s.ListMessages(ctx, session.ID)
	if err != nil || history[0].Attachments[0].Data != "" {
		t.Fatalf("raw bytes leaked: %+v %v", history, err)
	}
}
