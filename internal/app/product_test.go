package app

import (
	"context"
	"encoding/base64"
	"icetudio/internal/materials"
	"slices"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/storage"
)

func TestSendPreviewActualDestinationsScopesAndNoImplicitGrant(t *testing.T) {
	s, store := newTestService(t, &scriptModel{}, &scriptTools{})
	data := storage.DefaultRuntimeConfig()
	data.Model = config.Model{Provider: "openai", BaseURL: "https://proxy.example/v1", Name: "mock"}
	data.Execution.ImproverEnabled = true
	data.Execution.Improver.Model = &config.Model{Provider: "anthropic", BaseURL: "https://draft.example", Name: "mock"}
	p, err := s.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewSend(c.ID, true, "main")
	if err != nil || len(preview) != 1 {
		t.Fatal(preview, err)
	}
	first := preview[0]
	if first.Destination != "https://proxy.example" || !slices.Contains(first.Missing, storage.ScopePersonal) || !slices.Contains(first.Missing, storage.ScopeUnknown) || slices.Contains(first.Scopes, storage.ScopeCredential) {
		t.Fatal("wrong consent", first)
	}
	grants, err := s.ListExfilGrants(c.ContextID)
	if err != nil || len(grants) != 0 {
		t.Fatal("preview granted permission", grants, err)
	}
	if _, err = s.GrantExfil(c.ContextID, first.Destination, first.Scopes); err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewSend(c.ID, true, "main")
	if err != nil || len(preview[0].Missing) != 0 {
		t.Fatal(preview, err)
	}
	drafts, err := s.PreviewSend(c.ID, false, "improver")
	if err != nil || len(drafts) != 1 || drafts[0].Destination != "https://draft.example" || !slices.Contains(drafts[0].Scopes, storage.ScopeWorkspace) {
		t.Fatal("improver destination", drafts, err)
	}
	// Preview creates neither tasks nor conversation messages and makes no model calls.
	runs, err := store.ListRuns(context.Background(), c.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal(runs, err)
	}
	if _, err = s.PreviewSend(c.ID, false, "invalid"); err == nil {
		t.Fatal("invalid component accepted")
	}
}

func TestAttachmentAccessMetadataAndActualContent(t *testing.T) {
	s, store := newTestService(t, &scriptModel{}, &scriptTools{})
	c, err := s.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	files, err := materials.Prepare([]materials.Upload{{Name: "note.txt", Data: base64.StdEncoding.EncodeToString([]byte("实际附件正文"))}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := store.AddMessage(context.Background(), c.ID, "", storage.RoleUser, "content", files...)
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.MessageAttachment(message.ID, 0)
	if err != nil || file.Text != "实际附件正文" {
		t.Fatal(file, err)
	}
	history, err := s.ListMessages(c.ID)
	if err != nil || history[0].Attachments[0].Text != "" {
		t.Fatal("history returned full material", history, err)
	}
	if _, err = s.MessageAttachment(message.ID, 1); err == nil {
		t.Fatal("nonexistent attachment accepted")
	}
	if _, err = s.MessageAttachment(message.ID, -1); err == nil {
		t.Fatal("negative attachment accepted")
	}
}
