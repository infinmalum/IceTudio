package app

import (
	"context"
	"icetudio/internal/testfixture"
	"strings"
	"testing"

	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func findConfig(t *testing.T, svc *Service, id string) storage.RuntimeConfig {
	t.Helper()
	configs, err := svc.ListRuntimeConfigs()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range configs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("执行配置 %s not found", id)
	return storage.RuntimeConfig{}
}

// A system-prompt script asserts, on its second turn, which prompt the run
// is actually executing with.
func promptScript(t *testing.T, call model.ToolCall, wantPrompt string, firstTurnCallsTool bool) *scriptModel {
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			if firstTurnCallsTool {
				return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}}, nil
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "好的"}}, nil
		},
		func(messages []model.Message) (model.ChatResponse, error) {
			if !strings.Contains(messages[0].Content, wantPrompt) {
				t.Fatalf("run used prompt %q, want %q", messages[0].Content, wantPrompt)
			}
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "收到"}}, nil
		},
	}}
	if firstTurnCallsTool && !strings.HasPrefix(call.Function.Name, "local__") {
		chat.steps = append([]func([]model.Message) (model.ChatResponse, error){discoverToolStep(call.Function.Name)}, chat.steps...)
	}
	return chat
}

// Editing a shared execution config changes every referencing context's
// subsequent runs.
func TestSharedConfigEditAffectsSubsequentRuns(t *testing.T) {
	svc, _ := newTestService(t, &scriptModel{}, &scriptTools{})
	contextA, err := testfixture.Context(svc.store, context.Background(), "甲", storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	contextB, err := testfixture.Context(svc.store, context.Background(), "乙", storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	sessionA, err := svc.createContextSession(contextA.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionB, err := svc.createContextSession(contextB.ID)
	if err != nil {
		t.Fatal(err)
	}
	def := findConfig(t, svc, storage.DefaultConfigID)
	def.Data.SystemPrompt = "共享提示词 v2"
	if _, err := svc.UpdateRuntimeConfig(def); err != nil {
		t.Fatal(err)
	}
	svc.chat = promptScript(t, extToolCall("c", "local__current_time", "{}"), "共享提示词 v2", false)
	for _, tc := range []struct {
		contextID string
		sessionID string
	}{{contextA.ID, sessionA.ID}, {contextB.ID, sessionB.ID}} {
		run, err := svc.StartConversationTask(tc.sessionID, "你好")
		if err != nil {
			t.Fatal(err)
		}
		waitForRun(t, svc, run.ID, "completed")
	}
}

// An in-flight run keeps executing with the 执行配置 snapshot it started
// with, even after the config is edited while a tool call waits for
// approval; the next run uses the new content. The run row records both the
// config id and the revision it snapshotted.
func TestInFlightRunKeepsConfigSnapshot(t *testing.T) {
	release := make(chan struct{})
	tools := &scriptTools{defs: extToolDef("extsvc__ping"), respond: func(name, _ string) (string, error) {
		<-release
		return "pong", nil
	}}
	svc, store := newTestService(t, &scriptModel{}, tools)
	data := storage.BlankRuntimeConfig()
	data.SystemPrompt = "旧提示词"
	data.AllMCPServers = true
	config, err := store.CreateRuntimeConfig(context.Background(), "P", data)
	if err != nil {
		t.Fatal(err)
	}
	local, err := testfixture.Context(svc.store, context.Background(), "甲", config.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.createContextSession(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	setContextPolicy(t, svc, local.ID, func(p *storage.ContextPolicy) { p.ApprovalMode = "semi" })
	svc.chat = promptScript(t, extToolCall("call-1", "extsvc__ping", "{}"), "旧提示词", true)
	run, err := svc.StartConversationTask(session.ID, "ping 一下")
	if err != nil {
		t.Fatal(err)
	}
	call := waitForCallState(t, svc, session.ID, storage.CallPending)

	// Edit the shared execution config while the run waits for approval.
	target := findConfig(t, svc, config.ID)
	target.Data.SystemPrompt = "新提示词"
	target.Data.AllMCPServers = false
	target.Data.EnabledMCPServers = nil
	if _, err := svc.UpdateRuntimeConfig(target); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveToolCall(call.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForRun(t, svc, run.ID, "completed")
	runRow, err := svc.GetRun(run.ID)
	if err != nil || runRow.ConfigID != config.ID || runRow.ConfigRevision != 1 {
		t.Fatalf("in-flight run must record its snapshot revision: %+v %v", runRow, err)
	}
	// The next run resolves the newly committed content.
	svc.chat = promptScript(t, extToolCall("call-2", "local__current_time", "{}"), "新提示词", false)
	run2, err := svc.StartConversationTask(session.ID, "再问")
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run2.ID, "completed")
}

// Saving a combination whose capability references do not exist on this host
// is rejected before anything is persisted.
func TestConfigResourceValidation(t *testing.T) {
	svc, _ := newTestService(t, &scriptModel{}, &scriptTools{})
	def := findConfig(t, svc, storage.DefaultConfigID)
	def.Data.AllSkills = false
	def.Data.EnabledSkills = []string{"no-such-skill"}
	if _, err := svc.UpdateRuntimeConfig(def); err == nil || !strings.Contains(err.Error(), "Skill") {
		t.Fatalf("missing skill accepted: %v", err)
	}
	def = findConfig(t, svc, storage.DefaultConfigID)
	def.Data.AllMCPServers = false
	def.Data.EnabledMCPServers = []string{"no-such-server"}
	if _, err := svc.UpdateRuntimeConfig(def); err == nil || !strings.Contains(err.Error(), "MCP") {
		t.Fatalf("missing MCP server accepted: %v", err)
	}
	// The stored combination is unchanged after the failed saves.
	def = findConfig(t, svc, storage.DefaultConfigID)
	if !def.Data.AllSkills || !def.Data.AllMCPServers || def.Revision != 1 {
		t.Fatalf("failed save must leave the 执行配置 untouched: %+v", def)
	}
}
