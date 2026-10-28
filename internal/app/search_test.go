package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type nativeSearchMock struct {
	plain    atomic.Int32
	searched atomic.Int32
	started  chan struct{}
	release  chan struct{}
	fail     bool
}

func (m *nativeSearchMock) Chat(context.Context, config.Model, []model.Message, []model.Tool) (model.ChatResponse, error) {
	m.plain.Add(1)
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "普通回答"}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func (m *nativeSearchMock) ChatWithSearch(ctx context.Context, _ config.Model, _ []model.Message, _ []model.Tool, options model.SearchOptions, onDelta func(string) error) (model.ChatResponse, error) {
	m.searched.Add(1)
	if options.MaxCalls < 1 || options.MaxCalls > 3 {
		return model.ChatResponse{}, errors.New("bad search ceiling")
	}
	if options.OnEvent != nil {
		if err := options.OnEvent(model.SearchEvent{Type: "searching", ID: "s1"}); err != nil {
			return model.ChatResponse{}, err
		}
	}
	if m.started != nil {
		close(m.started)
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return model.ChatResponse{}, ctx.Err()
		}
	}
	if m.fail {
		return model.ChatResponse{}, errors.New("模拟服务失败")
	}
	if onDelta != nil {
		if err := onDelta("搜索回答"); err != nil {
			return model.ChatResponse{}, err
		}
	}
	calls := 1
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "搜索回答"}, Usage: model.Usage{TotalTokens: 10}, Search: model.SearchReport{Calls: &calls, Text: "搜索回答", Citations: []model.Citation{{URL: "https://example.org", Title: "来源"}}}}, nil
}
func nativeSearchConversation(t *testing.T, m *nativeSearchMock, grant bool) (*Service, storage.Conversation) {
	t.Helper()
	s, _ := newTestService(t, m, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.config.Model = config.Model{Provider: "openai", BaseURL: "https://mock.invalid/v1", Name: "gpt-4.1"}
	s.mu.Unlock()
	if grant {
		if _, err := s.GrantExfil(c.ContextID, "https://mock.invalid", []string{storage.ScopePersonal, storage.ScopeUnknown}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetConversationOptions(c.ID, storage.ConversationOptions{WebSearch: true}); err != nil {
		t.Fatal(err)
	}
	return s, c
}
func TestConversationSearchFrozenOptionAndNativeRecords(t *testing.T) {
	m := &nativeSearchMock{started: make(chan struct{}), release: make(chan struct{})}
	s, c := nativeSearchConversation(t, m, true)
	r, err := s.StartConversationTask(c.ID, "搜索资料", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.started:
	case <-time.After(3 * time.Second):
		t.Fatal("native search did not start")
	}
	if _, err := s.SetConversationOptions(c.ID, storage.ConversationOptions{}); err != nil {
		t.Fatal(err)
	}
	close(m.release)
	waitForRun(t, s, r.ID, "completed")
	records, err := s.ListConversationSearch(c.ID)
	if err != nil || len(records) != 1 || records[0].State != "completed" || len(records[0].Report.Citations) != 1 || len(records[0].Report.Events) != 1 || records[0].Report.Cost != nil {
		t.Fatal(records, err)
	}
	scopes, err := s.sessionScopes(context.Background(), c.ID, storage.RunConfigSnapshot{})
	if err != nil || !containsTool(scopes, storage.ScopeUnknown) {
		t.Fatal(scopes, err)
	}
	next, err := s.StartConversationTask(c.ID, "普通任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, next.ID, "completed")
	if m.searched.Load() != 1 || m.plain.Load() != 1 {
		t.Fatal(m.searched.Load(), m.plain.Load())
	}
}
func TestConversationSearchExfilFailureAndCancellation(t *testing.T) {
	for _, kind := range []string{"grant", "failure", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			m := &nativeSearchMock{fail: kind == "failure"}
			if kind == "cancel" {
				m.started = make(chan struct{})
				m.release = make(chan struct{})
			}
			s, c := nativeSearchConversation(t, m, kind != "grant")
			r, err := s.StartConversationTask(c.ID, "搜索资料", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := "failed"
			if kind == "cancel" {
				select {
				case <-m.started:
				case <-time.After(3 * time.Second):
					t.Fatal("search not started")
				}
				if err := s.CancelTask(r.ID); err != nil {
					t.Fatal(err)
				}
				want = "cancelled"
			}
			run := waitForRun(t, s, r.ID, want)
			records, err := s.ListConversationSearch(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "grant" {
				if m.searched.Load() != 0 || len(records) != 0 || !strings.Contains(run.Error, "外发") {
					t.Fatal(m.searched.Load(), records, run)
				}
			} else if len(records) != 1 || records[0].State != "execution_unknown" || records[0].Report.Calls != nil {
				t.Fatal(records)
			}
			if m.plain.Load() != 0 {
				t.Fatal("native request silently fell back")
			}
		})
	}
}
func TestConversationSearchUnavailableAndMCPDiscoveryBoundary(t *testing.T) {
	s, _ := newTestService(t, &scriptModel{}, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetConversationOptions(c.ID, storage.ConversationOptions{WebSearch: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartConversationTask(c.ID, "搜索", nil); err == nil {
		t.Fatal("unavailable search accepted")
	}
	def := model.Tool{Type: "function"}
	def.Function.Name = "web__lookup"
	def.Function.Parameters = []byte(`{"type":"object","properties":{}}`)
	tools := &scriptTools{defs: []model.Tool{def}, respond: func(string, string) (string, error) {
		return `{"results":[{"url":"https://example.org/source","title":"来源"}]}`, nil
	}}
	s.tools = tools
	s.mu.Lock()
	s.config.MCPServers = []config.MCPServer{{Name: "web", Command: "mock", WebSearchTools: []string{"lookup"}}}
	s.mu.Unlock()
	p, err := s.GetRuntimeConfig(storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	p.Data.AllMCPServers = false
	p.Data.EnabledMCPServers = []string{"local", "web"}
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}

	r, ctx, profile, err := s.runs.StartPrepared(context.Background(), c.ID, s.config.Model, s.skills.Snapshot, "", s.configuredSearchTools()...)
	if err != nil {
		t.Fatal(err)
	}
	enabled := newDiscoveryTools(bookkeepingTools{inner: tools, svc: s, profile: profile, sessionID: c.ID, runID: r.ID})
	defs, err := enabled.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range defs {
		if d.Function.Name == "web__lookup" {
			found = true
		}
	}
	if !found {
		t.Fatal("configured search was not loaded")
	}
	if _, err := s.SetConversationOptions(c.ID, storage.ConversationOptions{}); err != nil {
		t.Fatal(err)
	}
	defs, err = enabled.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, d := range defs {
		if d.Function.Name == "web__lookup" {
			found = true
		}
	}
	if !found {
		t.Fatal("in-flight search changed after toggle")
	}
	if err := s.runs.Finish(r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	next, ctx, profile, err := s.runs.StartPrepared(context.Background(), c.ID, s.config.Model, s.skills.Snapshot, "", s.configuredSearchTools()...)
	if err != nil {
		t.Fatal(err)
	}
	disabled := newDiscoveryTools(bookkeepingTools{inner: tools, svc: s, profile: profile, sessionID: c.ID, runID: next.ID})
	result, err := disabled.Call(ctx, "local__tool_search", `{"query":"lookup"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "web__lookup") {
		t.Fatal("disabled search discoverable", result)
	}
	if _, err := disabled.Call(ctx, "web__lookup", "{}"); err == nil {
		t.Fatal("disabled search executed")
	}
	if len(tools.calls) != 0 {
		t.Fatal("discovery executed tool")
	}
}

func TestConversationSearchMCPUsesExistingApprovalAndCitations(t *testing.T) {
	def := model.Tool{Type: "function"}
	def.Function.Name = "web__lookup"
	def.Function.Parameters = []byte(`{"type":"object","properties":{}}`)
	tools := &scriptTools{defs: []model.Tool{def}, respond: func(string, string) (string, error) {
		return `{"results":[{"url":"https://example.org/source","title":"来源"},{"url":"javascript:unsafe","title":"坏链接"}]}`, nil
	}}
	script := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			call := model.ToolCall{ID: "s1", Type: "function"}
			call.Function.Name = "web__lookup"
			call.Function.Arguments = "{}"
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, Usage: model.Usage{TotalTokens: 10}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "搜索完成"}, Usage: model.Usage{TotalTokens: 10}}, nil
		},
	}}
	s, _ := newTestService(t, script, tools)
	s.mu.Lock()
	s.config.MCPServers = []config.MCPServer{{Name: "web", Command: "mock", WebSearchTools: []string{"lookup"}}}
	s.mu.Unlock()
	data := storage.DefaultRuntimeConfig()
	data.AllMCPServers = false
	data.EnabledMCPServers = []string{"local", "web"}
	p, err := s.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetConversationOptions(c.ID, storage.ConversationOptions{WebSearch: true}); err != nil {
		t.Fatal(err)
	}
	run, err := s.StartConversationTask(c.ID, "搜索", nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := waitForCallState(t, s, c.ID, storage.CallPending)
	if pending.ToolName != "web__lookup" {
		t.Fatal(pending)
	}
	if _, err := s.ApproveToolCall(pending.ID); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, run.ID, "completed")
	records, err := s.ListConversationSearch(c.ID)
	if err != nil || len(records) != 1 || records[0].Provider != "web__lookup" || records[0].State != "succeeded" || len(records[0].Report.Citations) != 1 {
		t.Fatal(records, err)
	}
	if records[0].Report.Citations[0].URL != "https://example.org/source" {
		t.Fatal(records)
	}
}

func TestConversationSearchDoesNotWidenReadWorker(t *testing.T) {
	m := &nativeSearchMock{}
	s, c := nativeSearchConversation(t, m, true)
	ctx := context.Background()
	p, err := s.GetRuntimeConfig(c.ConfigID)
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.WorkerSources = []storage.WorkerSource{{Name: "reader"}}
	p.Data.PlanEnabled = true
	p.Data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1}
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	root, _, err := s.store.CreateRunWithOptions(ctx, c.ID, storage.CreateRunOptions{DefaultModel: s.config.Model})
	if err != nil {
		t.Fatal(err)
	}
	spec := storage.TaskSpec{ID: "read", Worker: "reader", Task: "只读分析", AccessMode: "read"}
	if _, err := s.store.SetTaskGraph(ctx, root.ID, 0, []storage.TaskSpec{spec}); err != nil {
		t.Fatal(err)
	}
	session, err := s.store.CreateDelegationSession(ctx, c.ContextID, storage.DefaultConfigID, "reader")
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := s.store.CreateRunWithOptions(ctx, session.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "reader", TaskSpec: &spec})
	if err != nil {
		t.Fatal(err)
	}
	g := guardedChat{svc: s, runID: child.ID, inner: m}
	_, _, enabled, err := g.searchContext(ctx, s.config.Model)
	if err != nil || enabled {
		t.Fatal("readonly worker gained external search", enabled, err)
	}
}

func TestConversationNativeSearchLoopbackDoesNotBypassGrant(t *testing.T) {
	m := &nativeSearchMock{}
	s, c := nativeSearchConversation(t, m, false)
	s.mu.Lock()
	s.config.Model.BaseURL = "http://127.0.0.1:19291/v1"
	s.mu.Unlock()
	run, err := s.StartConversationTask(c.ID, "搜索", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, run.ID, "failed")
	if m.searched.Load() != 0 {
		t.Fatal("loopback gateway bypassed search exfil grant")
	}
	if _, err := s.GrantExfil(c.ContextID, "http://127.0.0.1:19291", []string{storage.ScopePersonal, storage.ScopeUnknown}); err != nil {
		t.Fatal(err)
	}
	next, err := s.StartConversationTask(c.ID, "搜索", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, s, next.ID, "completed")
	if m.searched.Load() != 1 {
		t.Fatal("authorized loopback gateway did not dispatch search")
	}
}

func TestConversationNativeSearchDisabledForCompaction(t *testing.T) {
	m := &nativeSearchMock{}
	s, c := nativeSearchConversation(t, m, true)
	ctx := context.Background()
	root, _, err := s.store.CreateRunWithOptions(ctx, c.ID, storage.CreateRunOptions{DefaultModel: s.config.Model})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.BeginModelStep(ctx, root.ID, "summary"); err != nil {
		t.Fatal(err)
	}
	g := guardedChat{svc: s, runID: root.ID, inner: m, check: func(context.Context) error { return nil }}
	_, err = g.Chat(context.WithValue(ctx, suppressSearchKey{}, true), s.config.Model, []model.Message{{Role: "user", Content: "summarize evidence"}}, nil)
	if err != nil || m.searched.Load() != 0 || m.plain.Load() != 1 {
		t.Fatal(err, m.searched.Load(), m.plain.Load())
	}
}

func TestNativeSearchCapabilityIndependentOfMCPSelection(t *testing.T) {
	source := config.Model{Provider: "openai", Name: "gpt-4.1"}
	blank := storage.BlankRuntimeConfig()
	if availability := searchRoute(source, blank, nil); !availability.Available || availability.Route != "native" {
		t.Fatal("disabling MCP disabled native search", availability)
	}
	blank.AllowedTools = []string{"local__read_file"}
	if availability := searchRoute(source, blank, nil); availability.Available {
		t.Fatal("explicit tool ceiling widened", availability)
	}
}
