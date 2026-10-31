package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/storage"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProjectMaterialMCPHelper(t *testing.T) {
	if !containsID(os.Args, "project-material-helper") {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "material-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "analyze", Description: "Inspect supplied material"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Score string `json:"score"`
	}) (*mcp.CallToolResult, any, error) {
		var score struct {
			Revision int64 `json:"revision"`
			State    struct {
				Tracks []json.RawMessage `json:"tracks"`
			} `json:"state"`
		}
		if err := json.Unmarshal([]byte(args.Score), &score); err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("received revision=%d tracks=%d", score.Revision, len(score.State.Tracks))}}}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestProjectMaterialUsesRealMCPTransport(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	hub, err := mcptools.NewLocal(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = hub.Configure([]config.MCPServer{{Name: "music", Command: exe, Args: []string{"-test.run=^TestProjectMaterialMCPHelper$", "--", "project-material-helper"}, TimeoutSeconds: 5}}); err != nil {
		t.Fatal(err)
	}
	tools.svc.tools = hub
	tools.svc.endpoints = hub
	tools.inner = hub.ForServers(true, nil)
	run, profile, err := tools.svc.store.CreateRun(ctx, tools.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	tools.runID = run.ID
	tools.profile = profile
	begin := func(id, name, args, source string) storage.ToolCall {
		t.Helper()
		_, calls, e := tools.svc.store.AddAssistantMessage(ctx, tools.sessionID, run.ID, "", []storage.MessageToolCall{{ID: id, Name: name, Arguments: args}}, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = tools.svc.store.ApproveToolCall(ctx, calls[0].ID, source); e != nil {
			t.Fatal(e)
		}
		if e = tools.svc.store.BeginToolCall(ctx, calls[0].ID); e != nil {
			t.Fatal(e)
		}
		return calls[0]
	}
	raw := fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q},"tool":"music__analyze","field":"score","arguments":{}}`, ids.Project, ids.C1)
	call := begin("prepare", "project__prepare_material", raw, storage.ApprovalAuto)
	prepared, err := tools.Call(ctx, "project__prepare_material", raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = tools.svc.store.FinishToolCall(ctx, call.ID, storage.CallSucceeded, prepared, "", storage.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	var p preparedProjectMaterial
	if err = json.Unmarshal([]byte(prepared), &p); err != nil {
		t.Fatal(err)
	}
	raw = fmt.Sprintf(`{"preparationId":%q}`, p.ID)
	begin("send", "local__send_material", raw, storage.ApprovalUser)
	out, err := tools.Call(ctx, "local__send_material", raw)
	if err != nil || !strings.Contains(out, fmt.Sprintf("received revision=%d tracks=1", p.Revision)) {
		t.Fatal(out, err)
	}
	var material map[string]any
	if json.Unmarshal([]byte(out), &material) != nil || material["source"] != "music__analyze" {
		t.Fatal(out)
	}
	if _, err = tools.Call(ctx, "local__send_material", raw); err == nil {
		t.Fatal("real MCP dispatch replayed")
	}
	// The MCP result remains ordinary external data, never a music command.
	before, _ := tools.svc.GetProject(ids.Project)
	if before.Revision != p.Revision {
		t.Fatal("material send modified project")
	}
}
