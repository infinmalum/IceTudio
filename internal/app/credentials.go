package app

import (
	"errors"
	"os"
	"sync"

	"icetudio/internal/config"
)

// credentialsPath is the data-directory file holding the remote provider API
// keys. The empty path (tests, headless hosts without one) disables saving
// while listing still works.
type credentialStore struct {
	path string
	mu   sync.Mutex
}

// CredentialStatus reports whether a provider has a stored key. Keys are
// never returned — not to the UI, not over IPC, not into logs.
type CredentialStatus struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
}

func newCredentialStore(path string) *credentialStore {
	return &credentialStore{path: path}
}

// load reads the store fresh on every access so saved or removed keys apply
// immediately across desktop, CLI and the model adapters.
func (c *credentialStore) load() (config.Credentials, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == "" {
		return config.Credentials{Providers: map[string]string{}}, nil
	}
	return config.LoadCredentials(c.path)
}

// SaveCredential stores one remote provider's API key in the protected data
// directory. The key never enters config files, 执行配置, runs or logs.
func (s *Service) SaveCredential(provider, apiKey string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	s.credStore.mu.Lock()
	defer s.credStore.mu.Unlock()
	if s.credStore.path == "" {
		return errors.New("此宿主未启用凭据存储")
	}
	creds, err := config.LoadCredentials(s.credStore.path)
	if err != nil {
		return err
	}
	if err := creds.SetCredential(provider, apiKey); err != nil {
		return err
	}
	return config.SaveCredentials(s.credStore.path, creds)
}

// DeleteCredential removes one provider's stored key.
func (s *Service) DeleteCredential(provider string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	s.credStore.mu.Lock()
	defer s.credStore.mu.Unlock()
	if s.credStore.path == "" {
		return errors.New("此宿主未启用凭据存储")
	}
	creds, err := config.LoadCredentials(s.credStore.path)
	if err != nil {
		return err
	}
	if err := creds.DeleteCredential(provider); err != nil {
		return err
	}
	return config.SaveCredentials(s.credStore.path, creds)
}

// ListCredentialStatus reports, per remote provider, whether a key is
// stored. Local providers are key-less by design and not listed.
func (s *Service) ListCredentialStatus() ([]CredentialStatus, error) {
	creds, err := s.credStore.load()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	statuses := make([]CredentialStatus, 0, len(config.RemoteProviders))
	for _, provider := range config.RemoteProviders {
		_, configured := creds.Key(provider)
		statuses = append(statuses, CredentialStatus{Provider: provider, Configured: configured})
	}
	return statuses, nil
}
