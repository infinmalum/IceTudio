package app

import (
	"context"
	"errors"
	"icetudio/internal/testfixture"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

// extToolDef models an external MCP tool: the classifier treats any non-local
// server as external with unknown sensitivity, which semi-auto mode may not
// auto-execute.
func extToolDef(name string) (defs []model.Tool) {
	definition := model.Tool{Type: "function"}
	definition.Function.Name = name
	return []model.Tool{definition}
}

func extToolCall(id, name, arguments string) model.ToolCall {
	call := model.ToolCall{ID: id, Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = arguments
	return call
}

// toolCallScript discovers external tools, calls one, then returns a final
// answer that asserts the tool result (or denial) actually reached the model.
func toolCallScript(t *testing.T, call model.ToolCall, toolResult string, finalContent string) *scriptModel {
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}}, nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, toolResult) {
				t.Fatalf("model turn 2 missing %q, last message: %+v", toolResult, messages[len(messages)-1])
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: finalContent}}, nil
		},
	}}
	if !strings.HasPrefix(call.Function.Name, "local__") {
		chat.steps = append([]func([]model.Message) (model.ChatResponse, error){discoverToolStep(call.Function.Name)}, chat.steps...)
	}
	return chat
}

func discoverToolStep(name string) func([]model.Message) (model.ChatResponse, error) {
	return func([]model.Message) (model.ChatResponse, error) {
		raw := `{"query":"` + name + `"}`
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall("discover", "local__tool_search", raw)}}}, nil
	}
}

