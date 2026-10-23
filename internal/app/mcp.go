package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/storage"
)

// MCPEndpointSource is the hub surface the service needs to display MCP
// servers and keep their fingerprints registered. *mcptools.Hub satisfies
// it; tests can supply fakes.
type MCPEndpointSource interface {
	EndpointInfos() []mcptools.EndpointInfo
}

// MCPManager is the mutable configuration surface of the MCP hub: replacing
// the configured server set, connecting one configured server on demand and
// disconnecting one attached endpoint. *mcptools.Hub satisfies it.
type MCPManager interface {
	MCPEndpointSource
	Configure(servers []config.MCPServer) error
	Connect(ctx context.Context, name string) error
	Disconnect(name string) error
}

// MCPEndpointStatus is what the UI shows per attached MCP server: where it
// comes from (command), what it offers (tools), its fingerprints and whether
// either fingerprint changed since the last registration (in which case
// automatic execution is suspended until re-confirmed).
type MCPEndpointStatus struct {
	Name         string   `json:"name"`
	Command      string   `json:"command"`
	Args         []string `json:"args"`
	Version      string   `json:"version"`
	BinarySHA256 string   `json:"binarySha256"`
	SchemaSHA256 string   `json:"schemaSha256"`
	Tools        []string `json:"tools"`
	Changed      bool     `json:"changed"`
}

// SyncMCPEndpoints compares each attached server against the fingerprint
// registry and records what is now live. A mismatch marks the endpoint as
// changed: the permission gate then suspends automatic execution for its
// tools until a user re-confirms. It must run after the hub connected and
// after Definitions populated the schema hashes.
func (s *Service) SyncMCPEndpoints(ctx context.Context) error {
	if s.store == nil || s.endpoints == nil {
		return nil
	}
	infos := s.endpoints.EndpointInfos()
	changed := map[string]bool{}
	for _, info := range infos {
		if info.SchemaSHA256 == "" {
			continue
		}
		stored, err := s.store.GetMCPEndpoint(ctx, info.Name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (stored.BinarySHA256 != info.BinarySHA256 || stored.SchemaSHA256 != info.SchemaSHA256) {
			changed[info.Name] = true
			log.Printf("MCP %s 指纹变化：二进制 %s→%s，schema %s→%s", info.Name, shortHash(stored.BinarySHA256), shortHash(info.BinarySHA256), shortHash(stored.SchemaSHA256), shortHash(info.SchemaSHA256))
		}
		if err := s.store.SetMCPEndpoint(ctx, storage.MCPEndpoint{
			Name: info.Name, Command: info.Command, BinarySHA256: info.BinarySHA256,
			SchemaSHA256: info.SchemaSHA256, Version: info.Version,
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	for name, value := range changed {
		s.mcpChanged[name] = value
	}
	s.mu.Unlock()
	return nil
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12] + "…"
	}
	return hash
}

func (s *Service) endpointChanged(server string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mcpChanged[server]
}

// ListMCPEndpoints merges the live hub view with the persisted registry for
// display. The local in-process server is reported like any other source.
func (s *Service) ListMCPEndpoints() ([]MCPEndpointStatus, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	infos := []mcptools.EndpointInfo{}
	if s.endpoints != nil {
		infos = s.endpoints.EndpointInfos()
	}
	items := make([]MCPEndpointStatus, 0, len(infos))
	for _, info := range infos {
		items = append(items, MCPEndpointStatus{
			Name: info.Name, Command: info.Command, Args: info.Args, Version: info.Version,
			BinarySHA256: info.BinarySHA256, SchemaSHA256: info.SchemaSHA256,
			Tools: info.Tools, Changed: s.endpointChanged(info.Name),
		})
	}
	return items, nil
}

// ListConfiguredMCPServers returns the configured server list from the live
// configuration; this is the install/connect catalog the assistant editor
// picks enabled servers from.
func (s *Service) ListConfiguredMCPServers() []config.MCPServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]config.MCPServer(nil), s.config.MCPServers...)
}

