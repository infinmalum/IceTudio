package mcptools

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"icetudio/internal/config"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPProcessHelper(t *testing.T) {
	if os.Getenv("ICETUDIO_TEST_MCP") != "1" {
		return
	}
	f, err := os.OpenFile(os.Getenv("ICETUDIO_TEST_MARKER"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = f.WriteString("started\n")
	f.Close()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "test"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "disconnect", Description: "disconnect after dispatch"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		os.Exit(0)
		return nil, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestLazySelectedMCPStartsOnceAndTransportUnknown(t *testing.T) {
	t.Setenv("ICETUDIO_TEST_MCP", "1")
	marker := filepath.Join(t.TempDir(), "starts")
	t.Setenv("ICETUDIO_TEST_MARKER", marker)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewLocal(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	err = h.Configure([]config.MCPServer{{Name: "test", Command: exe, Args: []string{"-test.run=^TestMCPProcessHelper$"}, Env: []string{"ICETUDIO_TEST_MCP", "ICETUDIO_TEST_MARKER"}}, {Name: "unused", Command: "/no/such/program"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configuration started process")
	}
	local := h.ForServers(false, []string{"local"})
	if defs, err := local.Definitions(context.Background()); err != nil || len(defs) != 2 {
		t.Fatalf("local-only task: %v %v", defs, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unselected process started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	selected := h.ForServers(false, []string{"test"})
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defs, err := selected.Definitions(ctx)
			if err == nil && len(defs) != 2 {
				err = errors.New("wrong selected catalog")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	starts, err := os.ReadFile(marker)
	if err != nil || strings.Count(string(starts), "started") != 1 {
		t.Fatalf("duplicate process: %q %v", starts, err)
	}
	if defs, err := local.Definitions(ctx); err != nil || len(defs) != 2 {
		t.Fatal("other Run leaked tools", defs, err)
	}
	if _, err := local.Call(ctx, "test__ping", "{}"); err == nil {
		t.Fatal("unselected dispatch allowed")
	}
	if result, err := selected.Call(ctx, "test__ping", "{}"); err != nil || result != "pong" {
		t.Fatalf("ping %q %v", result, err)
	}
	_, err = selected.Call(ctx, "test__disconnect", "{}")
	var unknown *UnknownOutcomeError
	if !errors.As(err, &unknown) {
		t.Fatalf("disconnect must be unknown, got %v", err)
	}
}
