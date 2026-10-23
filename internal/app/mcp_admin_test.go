package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
)

// fakeManager records how the service drove the hub's mutable surface.
type fakeManager struct {
	infos        []mcptools.EndpointInfo
	configured   [][]config.MCPServer
	connected    []string
	disconnected []string
}

func (f *fakeManager) EndpointInfos() []mcptools.EndpointInfo { return f.infos }
func (f *fakeManager) Configure(servers []config.MCPServer) error {
	f.configured = append(f.configured, servers)
	return nil
}
func (f *fakeManager) Connect(_ context.Context, name string) error {
	f.connected = append(f.connected, name)
	return nil
}
func (f *fakeManager) Disconnect(name string) error {
	f.disconnected = append(f.disconnected, name)
	return nil
}

func newAdminTestService(t *testing.T, endpoints MCPEndpointSource, manager MCPManager) (*Service, *storage.Store) {
	t.Helper()
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	catalog, err := skills.Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := agent.NewRuns(store)
	t.Cleanup(runs.Close)
	source := config.DefaultModel("lmstudio")
	source.Name = "test-model"
	svc := New(Options{
		Store: store, Runs: runs, Skills: catalog,
		Config: config.Config{Model: source}, ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		MCPEndpoints: endpoints, MCPManager: manager,
	})
	return svc, store
}

func TestSaveAndDeleteMCPServer(t *testing.T) {
	manager := &fakeManager{}
	svc, store := newAdminTestService(t, manager, manager)

	servers, err := svc.SaveMCPServer(config.MCPServer{Name: "fetch", Command: "fetch-mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "fetch" || servers[0].Command != "fetch-mcp" {
		t.Fatalf("saved servers: %+v", servers)
	}
	data, err := os.ReadFile(svc.configPath)
	if err != nil || !strings.Contains(string(data), "fetch-mcp") {
		t.Fatalf("config not persisted: %v %s", err, data)
	}
	if len(manager.connected) != 1 || manager.connected[0] != "fetch" {
		t.Fatalf("server was not connected after save: %v", manager.connected)
	}
	if n := len(manager.configured); n == 0 || len(manager.configured[n-1]) != 1 {
		t.Fatalf("hub was not reconfigured: %+v", manager.configured)
	}

	// Saving the same name replaces the entry and restarts the process.
	servers, err = svc.SaveMCPServer(config.MCPServer{Name: "fetch", Command: "fetch-mcp", Args: []string{"--fast"}})
	if err != nil || len(servers) != 1 || len(servers[0].Args) != 1 {
		t.Fatalf("replace save: %v %+v", err, servers)
	}
	if len(manager.disconnected) != 1 || manager.disconnected[0] != "fetch" {
		t.Fatalf("replaced server was not disconnected: %v", manager.disconnected)
	}

	// An invalid entry never reaches the config file or the hub.
	if _, err := svc.SaveMCPServer(config.MCPServer{Name: "bad"}); err == nil {
		t.Fatal("expected invalid server to be rejected")
	}
	if n := len(manager.configured); n > 0 && len(manager.configured[n-1]) != 1 {
		t.Fatalf("invalid server reached the hub: %+v", manager.configured)
	}

	servers, err = svc.DeleteMCPServer("fetch")
	if err != nil || len(servers) != 0 {
		t.Fatalf("delete: %v %+v", err, servers)
	}
	if len(manager.disconnected) != 2 {
		t.Fatalf("deleted server was not disconnected: %v", manager.disconnected)
	}
	if _, err := store.GetMCPEndpoint(context.Background(), "fetch"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fingerprint registration survived deletion: %v", err)
	}
	if _, err := svc.DeleteMCPServer("fetch"); err == nil {
		t.Fatal("expected deleting an unknown server to fail")
	}
}

func TestConfirmMCPEndpointClearsChange(t *testing.T) {
	manager := &fakeManager{infos: []mcptools.EndpointInfo{
		{Name: "fetch", Command: "fetch-mcp", BinarySHA256: "new-bin", SchemaSHA256: "new-schema", Tools: []string{"get"}},
	}}
	svc, store := newAdminTestService(t, manager, manager)
	ctx := context.Background()
	if err := store.SetMCPEndpoint(ctx, storage.MCPEndpoint{Name: "fetch", BinarySHA256: "old-bin", SchemaSHA256: "old-schema"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncMCPEndpoints(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListMCPEndpoints()
	if err != nil || len(items) != 1 || !items[0].Changed {
		t.Fatalf("changed endpoint not flagged: %v %+v", err, items)
	}
	items, err = svc.ConfirmMCPEndpoint("fetch")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Changed {
		t.Fatal("confirmed endpoint still flagged changed")
	}
	registered, err := store.GetMCPEndpoint(ctx, "fetch")
	if err != nil || registered.BinarySHA256 != "new-bin" || registered.SchemaSHA256 != "new-schema" {
		t.Fatalf("registration not updated: %v %+v", err, registered)
	}

	if _, err := svc.ConfirmMCPEndpoint("unknown"); err == nil {
		t.Fatal("expected confirming an unknown server to fail")
	}
	manager.infos = append(manager.infos, mcptools.EndpointInfo{Name: "bare", Command: "bare-mcp"})
	if _, err := svc.ConfirmMCPEndpoint("bare"); err == nil {
		t.Fatal("expected confirming a server without tool discovery to fail")
	}
}

func TestListSkillsReturnsCatalog(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "report-check")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: report-check\ndescription: 检查报告结构\n---\n\n步骤"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skills.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	runs := agent.NewRuns(nil)
	t.Cleanup(runs.Close)
	svc := New(Options{Skills: catalog})
	items, err := svc.ListSkills()
	if err != nil || len(items) != 1 || items[0].Name != "report-check" || items[0].Description != "检查报告结构" {
		t.Fatalf("skill catalog: %v %+v", err, items)
	}
}
