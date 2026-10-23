package app

import (
	"context"
	"errors"
	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
	"testing"
)

func TestRunGateUsesSnapshotButLivePolicy(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.ResolveRunConfig(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := toolGate{svc: svc, contextID: snap.ContextID, snapshot: &snap}
	edited, err := store.GetRuntimeConfig(ctx, storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	edited.Data = storage.BlankRuntimeConfig()
	if _, err := store.UpdateRuntimeConfig(ctx, edited); err != nil {
		t.Fatal(err)
	}
	req := agent.ToolRequest{Tool: "local__current_time", Arguments: "{}"}
	if d := g.Gate(ctx, req); d.Deny != "" || d.Confirm != "" {
		t.Fatalf("config change altered in-flight capabilities: %+v", d)
	}
	setContextPolicy(t, svc, snap.ContextID, func(p *storage.ContextPolicy) { p.ApprovalMode = "manual"; p.Permissions.Unrestricted = false })
	if d := g.Gate(ctx, req); d.Confirm == "" {
		t.Fatal("live approval tightening ignored")
	}
	if d := g.Recheck(ctx, agent.ToolRequest{Tool: "local__read_file", Arguments: `{"path":"/etc/passwd"}`}); d.Deny == "" {
		t.Fatal("live path revocation ignored")
	}
	// Existing restrictive grants must not expand when the live policy expands.
	snap.Policy.Permissions = storage.PermissionScope{}
	setContextPolicy(t, svc, snap.ContextID, func(p *storage.ContextPolicy) { p.Permissions.Unrestricted = true })
	if d := g.Recheck(ctx, agent.ToolRequest{Tool: "local__read_file", Arguments: `{"path":"/etc/passwd"}`}); d.Deny == "" {
		t.Fatal("live relaxation expanded old Run")
	}
}

func TestTransportFailureBlocksFurtherRuns(t *testing.T) {
	calls := 0
	tools := &scriptTools{defs: extToolDef("remote__send"), respond: func(string, string) (string, error) {
		calls++
		return "", &mcptools.UnknownOutcomeError{Err: errors.New("EOF after dispatch")}
	}}
	svc, _ := newTestService(t, toolCallScript(t, extToolCall("send", "remote__send", "{}"), "", "must not continue"), tools)
	session, _ := svc.CreateSession()
	run, err := svc.StartConversationTask(session.ID, "send")
	if err != nil {
		t.Fatal(err)
	}
	call := waitForCallState(t, svc, session.ID, storage.CallPending)
	if _, err = svc.ApproveToolCall(call.ID); err != nil {
		t.Fatal(err)
	}
	svc.WaitIdle()
	list, err := svc.ListToolCalls(session.ID)
	if err != nil || len(list) != 2 || list[1].State != storage.CallExecutionUnknown {
		t.Fatalf("outcome: %+v %v", list, err)
	}
	if _, err = svc.StartConversationTask(session.ID, "retry"); err == nil {
		t.Fatal("unknown outcome allowed another Run")
	}
	if calls != 1 {
		t.Fatal("duplicate dispatch")
	}
	_ = run
}

func TestExfilContextIsolationAndPayload(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	other, err := testfixture.Context(svc.store, context.Background(), "other", storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := svc.CreateSession()
	otherSession, _ := svc.createContextSession(other.ID)
	remote := config.Model{Provider: "openai", BaseURL: "https://example.com/v1", Name: "test"}
	if _, err := svc.GrantExfil(testfixture.ContextID, remote.BaseURL, []string{storage.ScopePersonal}); err != nil {
		t.Fatal(err)
	}
	prompt := []model.Message{{Role: "system", Content: "private custom persona"}}
	if err := svc.checkPayload(ctx, remote, testfixture.ContextID, session.ID, prompt, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.checkPayload(ctx, remote, other.ID, otherSession.ID, prompt, nil); err == nil {
		t.Fatal("another Agent inherited grant")
	}
	if err := svc.checkPayload(ctx, remote, testfixture.ContextID, session.ID, prompt, extToolDef("remote__private_schema")); err == nil {
		t.Fatal("untrusted schema escaped")
	}
	prompt = append(prompt, model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall("c", "local__read_file", `{"path":"secret"}`)}})
	if err := svc.checkPayload(ctx, remote, testfixture.ContextID, session.ID, prompt, nil); err == nil {
		t.Fatal("unclassified arguments escaped")
	}
	run, _, _ := store.CreateRun(ctx, session.ID)
	_, calls, err := store.AddAssistantMessage(ctx, session.ID, run.ID, "", []storage.MessageToolCall{{ID: "failed", Name: "local__read_file", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishToolCall(ctx, calls[0].ID, storage.CallFailed, "", "sensitive error", storage.ScopeCredential); err != nil {
		t.Fatal(err)
	}
	snap, _ := testfixture.RunConfig(store, ctx, testfixture.ContextID)
	if err := svc.exfilCheck(ctx, session.ID, remote, snap); err == nil {
		t.Fatal("failed tool data escaped")
	}
	if err := svc.RevokeExfil(testfixture.ContextID, remote.BaseURL); err != nil {
		t.Fatal(err)
	}
	if err := svc.checkPayload(ctx, remote, testfixture.ContextID, session.ID, prompt, nil); err == nil {
		t.Fatal("revocation ignored")
	}
}

func TestLocalContextRestrictionsSurviveConfigChange(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	session, _ := svc.CreateSession()
	ctx := context.Background()
	local := config.Model{Provider: "ollama", BaseURL: "http://127.0.0.1:11434/v1"}
	remote := config.Model{Provider: "openai", BaseURL: "https://example.com/v1"}
	if err := svc.checkPayload(ctx, local, testfixture.ContextID, session.ID, nil, extToolDef("external__private")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrantExfil(testfixture.ContextID, remote.BaseURL, []string{storage.ScopePersonal}); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetRuntimeConfig(ctx, storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	current.Data = storage.BlankRuntimeConfig()
	if _, err := store.UpdateRuntimeConfig(ctx, current); err != nil {
		t.Fatal(err)
	}
	snap, _ := testfixture.RunConfig(store, ctx, testfixture.ContextID)
	if err := svc.exfilCheck(ctx, session.ID, remote, snap); err == nil {
		t.Fatal("config change discarded prior unknown input")
	}
	initial := storage.ExfilGrant{ContextID: testfixture.ContextID, Scopes: []string{storage.ScopePersonal}}
	if _, err := svc.GrantExfil(testfixture.ContextID, remote.BaseURL, []string{storage.ScopePersonal, storage.ScopeUnknown}); err != nil {
		t.Fatal(err)
	}
	if err := svc.exfilCheck(ctx, session.ID, remote, snap); err != nil {
		t.Fatal(err)
	}
	if err := svc.checkInitialGrant(ctx, session.ID, remote, snap, initial); err == nil {
		t.Fatal("authorization expansion affected existing Run")
	}
}