func waitForCallState(t *testing.T, svc *Service, sessionID, state string) storage.ToolCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		calls, err := svc.ListToolCalls(sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) > 0 && calls[len(calls)-1].ToolName != "local__tool_search" && calls[len(calls)-1].State == state {
			return calls[len(calls)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("call never reached state %s: %+v", state, calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// setContextPolicy rewrites one context's machine policy (approval mode and
// resource authorization) through the service surface.
func setContextPolicy(t *testing.T, svc *Service, contextID string, mutate func(*storage.ContextPolicy)) {
	t.Helper()
	resolved, err := testfixture.RunConfig(svc.store, context.Background(), contextID)
	if err != nil {
		t.Fatal(err)
	}
	policy := resolved.Policy
	mutate(&policy)
	if _, err := svc.UpdateContextPolicy(contextID, policy); err != nil {
		t.Fatal(err)
	}
}

// Semi-auto mode must hold an external tool call for explicit approval; the
// approval binds to that call row, executes exactly once and is recorded as
// user-granted.
func TestApprovalFlowSemiMode(t *testing.T) {
	chat := toolCallScript(t, extToolCall("call-1", "extsvc__ping", "{}"), "pong", "收到")
	tools := &scriptTools{defs: extToolDef("extsvc__ping"), respond: func(name, _ string) (string, error) {
		return "pong", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "ping 一下", nil)
	if err != nil {
		t.Fatal(err)
	}
	call := waitForCallState(t, svc, session.ID, storage.CallPending)
	if _, err := svc.StartConversationTask(session.ID, "再发一次", nil); err == nil {
		t.Fatal("new run accepted while approval pending")
	}
	approved, err := svc.ApproveToolCall(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != storage.CallApproved || approved.ApprovalSource != storage.ApprovalUser || approved.ArgsHash == "" {
		t.Fatalf("approval not bound: %+v", approved)
	}
	waitForRun(t, svc, run.ID, "completed")
	calls, _ := svc.ListToolCalls(session.ID)
	if len(calls) != 2 || calls[1].State != storage.CallSucceeded || calls[1].Result != "pong" {
		t.Fatalf("approved call outcome: %+v", calls)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("tool executed %d times: %v", len(tools.calls), tools.calls)
	}
}

// A denial is terminal and reaches the model as a tool error.
func TestApprovalDenialReturnsToModel(t *testing.T) {
	chat := toolCallScript(t, extToolCall("call-1", "extsvc__ping", "{}"), "用户拒绝", "好的，不发了")
	tools := &scriptTools{defs: extToolDef("extsvc__ping"), respond: func(name, _ string) (string, error) {
		return "pong", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "ping 一下", nil)
	if err != nil {
		t.Fatal(err)
	}
	call := waitForCallState(t, svc, session.ID, storage.CallPending)
	if _, err := svc.DenyToolCall(call.ID, "不想调用外部服务"); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	calls, _ := svc.ListToolCalls(session.ID)
	if len(calls) != 2 || calls[1].State != storage.CallFailed || !strings.Contains(calls[1].Error, "用户拒绝") {
		t.Fatalf("denial outcome: %+v", calls)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("denied tool executed: %v", tools.calls)
	}
}

// An approval window that times out expires the call explicitly; the session
// stays usable afterwards.
func TestApprovalTimeoutExpiresCall(t *testing.T) {
	chat := toolCallScript(t, extToolCall("call-1", "extsvc__ping", "{}"), "审批超时", "超时了")
	tools := &scriptTools{defs: extToolDef("extsvc__ping"), respond: func(name, _ string) (string, error) {
		return "pong", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	svc.ApprovalTimeout = 80 * time.Millisecond
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "ping 一下", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	calls, _ := svc.ListToolCalls(session.ID)
	if len(calls) != 2 || calls[1].State != storage.CallCancelled {
		t.Fatalf("expired call: %+v", calls)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("expired tool executed: %v", tools.calls)
	}
	if _, err := svc.StartConversationTask(session.ID, "继续", nil); err != nil {
		t.Fatalf("session unusable after expiry: %v", err)
	}
	svc.WaitIdle()
}

// Automatic mode still holds calls whose sensitivity is unknown; credentials
// and unknown reads never auto-execute.
func TestAutomaticModeStillConfirmsUnknown(t *testing.T) {
	chat := toolCallScript(t, extToolCall("call-1", "extsvc__ping", "{}"), "pong", "收到")
	tools := &scriptTools{defs: extToolDef("extsvc__ping"), respond: func(name, _ string) (string, error) {
		return "pong", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	setContextPolicy(t, svc, testfixture.ContextID, func(p *storage.ContextPolicy) { p.ApprovalMode = "automatic" })
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "ping 一下", nil)
	if err != nil {
		t.Fatal(err)
	}
	call := waitForCallState(t, svc, session.ID, storage.CallPending)
	if _, err := svc.ApproveToolCall(call.ID); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
}

// A pure built-in tool auto-executes in semi mode without any approval wait.
func TestSemiModeAutoExecutesPureTool(t *testing.T) {
	chat := toolCallScript(t, extToolCall("call-1", "local__current_time", "{}"), "T", "收到")
	tools := &scriptTools{defs: extToolDef("local__current_time"), respond: func(name, _ string) (string, error) {
		return "2026-10-02T12:00:00+08:00", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "几点了", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	calls, _ := svc.ListToolCalls(session.ID)
	if len(calls) != 1 || calls[0].ApprovalSource != storage.ApprovalAuto {
		t.Fatalf("auto approval not recorded: %+v", calls)
	}
}

// When a workspace boundary is configured, a read outside it is a hard deny:
// no confirmation can override it, and the tool never runs.
func TestGateWorkspaceBoundaryHardDeny(t *testing.T) {
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "notes.txt")
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall("call-1", "local__read_file", `{"path":"/etc/passwd"}`)}}}, nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[len(messages)-1].Content, "工作区") {
				t.Fatalf("boundary denial not returned: %+v", messages)
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "明白"}}, nil
		},
	}}
	tools := &scriptTools{defs: extToolDef("local__read_file"), respond: func(name, args string) (string, error) {
		if strings.Contains(args, "notes.txt") {
			return "inside content", nil
		}
		return "should not happen", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	setContextPolicy(t, svc, testfixture.ContextID, func(p *storage.ContextPolicy) {
		p.Permissions.Unrestricted = false
		p.Permissions.WorkspacePaths = []string{workspace}
	})
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "读配置", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	calls, _ := svc.ListToolCalls(session.ID)
	if len(calls) != 1 || calls[0].State != storage.CallFailed || !strings.Contains(calls[0].Error, "工作区") {
		t.Fatalf("boundary deny outcome: %+v", calls)
	}
	// A path inside the workspace classifies as workspace content and auto-
	// executes in semi mode.
	chat2 := toolCallScript(t, extToolCall("call-2", "local__read_file", `{"path":"`+inside+`"}`), "inside content", "读完")
	svc.chat = chat2
	run2, err := svc.StartConversationTask(session.ID, "读笔记", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run2.ID, "completed")
	calls, _ = svc.ListToolCalls(session.ID)
	if len(calls) != 2 || calls[1].State != storage.CallSucceeded || calls[1].ApprovalSource != storage.ApprovalAuto {
		t.Fatalf("workspace read outcome: %+v", calls)
	}
}

// The exfiltration check blocks remote sends without a covering grant while
// loopback models stay exempt; grants are per destination and per scope.
func TestExfilCheckAndGrants(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMessage(ctx, session.ID, "", storage.RoleUser, "机密任务"); err != nil {
		t.Fatal(err)
	}
	remote := config.Model{BaseURL: "http://api.example.com:8080/v1"}
	local := config.Model{BaseURL: "http://127.0.0.1:11434/v1"}
	profile, err := testfixture.RunConfig(store, ctx, testfixture.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.exfilCheck(ctx, session.ID, local, profile); err != nil {
		t.Fatalf("loopback endpoint must stay exempt: %v", err)
	}
	if err := svc.exfilCheck(ctx, session.ID, remote, profile); err == nil || !strings.Contains(err.Error(), "数据外发被拒绝") {
		t.Fatalf("remote send without grant accepted: %v", err)
	}
	if _, err := svc.GrantExfil(testfixture.ContextID, "http://api.example.com:8080/v1", []string{storage.ScopePersonal}); err != nil {
		t.Fatal(err)
	}
	if err := svc.exfilCheck(ctx, session.ID, remote, profile); err != nil {
		t.Fatalf("granted send rejected: %v", err)
	}
	// A credential-scoped result tightens the session scope: the grant no
	// longer covers it and the next send must be refused.
	run, _, err := store.CreateRun(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, calls, err := store.AddAssistantMessage(ctx, session.ID, run.ID, "", []storage.MessageToolCall{{ID: "c1", Name: "local__read_file", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishToolCall(ctx, calls[0].ID, storage.CallSucceeded, "secret", "", storage.ScopeCredential); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunStatus(ctx, run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	err = svc.exfilCheck(ctx, session.ID, remote, profile)
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("send after credential-scoped result accepted: %v", err)
	}
	if err := svc.RevokeExfil(testfixture.ContextID, "http://api.example.com:8080"); err != nil {
		t.Fatal(err)
	}
	if err := svc.exfilCheck(ctx, session.ID, remote, profile); err == nil {
		t.Fatal("send accepted after revocation")
	}
}

// The guarded model client must not call the inner client when the check
// refuses the send.
func TestGuardedChatBlocksOnCheckError(t *testing.T) {
	called := false
	inner := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			called = true
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "hi"}}, nil
		},
	}}
	blocked := guardedChat{inner: inner, check: func(context.Context) error { return errors.New("数据外发被拒绝") }}
	if _, err := blocked.Chat(context.Background(), config.Model{}, nil, nil); err == nil || called {
		t.Fatalf("inner client called despite block: called=%v err=%v", called, err)
	}
	allowed := guardedChat{inner: inner, check: func(context.Context) error { return nil }}
	if _, err := allowed.Chat(context.Background(), config.Model{}, nil, nil); err != nil || !called {
		t.Fatalf("inner client not called when allowed: called=%v err=%v", called, err)
	}
}

// Fingerprint sync records endpoints on first sight and flags any change to
// binary or schema afterwards; the gate then suspends automatic execution.
func TestSyncMCPEndpointsAndGateSuspension(t *testing.T) {
	chat := toolCallScript(t, extToolCall("call-1", "files__ping", "{}"), "pong", "收到")
	tools := &scriptTools{defs: extToolDef("files__ping"), respond: func(name, _ string) (string, error) {
		return "pong", nil
	}}
	svc, _ := newTestService(t, chat, tools)
	hub := &fakeEndpointSource{infos: []mcptools.EndpointInfo{{
		Name: "files", Command: "files-mcp", BinarySHA256: "bin1", SchemaSHA256: "schema1", Tools: []string{"ping"},
	}}}
	svc.endpoints = hub
	if err := svc.SyncMCPEndpoints(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListMCPEndpoints()
	if err != nil || len(items) != 1 || items[0].Changed {
		t.Fatalf("first sync: %+v %v", items, err)
	}
	setContextPolicy(t, svc, testfixture.ContextID, func(p *storage.ContextPolicy) { p.ApprovalMode = "automatic" })
	snapshot, err := testfixture.RunConfig(svc.store, context.Background(), testfixture.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	gate := toolGate{svc: svc, contextID: testfixture.ContextID, snapshot: &snapshot}
	// A pure built-in tool auto-approves in automatic mode.
	if decision := gate.Gate(context.Background(), agent.ToolRequest{Tool: "local__current_time", Arguments: "{}"}); decision.Confirm != "" || decision.Deny != "" {
		t.Fatalf("automatic mode should auto-approve pure tools: %+v", decision)
	}
	// The binary changed: automatic execution pauses in every mode, and the
	// confirmation reason must cite the fingerprint, not the classification.
	hub.infos[0].BinarySHA256 = "bin2"
	if err := svc.SyncMCPEndpoints(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ = svc.ListMCPEndpoints()
	if !items[0].Changed {
		t.Fatal("fingerprint change not flagged")
	}
	if decision := gate.Gate(context.Background(), agent.ToolRequest{Tool: "files__ping", Arguments: "{}"}); !strings.Contains(decision.Confirm, "指纹") {
		t.Fatalf("fingerprint change must suspend auto-execution: %+v", decision)
	}
}

type fakeEndpointSource struct{ infos []mcptools.EndpointInfo }

func (f *fakeEndpointSource) EndpointInfos() []mcptools.EndpointInfo { return f.infos }
