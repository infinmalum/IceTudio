package mcptools

import (
	"context"
	"fmt"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"strings"
	"time"
)

// Configure records explicitly configured commands without starting processes.
func (h *Hub) Configure(servers []config.MCPServer) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("MCP 宿主已关闭")
	}
	h.configured = map[string]config.MCPServer{}
	for _, s := range servers {
		if s.Name == "" || s.Name == "local" || strings.Contains(s.Name, "__") || s.Command == "" {
			return fmt.Errorf("MCP 服务名称或命令无效")
		}
		if _, ok := h.configured[s.Name]; ok {
			return fmt.Errorf("重复 MCP 服务 %s", s.Name)
		}
		h.configured[s.Name] = s
	}
	return nil
}

// ForServers creates a Run-local view; it never exposes another Run's servers.
func (h *Hub) ForServers(all bool, names []string) *SelectedTools {
	return &SelectedTools{hub: h, all: all, names: append([]string(nil), names...)}
}

type SelectedTools struct {
	hub   *Hub
	all   bool
	names []string
}

func (s *SelectedTools) enabled(name string) bool {
	if s.all {
		return true
	}
	for _, n := range s.names {
		if n == name {
			return true
		}
	}
	return false
}
func (s *SelectedTools) Definitions(ctx context.Context) ([]model.Tool, error) {
	h := s.hub
	// Serialize first connection so simultaneous Runs share one process.
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return nil, fmt.Errorf("MCP 宿主已关闭")
	}
	configs := []config.MCPServer{}
	for name, cfg := range h.configured {
		if !s.enabled(name) {
			continue
		}
		connected := false
		for _, ep := range h.endpoints {
			if ep.name == name {
				connected = true
				break
			}
		}
		if !connected {
			configs = append(configs, cfg)
		}
	}
	h.mu.RUnlock()
	for _, cfg := range configs {
		seconds := cfg.TimeoutSeconds
		if seconds <= 0 {
			seconds = 10
		}
		start, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		err := h.ConnectCommand(start, cfg)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("MCP %s: %w", cfg.Name, err)
		}
	}
	return h.definitions(ctx, s.enabled)
}
func (s *SelectedTools) Call(ctx context.Context, name, args string) (string, error) {
	server, _, ok := strings.Cut(name, "__")
	if !ok || !s.enabled(server) {
		return "", fmt.Errorf("未选择的 MCP 工具 %q", name)
	}
	return s.hub.Call(ctx, name, args)
}

// LocalDefinitions does not connect any external process.
func (s *SelectedTools) LocalDefinitions(ctx context.Context) ([]model.Tool, error) {
	return s.hub.definitions(ctx, func(name string) bool { return name == "local" && s.enabled(name) })
}
