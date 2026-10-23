package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
	"os"
	"strings"
	"testing"
	"time"
)

func discoveryFixture(t *testing.T) (*Service, *discoveryTools, *scriptTools) {
	t.Helper()
	tools := &scriptTools{respond: func(name, args string) (string, error) { return "actual external result", nil }}
	for i := 0; i < 100; i++ {
		d := model.Tool{Type: "function"}
		d.Function.Name = fmt.Sprintf("ext__task_%03d", i)
		d.Function.Description = "generic external task"
		d.Function.Parameters = json.RawMessage(`{"type":"object"}`)
		tools.defs = append(tools.defs, d)
	}
	s, _ := newTestService(t, &scriptModel{}, tools)
	p, err := testfixture.RunConfig(s.store, context.Background(), testfixture.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	p.AllMCPServers = true
	p.AllowedTools = nil
	d := newDiscoveryTools(bookkeepingTools{inner: tools, svc: s, profile: p})
	return s, d, tools
}
func TestToolDiscoveryHidesLoadsAndRejectsChangedSchema(t *testing.T) {
	_, d, tools := discoveryFixture(t)
	ctx := context.Background()
	defs, err := d.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range defs {
		if strings.HasPrefix(v.Function.Name, "ext__") {
			t.Fatal("external tools leaked initially")
		}
	}
	raw, err := d.Call(ctx, "local__tool_search", `{"query":"ext__task_042","limit":1}`)
	if err != nil || !strings.Contains(raw, "ext__task_042") {
		t.Fatal(raw, err)
	}
	if _, err = d.Call(ctx, "ext__task_042", "{}"); err == nil {
		t.Fatal("same-turn unoffered tool executed")
	}
	defs, err = d.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range defs {
		if v.Function.Name == "ext__task_042" {
			found = true
		}
	}
	if !found {
		t.Fatal("loaded schema not offered")
	}
	if _, err = d.Call(ctx, "ext__task_042", "{}"); err != nil {
		t.Fatal(err)
	}
	tools.defs[42].Function.Parameters = json.RawMessage(`{"type":"object","required":["new"]}`)
	if _, err = d.Call(ctx, "ext__task_042", "{}"); err == nil {
		t.Fatal("changed schema dispatched")
	}
	if len(tools.calls) != 1 {
		t.Fatal(tools.calls)
	}
}
func TestToolDiscoveryRespectsSelectionAndBounds(t *testing.T) {
	_, d, _ := discoveryFixture(t)
	d.inner.profile.AllowedTools = []string{"local__tool_search", "ext__task_002"}
	raw, err := d.Call(context.Background(), "local__tool_search", `{"query":"task","limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "ext__task_001") || !strings.Contains(raw, "ext__task_002") {
		t.Fatal(raw)
	}
	if len(d.loaded) != 1 {
		t.Fatal(d.loaded)
	}
	if _, err = d.Call(context.Background(), "local__tool_search", `{"query":"task","limit":6}`); err == nil {
		t.Fatal("unbounded search accepted")
	}
}

type discoveryMock struct{ turn int }

func (m *discoveryMock) Chat(_ context.Context, _ config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	m.turn++
	call := model.ToolCall{ID: fmt.Sprintf("d%d", m.turn), Type: "function"}
	switch m.turn {
	case 1:
		for _, d := range tools {
			if d.Function.Name == "ext__task_042" {
				return model.ChatResponse{}, fmt.Errorf("schema was not deferred")
			}
		}
		call.Function.Name = "local__tool_search"
		call.Function.Arguments = `{"query":"ext__task_042","limit":1}`
	case 2:
		found := false
		for _, d := range tools {
			if d.Function.Name == "ext__task_042" {
				found = true
			}
		}
		if !found {
			return model.ChatResponse{}, fmt.Errorf("next request missing loaded schema")
		}
		call.Function.Name = "ext__task_042"
		call.Function.Arguments = "{}"
	default:
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "发现并执行完成"}}, nil
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}}, nil
}
func TestToolDiscoveryModelLoopLoadsAndExecutes(t *testing.T) {
	s, _, tools := discoveryFixture(t)
	s.chat = &discoveryMock{}
	a, err := s.store.GetLocalContext(context.Background(), testfixture.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	a.Policy.ApprovalMode = "semi"
	if _, err = s.UpdateContextPolicy(a.ID, a.Policy); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetRuntimeConfig(storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	p.Data.AllMCPServers = true
	if _, err = s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	session, err := s.createContextSession(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(session.ID, "发现 task042 工具后执行")
	if err != nil {
		t.Fatal(err)
	}
	// External tools still require the existing approval channel.
	for i := 0; i < 300; i++ {
		calls, err := s.ListToolCalls(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range calls {
			if c.State == storage.CallPending && c.ToolName == "ext__task_042" {
				_, _ = s.ApproveToolCall(c.ID)
			}
		}
		run, err := s.GetRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" {
			if run.Status != "completed" || len(tools.calls) != 1 {
				t.Fatal(run, tools.calls)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("discovery loop did not finish")
}

func TestDiscoveryMCPHelper(t *testing.T) {
	helper := false
	for _, arg := range os.Args {
		if arg == "tool-mcp-helper" {
			helper = true
		}
	}
	if !helper {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "discovery-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "task_042", Description: "actual discovery integration"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "real MCP response"}}}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestToolDiscoveryRealMCPDefersConnectionAndLoadsSchema(t *testing.T) {
	ctx := context.Background()
	hub, err := mcptools.NewLocal(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = hub.Configure([]config.MCPServer{{Name: "ext", Command: exe, Args: []string{"-test.run=^TestDiscoveryMCPHelper$", "--", "tool-mcp-helper"}, TimeoutSeconds: 5}}); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestService(t, &scriptModel{}, hub)
	s.endpoints = hub
	p, err := testfixture.RunConfig(s.store, ctx, testfixture.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	d := newDiscoveryTools(bookkeepingTools{inner: hub.ForServers(true, nil), svc: s, profile: p})
	defs, err := d.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range defs {
		if strings.HasPrefix(def.Function.Name, "ext__") {
			t.Fatal("external schema in initial turn")
		}
	}
	for _, info := range hub.EndpointInfos() {
		if info.Name == "ext" && info.SchemaSHA256 != "" {
			t.Fatal("external process connected before search")
		}
	}
	raw, err := d.Call(ctx, "local__tool_search", `{"query":"ext__task_042"}`)
	if err != nil || !strings.Contains(raw, "ext__task_042") {
		t.Fatal(raw, err)
	}
	if _, err = d.Definitions(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := d.Call(ctx, "ext__task_042", "{}")
	if err != nil || response != "real MCP response" {
		t.Fatal(response, err)
	}
}

func TestSmallExternalCatalogStillRequiresDiscovery(t *testing.T) {
	_, d, tools := discoveryFixture(t)
	tools.defs = tools.defs[:1]
	for _, allowed := range [][]string{nil, {"ext__task_000"}} {
		d.inner.profile.AllowedTools = allowed
		defs, err := d.Definitions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range defs {
			if tool.Function.Name == "ext__task_000" {
				t.Fatal("external schema offered without discovery")
			}
		}
		if _, err := d.Call(context.Background(), "ext__task_000", "{}"); err == nil {
			t.Fatal("undiscovered tool executed")
		}
	}
	if len(tools.calls) != 0 {
		t.Fatal("tool discovery restriction bypassed")
	}
}
