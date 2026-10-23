// Package testfixture builds explicit local contexts for integration tests.
package testfixture

import (
	"context"
	"database/sql"
	"icetudio/internal/storage"
)

const ContextID = "test-context"

func Open(path string) (*storage.Store, error) {
	s, err := storage.Open(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		s.Close()
		return nil, err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT OR IGNORE INTO contexts(id,name,policy,created_at,updated_at) VALUES(?, 'Test context', '{"approvalMode":"semi","permissions":{"unrestricted":true}}', '', '')`, ContextID)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func Session(s *storage.Store, ctx context.Context, title string) (storage.Session, error) {
	return s.CreateContextSession(ctx, ContextID, storage.DefaultConfigID, title)
}
func Context(s *storage.Store, ctx context.Context, name, configID string) (storage.LocalContext, error) {
	c, err := s.CreateConversation(ctx, configID, "", name)
	if err != nil {
		return storage.LocalContext{}, err
	}
	return s.GetLocalContext(ctx, c.ContextID)
}
func ContextSession(s *storage.Store, ctx context.Context, id, title string) (storage.Session, error) {
	cs, err := s.ListContextSessions(ctx, id)
	if err != nil {
		return storage.Session{}, err
	}
	configID := storage.DefaultConfigID
	if len(cs) > 0 {
		c, err := s.GetConversation(ctx, cs[0].ID)
		if err != nil {
			return storage.Session{}, err
		}
		configID = c.ConfigID
	}
	return s.CreateContextSession(ctx, id, configID, title)
}
func RunConfig(s *storage.Store, ctx context.Context, id string) (storage.RunConfigSnapshot, error) {
	cs, err := s.ListContextSessions(ctx, id)
	if err != nil {
		return storage.RunConfigSnapshot{}, err
	}
	if len(cs) > 0 {
		return s.ResolveRunConfig(ctx, cs[0].ID)
	}
	session, err := ContextSession(s, ctx, id, "fixture")
	if err != nil {
		return storage.RunConfigSnapshot{}, err
	}
	return s.ResolveRunConfig(ctx, session.ID)
}
