package app

import (
	"context"
	"fmt"

	"icetudio/internal/storage"
)

// ListRuntimeConfigs returns every execution config, built-in default first.
func (s *Service) ListRuntimeConfigs() ([]storage.RuntimeConfig, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	return s.store.ListRuntimeConfigs(context.Background())
}

// GetRuntimeConfig returns one execution config for clients that need the
// current revision before an edit.
func (s *Service) GetRuntimeConfig(id string) (storage.RuntimeConfig, error) {
	if err := s.checkReady(); err != nil {
		return storage.RuntimeConfig{}, err
	}
	return s.store.GetRuntimeConfig(context.Background(), id)
}

// UpdateRuntimeConfig saves a new combination atomically. Before persisting,
// the capability references are checked against what this host actually
// offers: enabled skills must be installed and enabled MCP servers must be
// configured, so a saved execution config never points at resources that
// cannot load.
func (s *Service) UpdateRuntimeConfig(p storage.RuntimeConfig) (storage.RuntimeConfig, error) {
	if err := s.checkReady(); err != nil {
		return storage.RuntimeConfig{}, err
	}
	if err := s.validateRuntimeConfigResources(p.Data); err != nil {
		return storage.RuntimeConfig{}, err
	}
	return s.store.UpdateRuntimeConfig(context.Background(), p)
}

// RestoreRuntimeConfigDefault resets the built-in default execution config's
// combination to the shipped content.
func (s *Service) RestoreRuntimeConfigDefault(revision int) (storage.RuntimeConfig, error) {
	if err := s.checkReady(); err != nil {
		return storage.RuntimeConfig{}, err
	}
	return s.store.RestoreDefaultRuntimeConfigData(context.Background(), revision)
}

// validateRuntimeConfigResources checks the execution config's capability
// selections against the resources this host currently offers.
func (s *Service) validateRuntimeConfigResources(data storage.RuntimeConfigData) error {
	if !data.AllSkills {
		for _, name := range data.EnabledSkills {
			if !s.skills.Installed(name) {
				return fmt.Errorf("未安装的 Skill %q", name)
			}
		}
	}
	if !data.AllMCPServers {
		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()
		configured := map[string]bool{"local": true}
		for _, server := range cfg.MCPServers {
			configured[server.Name] = true
		}
		for _, name := range data.EnabledMCPServers {
			if !configured[name] {
				return fmt.Errorf("未接入的 MCP 服务 %q", name)
			}
		}
	}
	return nil
}
