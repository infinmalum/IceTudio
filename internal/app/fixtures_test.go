package app

import (
	"context"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
)

func (s *Service) CreateSession() (storage.Session, error) {
	return testfixture.Session(s.store, context.Background(), "test")
}
func (s *Service) createContextSession(id string) (storage.Session, error) {
	return testfixture.ContextSession(s.store, context.Background(), id, "test")
}

// configureDefault publishes the given execution data as the app default config.
func (s *Service) configureDefault(data storage.RuntimeConfigData) (storage.RuntimeConfig, error) {
	if err := s.checkReady(); err != nil {
		return storage.RuntimeConfig{}, err
	}
	ctx := context.Background()
	current, err := s.store.GetRuntimeConfig(ctx, storage.DefaultConfigID)
	if err != nil {
		return storage.RuntimeConfig{}, err
	}
	current.Data = data
	return s.store.UpdateRuntimeConfig(ctx, current)
}
