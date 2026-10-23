package storage

import (
	"context"
	"testing"
)

const testContextID = "test-context"

func testOpen(path string) (*Store, error) {
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO contexts(id,name,policy,created_at,updated_at) VALUES(?, 'Test context', '{"approvalMode":"semi","permissions":{"unrestricted":true}}', '', '')`, testContextID)
	return s, err
}
func (s *Store) CreateSession(ctx context.Context, title string) (Session, error) {
	return s.CreateContextSession(ctx, testContextID, DefaultConfigID, title)
}
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	return s.ListContextSessions(ctx, testContextID)
}
func newTestContext(t *testing.T, s *Store, name, profile string) LocalContext {
	t.Helper()
	c, err := s.CreateConversation(context.Background(), profile, "", name)
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.GetLocalContext(context.Background(), c.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	return local
}
