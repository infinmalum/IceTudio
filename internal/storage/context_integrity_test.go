package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestContextOwnershipIsDerivedAndImmutable(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "contexts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM contexts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fresh host must not create an implicit context: %d %v", count, err)
	}
	a, err := s.CreateConversation(ctx, DefaultConfigID, "", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateConversation(ctx, DefaultConfigID, "", "B")
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.GetLocalContext(ctx, a.ContextID)
	if err != nil || local.Policy.Permissions.Unrestricted {
		t.Fatalf("new context permission: %+v %v", local, err)
	}
	root, _, err := s.CreateRun(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.AddMessage(ctx, a.ID, root.ID, RoleUser, "private")
	if err != nil || message.ContextID != a.ContextID {
		t.Fatalf("message context: %+v %v", message, err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE sessions SET context_id=? WHERE id=?`, []any{b.ContextID, a.ID}},
		{`UPDATE runs SET session_id=? WHERE id=?`, []any{b.ID, root.ID}},
		{`UPDATE messages SET session_id=? WHERE id=?`, []any{b.ID, message.ID}},
		{`INSERT INTO messages(id,session_id,run_id,role,content,created_at,seq) VALUES('bad',?,?, 'user','bad','',1)`, []any{b.ID, root.ID}},
		{`INSERT INTO runs(id,session_id,parent_run_id,status,created_at,updated_at) VALUES('bad',?,?,'running','','')`, []any{b.ID, root.ID}},
	} {
		if _, err := s.db.ExecContext(ctx, q.sql, q.args...); err == nil {
			t.Fatalf("invalid ownership accepted: %s", q.sql)
		}
	}
	read, err := s.GetRun(ctx, root.ID)
	if err != nil || read.ContextID != a.ContextID {
		t.Fatalf("derived run context: %+v %v", read, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign key integrity: %d %v", count, err)
	}
}

func TestConfigSelectionBelongsToConversation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, err := s.CreateConversation(ctx, DefaultConfigID, "", "A")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateRuntimeConfig(ctx, "other", BlankRuntimeConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateConversation(ctx, p.ID, a.ID, "B")
	if err != nil {
		t.Fatal(err)
	}
	if a.ContextID != b.ContextID {
		t.Fatal("explicit reuse lost context")
	}
	if _, err = s.SelectConversationConfig(ctx, a.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SelectConversationConfig(ctx, b.ID, DefaultConfigID); err != nil {
		t.Fatal(err)
	}
	pa, err := s.ResolveRunConfig(ctx, a.ID)
	if err != nil || pa.ConfigID != p.ID {
		t.Fatal(pa, err)
	}
	pb, err := s.ResolveRunConfig(ctx, b.ID)
	if err != nil || pb.ConfigID != DefaultConfigID {
		t.Fatal(pb, err)
	}
}