// mcpConnectTimeout mirrors the hub's handshake default.
func mcpConnectTimeout(server config.MCPServer) time.Duration {
	seconds := server.TimeoutSeconds
	if seconds <= 0 {
		seconds = 10
	}
	return time.Duration(seconds) * time.Second
}

// SaveMCPServer validates and persists one MCP server entry, replacing any
// existing entry with the same name. The hub's configured set is replaced, a
// replaced server is disconnected so its old process stops, and the saved
// server is connected immediately so the user learns right away whether the
// command works — the fingerprints are registered on success.
func (s *Service) SaveMCPServer(server config.MCPServer) ([]config.MCPServer, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	updated := s.config
	s.mu.RUnlock()
	replaced := false
	for i := range updated.MCPServers {
		if updated.MCPServers[i].Name == server.Name {
			updated.MCPServers[i] = server
			replaced = true
			break
		}
	}
	if !replaced {
		updated.MCPServers = append(updated.MCPServers, server)
	}
	if err := config.Save(s.configPath, updated); err != nil {
		return nil, err
	}
	if s.manager != nil {
		if err := s.manager.Configure(updated.MCPServers); err != nil {
			return nil, err
		}
		if replaced {
			_ = s.manager.Disconnect(server.Name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), mcpConnectTimeout(server))
		defer cancel()
		if err := s.manager.Connect(ctx, server.Name); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.config = updated
	s.mu.Unlock()
	if s.store != nil {
		if err := s.SyncMCPEndpoints(context.Background()); err != nil {
			log.Printf("MCP 指纹登记: %v", err)
		}
	}
	return s.ListConfiguredMCPServers(), nil
}

// DeleteMCPServer removes one configured server: the entry leaves the
// config, its process is disconnected and its fingerprint registration is
// dropped so a later re-addition starts a fresh trust baseline.
func (s *Service) DeleteMCPServer(name string) ([]config.MCPServer, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	updated := s.config
	s.mu.RUnlock()
	remaining := updated.MCPServers[:0:0]
	found := false
	for _, item := range updated.MCPServers {
		if item.Name == name {
			found = true
			continue
		}
		remaining = append(remaining, item)
	}
	if !found {
		return nil, fmt.Errorf("未配置 MCP 服务 %q", name)
	}
	updated.MCPServers = remaining
	if err := config.Save(s.configPath, updated); err != nil {
		return nil, err
	}
	if s.manager != nil {
		if err := s.manager.Configure(updated.MCPServers); err != nil {
			return nil, err
		}
		if err := s.manager.Disconnect(name); err != nil {
			log.Printf("MCP %s 断开: %v", name, err)
		}
	}
	s.mu.Lock()
	s.config = updated
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.DeleteMCPEndpoint(context.Background(), name); err != nil {
			log.Printf("MCP %s 注销: %v", name, err)
		}
	}
	return s.ListConfiguredMCPServers(), nil
}

// ConfirmMCPEndpoint re-records one server's current fingerprints as trusted
// after the user reviewed the change; automatic execution based on the
// registration resumes.
func (s *Service) ConfirmMCPEndpoint(name string) ([]MCPEndpointStatus, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if s.endpoints == nil {
		return nil, errors.New("MCP 宿主未就绪")
	}
	var live *mcptools.EndpointInfo
	for _, info := range s.endpoints.EndpointInfos() {
		if info.Name == name {
			info := info
			live = &info
			break
		}
	}
	if live == nil {
		return nil, fmt.Errorf("MCP 服务 %q 未连接", name)
	}
	if live.SchemaSHA256 == "" {
		return nil, fmt.Errorf("MCP 服务 %q 尚未完成工具发现，无法核对指纹；请先在任务中使用它", name)
	}
	if err := s.store.SetMCPEndpoint(context.Background(), storage.MCPEndpoint{
		Name: live.Name, Command: live.Command, BinarySHA256: live.BinarySHA256,
		SchemaSHA256: live.SchemaSHA256, Version: live.Version,
	}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.mcpChanged, name)
	s.mu.Unlock()
	return s.ListMCPEndpoints()
}
