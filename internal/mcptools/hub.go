package mcptools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/skills"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EndpointInfo is the UI/fingerprint view of one attached MCP server. The
// hashes are recorded per server so a changed binary or tool schema pauses
// automatic execution that relied on the old registration. Command and Args
// stay separate: the launch is the argument-list form and no shell string is
// ever assembled from them.
type EndpointInfo struct {
	Name         string   `json:"name"`
	Command      string   `json:"command"`
	Args         []string `json:"args"`
	Version      string   `json:"version"`
	BinarySHA256 string   `json:"binarySha256"`
	SchemaSHA256 string   `json:"schemaSha256"`
	Tools        []string `json:"tools"`
}

type endpoint struct {
	name       string
	command    string
	args       []string
	version    string
	binaryHash string
	schemaHash string
	tools      []string
	session    *mcp.ClientSession
	close      func() error
}

type Hub struct {
	closed     bool
	startMu    sync.Mutex
	configured map[string]config.MCPServer
	mu         sync.RWMutex
	endpoints  []endpoint
}

func NewLocal(ctx context.Context, catalog *skills.Catalog) (*Hub, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "icetudio-local", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "current_time", Description: "读取本机当前时间（含时区）"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: time.Now().Format(time.RFC3339)}}}, nil, ctx.Err()
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read_file", Description: "读取本机文本文件或列出目录条目（最多200项，不递归）；用户指定目录时先列目录再按需读取相关文件，受本机上下文权限与审批约束"}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, any, error) {
		content, err := readLocalFile(ctx, args.Path)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: content}}}, nil, nil
	})
	if catalog != nil && catalog.Summary() != "" {
		mcp.AddTool(server, &mcp.Tool{Name: "load_skill", Description: "按名称读取已发现的 Agent Skill 的 SKILL.md 指令"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
			Name string `json:"name"`
			Path string `json:"path,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			snapshots, err := catalog.Snapshot(false, []string{args.Name})
			if err != nil {
				return nil, nil, err
			}
			content, err := snapshots[0].ReadResource(args.Path)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: content}}}, nil, nil
		})
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "icetudio", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		serverSession.Close()
		return nil, err
	}
	h := &Hub{}
	h.endpoints = append(h.endpoints, endpoint{name: "local", session: clientSession, close: func() error {
		err := clientSession.Close()
		serverSession.Close()
		return err
	}})
	return h, nil
}

// ConnectCommand attaches an explicitly configured stdio MCP server. The
// process runs in its own process group with a restricted environment and an
// explicit working directory; its binary and later its tool schema are
// fingerprinted so stale registrations can be detected. The configured
// command is resolved on PATH and launched with the separate argument list
// from the user's own config — no shell is involved at any point.
func (h *Hub) ConnectCommand(ctx context.Context, server config.MCPServer) error {
	name, command, args := server.Name, server.Command, server.Args
	if name == "" || strings.Contains(name, "__") || name == "local" || name == "project" || command == "" {
		return errors.New("MCP 服务名称或命令无效")
	}
	h.mu.Lock()
	for _, ep := range h.endpoints {
		if ep.name == name {
			h.mu.Unlock()
			return fmt.Errorf("MCP 服务 %q 已存在", name)
		}
	}
	h.mu.Unlock()
	path, binaryHash, err := resolveBinary(command)
	if err != nil {
		return err
	}
	cmd := &exec.Cmd{Path: path, Args: append([]string{command}, args...)}
	if server.WorkingDir != "" {
		cmd.Dir = server.WorkingDir
	}
	cmd.Env = BuildEnv(server.Env)
	setProcessGroup(cmd)
	client := mcp.NewClient(&mcp.Implementation{Name: "icetudio", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		// A failed handshake (including the connect timeout) must not leak
		// the child process or its process group.
		killProcessGroup(cmd)
		return err
	}
	version := ""
	if info := session.InitializeResult(); info != nil && info.ServerInfo != nil {
		version = info.ServerInfo.Version
	}
	h.mu.Lock()
	h.endpoints = append(h.endpoints, endpoint{
		name: name, command: command, args: append([]string(nil), args...),
		version: version, binaryHash: binaryHash, session: session,
		close: func() error {
			closeErr := session.Close()
			killProcessGroup(cmd)
			return closeErr
		},
	})
	h.mu.Unlock()
	return nil
}

// Disconnect removes one attached endpoint and terminates its process. The
// server stays in the configured set; the next run reconnects it lazily.
// An unknown name is not an error so callers can disconnect unconditionally.
func (h *Hub) Disconnect(name string) error {
	h.mu.Lock()
	var target *endpoint
	kept := make([]endpoint, 0, len(h.endpoints))
	for _, ep := range h.endpoints {
		if ep.name == name && target == nil {
			detached := ep
			target = &detached
			continue
		}
		kept = append(kept, ep)
	}
	h.endpoints = kept
	h.mu.Unlock()
	if target == nil {
		return nil
	}
	return target.close()
}

// Connect attaches one configured server immediately, bypassing the lazy
// per-run connection, so the UI can verify a configuration right away. It is
// a no-op when the server is already attached.
func (h *Hub) Connect(ctx context.Context, name string) error {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.RLock()
	cfg, ok := h.configured[name]
	attached := false
	for _, ep := range h.endpoints {
		if ep.name == name {
			attached = true
			break
		}
	}
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("未配置 MCP 服务 %q", name)
	}
	if attached {
		return nil
	}
	seconds := cfg.TimeoutSeconds
	if seconds <= 0 {
		seconds = 10
	}
	start, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	return h.ConnectCommand(start, cfg)
}

// BuildEnv assembles the child environment from an allowlist of variable
// names. Values come from this process at launch; the config file only ever
// stores names. An empty allowlist passes just PATH and HOME.
func BuildEnv(allowlist []string) []string {
	parent := map[string]string{}
	for _, kv := range os.Environ() {
		if entry, _, ok := strings.Cut(kv, "="); ok {
			parent[entry] = kv
		}
	}
	names := allowlist
	if len(names) == 0 {
		names = []string{"PATH", "HOME"}
	}
	seen := map[string]bool{}
	var out []string
	for _, entry := range names {
		if seen[entry] {
			continue
		}
		seen[entry] = true
		if kv, ok := parent[entry]; ok {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return out
}

// resolveBinary resolves command on PATH and fingerprints the executable in
// one pass. The hash anchors the endpoint registration: a different binary
// under the same name invalidates automatic execution grants made before.
func resolveBinary(command string) (string, string, error) {
	path, err := exec.LookPath(command)
	if err != nil {
		return "", "", fmt.Errorf("找不到 MCP 服务程序 %q: %w", command, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", "", err
	}
	return path, hex.EncodeToString(hasher.Sum(nil)), nil
}

func (h *Hub) Definitions(ctx context.Context) ([]model.Tool, error) {
	return h.definitions(ctx, func(string) bool { return true })
}
func (h *Hub) definitions(ctx context.Context, enabled func(string) bool) ([]model.Tool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var definitions []model.Tool
	for i := range h.endpoints {
		ep := &h.endpoints[i]
		if !enabled(ep.name) {
			continue
		}
		schemaHasher := sha256.New()
		ep.tools = nil
		for tool, err := range ep.session.Tools(ctx, nil) {
			if err != nil {
				return nil, fmt.Errorf("MCP %s tools/list: %w", ep.name, err)
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, err
			}
			schemaHasher.Write([]byte(tool.Name))
			schemaHasher.Write([]byte{0})
			schemaHasher.Write(schema)
			definition := model.Tool{Type: "function"}
			definition.Function.Name = ep.name + "__" + tool.Name
			definition.Function.Description = tool.Description
			definition.Function.Parameters = schema
			definitions = append(definitions, definition)
			ep.tools = append(ep.tools, tool.Name)
		}
		ep.schemaHash = hex.EncodeToString(schemaHasher.Sum(nil))
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Function.Name < definitions[j].Function.Name })
	return definitions, nil
}

// EndpointInfos snapshots the fingerprint view of every attached server.
func (h *Hub) EndpointInfos() []EndpointInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	infos := make([]EndpointInfo, 0, len(h.endpoints))
	for _, ep := range h.endpoints {
		infos = append(infos, EndpointInfo{
			Name: ep.name, Command: ep.command, Args: append([]string(nil), ep.args...), Version: ep.version,
			BinarySHA256: ep.binaryHash, SchemaSHA256: ep.schemaHash,
			Tools: append([]string(nil), ep.tools...),
		})
	}
	for name, cfg := range h.configured {
		found := false
		for _, ep := range h.endpoints {
			if ep.name == name {
				found = true
				break
			}
		}
		if !found {
			infos = append(infos, EndpointInfo{Name: name, Command: cfg.Command, Args: cfg.Args})
		}
	}
	return infos
}

func (h *Hub) Call(ctx context.Context, name, arguments string) (string, error) {
	parts := strings.SplitN(name, "__", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("未知 MCP 工具 %q", name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args == nil {
		return "", errors.New("MCP 工具参数必须是 JSON 对象")
	}
	h.mu.RLock()
	var session *mcp.ClientSession
	for _, ep := range h.endpoints {
		if ep.name == parts[0] {
			session = ep.session
			break
		}
	}
	h.mu.RUnlock()
	if session == nil {
		return "", fmt.Errorf("未知 MCP 服务 %q", parts[0])
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: parts[1], Arguments: args})
	if err != nil {
		if parts[0] != "local" {
			return "", &UnknownOutcomeError{Err: err}
		}
		return "", err
	}
	var texts []string
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, item.Text)
		}
	}
	if len(texts) == 0 && result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return "", err
		}
		texts = append(texts, string(data))
	}
	output := strings.Join(texts, "\n")
	if result.IsError {
		return "", fmt.Errorf("MCP 工具错误: %s", output)
	}
	return output, nil
}

func (h *Hub) Close() error {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	var first error
	for _, ep := range h.endpoints {
		if err := ep.close(); err != nil && first == nil {
			first = err
		}
	}
	h.endpoints = nil
	h.closed = true
	return first
}

// UnknownOutcomeError means an external dispatch has no authoritative reply.
type UnknownOutcomeError struct{ Err error }

func (e *UnknownOutcomeError) Error() string        { return e.Err.Error() }
func (e *UnknownOutcomeError) Unwrap() error        { return e.Err }
func (e *UnknownOutcomeError) OutcomeUnknown() bool { return true }

func (h *Hub) LocalDefinitions(ctx context.Context) ([]model.Tool, error) {
	return h.definitions(ctx, func(name string) bool { return name == "local" })
}
