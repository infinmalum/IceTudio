package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type SearchAvailability struct {
	ConfigPath string `json:"configPath"`
	Available  bool   `json:"available"`
	Route      string `json:"route"`
	Reason     string `json:"reason"`
}

func (s *Service) configuredSearchTools() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := []string{}
	for _, server := range s.config.MCPServers {
		for _, tool := range server.WebSearchTools {
			names = append(names, server.Name+"__"+tool)
		}
	}
	return names
}
func searchRoute(source config.Model, p storage.RuntimeConfigData, tools []string) SearchAvailability {
	if model.NativeSearchSupported(source) && allowsNativeSearch(p) {
		reason := source.Provider + " 原生搜索；账户与端点支持情况在请求时验证"
		if source.Provider == "gemini" {
			reason = "Gemini Interactions 原生搜索；共享预算限制宿主请求数，服务商内部搜索次数没有硬上限"
		}
		return SearchAvailability{Available: true, Route: "native", Reason: reason}
	}
	for _, name := range tools {
		if p.AllowsTool(name) {
			return SearchAvailability{Available: true, Route: "mcp", Reason: "使用已配置搜索 MCP，实际执行仍需现有权限与审批"}
		}
	}
	return SearchAvailability{Reason: "当前接口未支持原生搜索，或组件未允许搜索。可在已接入 MCP 服务配置 webSearchTools，然后在执行配置中允许该工具。"}
}
func (s *Service) ConversationSearchAvailability(id string) (SearchAvailability, error) {
	if err := s.checkReady(); err != nil {
		return SearchAvailability{}, err
	}
	p, err := s.store.ResolveRunConfig(context.Background(), id)
	if err != nil {
		return SearchAvailability{}, err
	}
	main, err := p.RuntimeConfigData.ResolveComponent("main")
	if err != nil {
		return SearchAvailability{}, err
	}
	source := main.Model
	if source.Provider == "" {
		s.mu.RLock()
		source = s.config.Model
		s.mu.RUnlock()
	}
	availability := searchRoute(source, main, s.configuredSearchTools())
	availability.ConfigPath = s.configPath
	return availability, nil
}
func (s *Service) SetConversationOptions(id string, options storage.ConversationOptions) (storage.Conversation, error) {
	if err := s.checkReady(); err != nil {
		return storage.Conversation{}, err
	}
	c, err := s.store.SetConversationOptions(context.Background(), id, options)
	if err == nil {
		s.publish(Event{Type: "conversation.updated", SessionID: id})
	}
	return c, err
}
func (s *Service) ListConversationSearch(id string) ([]storage.SearchRequest, error) {
	if _, err := s.GetConversation(id); err != nil {
		return nil, err
	}
	ctx := context.Background()
	items, err := s.store.ListConversationSearch(ctx, id)
	if err != nil {
		return nil, err
	}
	calls, err := s.store.ListSessionToolCalls(ctx, id)
	if err != nil {
		return nil, err
	}
	resources := map[string]storage.RunResources{}
	for _, call := range calls {
		r, ok := resources[call.RunID]
		if !ok {
			r, err = s.store.RunResources(ctx, call.RunID)
			if err != nil {
				return nil, err
			}
			resources[call.RunID] = r
		}
		if !containsTool(r.SearchTools, call.ToolName) {
			continue
		}
		report := model.SearchReport{Text: call.Result, Events: []model.SearchEvent{{Type: "mcp_tool_call", ID: call.ID, Status: call.State}}}
		if call.State == storage.CallSucceeded {
			report.Citations = mcpSearchCitations(call.Result)
		}
		items = append(items, storage.SearchRequest{ID: call.ID, RunID: call.RunID, SessionID: id, StepID: call.StepID, Provider: call.ToolName, State: call.State, Reserved: 1, Report: report, Error: call.Error, CreatedAt: call.CreatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	return items, nil
}

// Accept explicit URL/title pairs in JSON results. Opaque MCP text is kept as
// evidence, without inventing citations from prose or trusting it as public.
func mcpSearchCitations(raw string) []model.Citation {
	var data any
	if len(raw) > 512<<10 || json.Unmarshal([]byte(raw), &data) != nil {
		return nil
	}
	out := []model.Citation{}
	seen := map[string]bool{}
	var visit func(any, int)
	visit = func(value any, depth int) {
		if depth > 20 || len(out) >= 100 {
			return
		}
		switch node := value.(type) {
		case map[string]any:
			if u, ok := node["url"].(string); ok && model.ValidCitationURL(u) && !seen[u] {
				title, _ := node["title"].(string)
				out = append(out, model.Citation{URL: u, Title: title})
				seen[u] = true
			}
			// Deterministic source ordering for reproducible record display.
			keys := []string{}
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(node[key], depth+1)
			}
		case []any:
			for _, child := range node {
				visit(child, depth+1)
			}
		}
	}
	visit(data, 0)
	return out
}

// No native tools in compaction: it summarizes retained evidence only.
type suppressSearchKey struct{}

func (g guardedChat) searchContext(ctx context.Context, source config.Model) (storage.RunResources, storage.Run, bool, error) {
	if g.svc == nil || g.runID == "" || ctx.Value(suppressSearchKey{}) == true {
		return storage.RunResources{}, storage.Run{}, false, nil
	}
	resources, err := g.svc.store.RunResources(ctx, g.runID)
	if err != nil {
		return resources, storage.Run{}, false, err
	}
	if !resources.Options.WebSearch {
		return resources, storage.Run{}, false, nil
	}
	run, err := g.svc.store.GetRun(ctx, g.runID)
	if err != nil {
		return resources, run, false, err
	}
	// Structured planner/reviewer responses consume existing evidence; execution
	// components inherit the option and cannot enable it beyond the root.
	if run.Component != "main" && run.Component != "worker" {
		return resources, run, false, nil
	}
	if run.Component == "worker" {
		spec, found, err := g.svc.store.RunTaskSpec(ctx, g.runID)
		if err != nil {
			return resources, run, false, err
		}
		if found && spec.AccessMode == "read" {
			return resources, run, false, nil
		}
	}
	p, _, err := g.svc.store.ProfileSnapshot(ctx, g.runID)
	if err != nil {
		return resources, run, false, err
	}
	route := searchRoute(source, p, resources.SearchTools)
	if !route.Available {
		if run.Component == "worker" {
			return resources, run, false, nil
		}
		return resources, run, false, errors.New(route.Reason)
	}
	native := route.Route == "native"
	if native {
		if _, ok := g.inner.(model.SearchChatModel); !ok {
			return resources, run, false, errors.New("当前模型客户端未实现原生搜索接口")
		}
		// Retrieved content and model-generated queries are unknown sources. Require
		// coverage before the request, so the first response cannot bypass the grant.
		if err := g.svc.store.AccumulateContextScopes(ctx, run.SessionID, []string{storage.ScopeUnknown}); err != nil {
			return resources, run, false, err
		}
	}
	return resources, run, native, nil
}
func (g guardedChat) callModel(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool, onDelta func(string) error) (model.ChatResponse, error) {
	resources, run, native, err := g.searchContext(ctx, source)
	if err != nil {
		return model.ChatResponse{}, err
	}
	if g.payloadCheck != nil {
		if err := g.payloadCheck(ctx, messages, tools); err != nil {
			return model.ChatResponse{}, err
		}
	}
	if err := g.check(ctx); err != nil {
		return model.ChatResponse{}, err
	}
	var resp model.ChatResponse
	if native {
		if err := g.svc.checkNativeSearchGrant(ctx, source, resources, run); err != nil {
			return resp, err
		}
		item, e := g.svc.store.BeginNativeSearch(ctx, g.runID, source.Provider)
		if e != nil {
			return resp, e
		}
		options := model.SearchOptions{MaxCalls: item.Reserved, OnEvent: func(event model.SearchEvent) error {
			if err := g.svc.store.AppendSearchEvent(context.WithoutCancel(ctx), item.ID, event); err != nil {
				return err
			}
			g.svc.publish(Event{Type: "search.updated", RunID: g.runID, SessionID: run.SessionID, Detail: event.Type})
			return nil
		}}
		resp, err = g.inner.(model.SearchChatModel).ChatWithSearch(ctx, source, messages, tools, options, onDelta)
		if e := g.svc.store.FinishNativeSearch(context.WithoutCancel(ctx), item.ID, resp.Search, err); e != nil {
			return resp, fmt.Errorf("记录搜索结果: %w", e)
		}
		g.svc.publish(Event{Type: "search.updated", RunID: g.runID, SessionID: run.SessionID})
	} else if streamer, ok := g.inner.(interface {
		ChatStream(context.Context, config.Model, []model.Message, []model.Tool, func(string) error) (model.ChatResponse, error)
	}); ok && onDelta != nil {
		resp, err = streamer.ChatStream(ctx, source, messages, tools, onDelta)
	} else {
		resp, err = g.inner.Chat(ctx, source, messages, tools)
	}
	if g.svc != nil {
		if e := g.svc.store.RecordUsage(context.WithoutCancel(ctx), g.runID, usageTokens(resp.Usage)); e != nil {
			return resp, e
		}
	}
	return resp, err
}
func containsTool(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
func (d *discoveryTools) searchToolAllowed(name string) bool {
	resources := d.inner.profile.Resources
	if resources == nil {
		return true
	}
	if !containsTool(resources.SearchTools, name) {
		return true
	}
	return resources.Options.WebSearch
}

// Only explicit MCP registrations are eligible as search fallbacks. Names and
// descriptions from an external server cannot register themselves as search.
func (d *discoveryTools) loadSearchFallback(ctx context.Context) error {
	r := d.inner.profile.Resources
	if r == nil || !r.Options.WebSearch {
		return nil
	}
	if d.inner.delegated {
		spec, found, err := d.inner.svc.store.RunTaskSpec(ctx, d.inner.runID)
		if err != nil {
			return err
		}
		if found && spec.AccessMode == "read" {
			return nil
		}
	}
	if model.NativeSearchSupported(d.inner.profile.Model) && allowsNativeSearch(d.inner.profile.RuntimeConfigData) {
		return nil
	}
	allowed := false
	for _, name := range r.SearchTools {
		if d.inner.profile.AllowsTool(name) {
			allowed = true
		}
	}
	if !allowed {
		return nil
	}
	catalog, err := d.inner.Definitions(ctx)
	if err != nil {
		return err
	}
	if err := d.inner.svc.SyncMCPEndpoints(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	found := 0
	schemaBytes := 0
	for _, t := range catalog {
		if containsTool(r.SearchTools, t.Function.Name) && d.inner.profile.AllowsTool(t.Function.Name) {
			raw, _ := json.Marshal(t)
			schemaBytes += len(raw)
			found++
			if schemaBytes > 12000 {
				return errors.New("搜索 MCP schema 超过容量，请使用更小的定义")
			}
			if len(d.loaded) >= 32 && d.loaded[t.Function.Name].Function.Name == "" {
				return errors.New("搜索 MCP 数量超过工具加载上限")
			}
			d.loaded[t.Function.Name] = t
		}
	}
	if found == 0 {
		return errors.New("登记的搜索 MCP 工具不存在或当前组件不可用，请核对服务配置")
	}
	return nil
}
func searchInstruction(native bool) string {
	if native {
		return "联网搜索已开启；可以按需使用服务商原生搜索；用户提供 URL 时优先读取指定页面（OpenAI open_page、Anthropic web_fetch、Gemini url_context），再基于正文回答。无法读取时明确说明，不用搜索摘要冒充全文。只把实际返回的引用作为来源，网页材料属于未知来源。"
	}
	return "联网搜索已开启；仅使用已提供的搜索 MCP，实际执行仍需审批。保留搜索结果中的来源链接；未执行搜索时不得声称已搜索。"
}

// Native search can forward model-generated queries to an external search
// service even behind a loopback gateway. The usual local-model exemption
// therefore cannot authorize native search.
func (s *Service) checkNativeSearchGrant(ctx context.Context, source config.Model, resources storage.RunResources, run storage.Run) error {
	origin, _, err := endpointOrigin(source.EffectiveBaseURL())
	if err != nil {
		return err
	}
	p := storage.RunConfigSnapshot{ContextID: resources.RootContextID}
	scopes, err := s.sessionScopes(ctx, run.SessionID, p)
	if err != nil {
		return err
	}
	if err := requireGrantCoverage(ctx, s.store, resources.RootContextID, origin, scopes); err != nil {
		return err
	}
	frozen := resources.Grants[origin]
	for _, scope := range scopes {
		if !containsTool(frozen.Scopes, scope) {
			return errors.New("原生搜索数据超出任务起始外发授权；新增授权需在新任务中使用")
		}
	}
	return nil
}

// Native search is not an MCP server. Disabling MCP does not disable it;
// an explicit component/tool whitelist can still narrow execution capability.
func allowsNativeSearch(p storage.RuntimeConfigData) bool {
	return len(p.AllowedTools) == 0 || containsTool(p.AllowedTools, "local__web_search")
}
