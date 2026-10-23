package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

// guardedChat wraps the model client so every request — every turn of the
// loop, not just the first — re-checks the session's data exfiltration
// authorization against the actual endpoint. Tool results and policy changes
// since the previous turn are accounted for automatically. The check runs
// before the inner call on both the plain and the streaming path, so a
// denied send never reaches an adapter.
type guardedChat struct {
	svc          *Service
	runID        string
	inner        agent.ChatModel
	check        func(context.Context) error
	payloadCheck func(context.Context, []model.Message, []model.Tool) error
}

var (
	_ agent.ChatModel          = guardedChat{}
	_ agent.StreamingChatModel = guardedChat{}
)

func (g guardedChat) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	return g.callModel(ctx, source, messages, tools, nil)
}
func (g guardedChat) ChatStream(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool, onDelta func(string) error) (model.ChatResponse, error) {
	return g.callModel(ctx, source, messages, tools, onDelta)
}

// endpointOrigin normalizes a model base URL to scheme://host:port and
// reports whether the endpoint is on this machine (loopback), which is not
// an exfiltration at all.
func endpointOrigin(baseURL string) (string, bool, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false, fmt.Errorf("无法解析模型地址 %q", baseURL)
	}
	host := parsed.Hostname()
	loopback := host == "localhost" || host == "::1" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
	return parsed.Scheme + "://" + parsed.Host, loopback, nil
}

// exfilCheck decides whether this session's accumulated context may be sent
// to the model endpoint. Local loopback models are exempt: the data never
// leaves the machine. Remote endpoints need a grant whose scopes cover every
// scope accumulated in the session; an unknown scope can only be covered by
// a grant that explicitly includes it.
func (s *Service) exfilCheck(ctx context.Context, sessionID string, source config.Model, profile storage.RunConfigSnapshot) error {
	// Remote providers may leave BaseURL empty to mean the official
	// endpoint; the check compares against the effective origin, never the
	// provider name.
	origin, loopback, err := endpointOrigin(source.EffectiveBaseURL())
	if err != nil {
		return err
	}
	if loopback {
		return nil
	}
	scopes, err := s.sessionScopes(ctx, sessionID, profile)
	if err != nil {
		return err
	}
	if len(scopes) == 0 {
		return nil
	}
	return requireGrantCoverage(ctx, s.store, profile.ContextID, origin, scopes)
}

// requireGrantCoverage verifies one Context's grant for origin covers every
// scope; the grant is keyed to the actual endpoint, never a provider name.
func requireGrantCoverage(ctx context.Context, store *storage.Store, contextID, origin string, scopes []string) error {
	grant, found, err := store.GetExfilGrant(ctx, contextID, origin)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("数据外发被拒绝：未授权向 %s 发送会话数据（累计范围：%s）。请先授权该目的地，或改用本机模型", origin, strings.Join(scopes, "、"))
	}
	allowed := map[string]bool{}
	for _, scope := range grant.Scopes {
		allowed[scope] = true
	}
	missing := []string{}
	for _, scope := range scopes {
		if !allowed[scope] {
			missing = append(missing, scope)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("数据外发被拒绝：%s 未包含在向 %s 的授权范围（%s）内。请调整授权或改用本机模型", strings.Join(missing, "、"), origin, strings.Join(grant.Scopes, "、"))
	}
	return nil
}

// sessionScopes accumulates the data scopes present in a session's context:
// user input is personal, successful tool results carry the sensitivity
// their tool access touched (tagged at persistence time), and the system
// prompt's skills summary is workspace-scoped.
func (s *Service) sessionScopes(ctx context.Context, sessionID string, profile storage.RunConfigSnapshot) ([]string, error) {
	hasUser, toolScopes, err := s.store.SessionDataScopes(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, scope := range profile.ConfigScopes {
		set[scope] = true
	}
	if hasUser || profile.SystemPrompt != "" || profile.Persona != "" {
		set[storage.ScopePersonal] = true
	}
	for _, scope := range toolScopes {
		set[scope] = true
	}
	if profile.AllowsTool("local__load_skill") {
		for _, skill := range profile.FrozenSkills {
			for _, scope := range skill.Scopes {
				set[scope] = true
			}
		}
		summary := s.snapshotSkillSummary(profile)
		if summary != "" {
			set[storage.ScopeWorkspace] = true
		}
	}
	scopes := make([]string, 0, len(set))
	for scope := range set {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes, nil
}

// GrantExfil records (or tightens/extends) the persistent authorization for
// one destination. The destination is normalized to its endpoint origin; a
// provider name is never sufficient — the actual endpoint is what the send
// check compares against.
func (s *Service) GrantExfil(contextID, destination string, scopes []string) (storage.ExfilGrant, error) {
	if err := s.checkReady(); err != nil {
		return storage.ExfilGrant{}, err
	}
	origin, _, err := endpointOrigin(strings.TrimSpace(destination))
	if err != nil {
		return storage.ExfilGrant{}, fmt.Errorf("目的地必须是 http(s) 端点地址: %w", err)
	}
	cleaned := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if !storage.ValidScope(scope) {
			return storage.ExfilGrant{}, fmt.Errorf("未知的数据范围 %q", scope)
		}
		cleaned = append(cleaned, scope)
	}
	grant := storage.ExfilGrant{ContextID: contextID, Destination: origin, Scopes: cleaned, CreatedAt: ""}
	if err := s.store.SetExfilGrant(context.Background(), grant); err != nil {
		return storage.ExfilGrant{}, err
	}
	saved, _, err := s.store.GetExfilGrant(context.Background(), contextID, origin)
	return saved, err
}

func (s *Service) ListExfilGrants(contextID string) ([]storage.ExfilGrant, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if _, err := s.store.GetLocalContext(context.Background(), contextID); err != nil {
		return nil, err
	}
	return s.store.ListExfilGrants(context.Background(), contextID)
}

func (s *Service) RevokeExfil(contextID, destination string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	origin, _, err := endpointOrigin(strings.TrimSpace(destination))
	if err != nil {
		return fmt.Errorf("目的地必须是 http(s) 端点地址: %w", err)
	}
	return s.store.DeleteExfilGrant(context.Background(), contextID, origin)
}

// External tool descriptions/schemas are untrusted input, including before any
// tool call. They require explicit unknown-source authorization for this Context.
// checkPayload accounts for untrusted payload scopes under the run context.
func (s *Service) checkPayload(ctx context.Context, source config.Model, contextID string, sessionID string, messages []model.Message, tools []model.Tool) error {
	origin, local, err := endpointOrigin(source.EffectiveBaseURL())
	if err != nil {
		return err
	}
	required := map[string]bool{}
	for _, m := range messages {
		if len(m.Files) > 0 {
			required[storage.ScopePersonal] = true
			required[storage.ScopeUnknown] = true
		}
		if m.Role == "system" && m.Content != "" {
			required[storage.ScopePersonal] = true
		}
		for _, c := range m.ToolCalls {
			if c.Function.Arguments != "" {
				required[storage.ScopeUnknown] = true
			}
		}
	}
	for _, t := range tools {
		if !strings.HasPrefix(t.Function.Name, "local__") {
			required[storage.ScopeUnknown] = true
		}
	}
	scopes := make([]string, 0, len(required))
	for scope := range required {
		scopes = append(scopes, scope)
	}
	if err := s.store.AccumulateContextScopes(ctx, sessionID, scopes); err != nil {
		return err
	}
	if local {
		return nil
	}
	grant, found, err := s.store.GetExfilGrant(ctx, contextID, origin)
	if err != nil {
		return err
	}
	covered := map[string]bool{}
	for _, v := range grant.Scopes {
		covered[v] = true
	}
	for scope := range required {
		if !covered[scope] {
			return fmt.Errorf("数据外发被拒绝：实际请求包含未授权的提示词、工具参数或外部工具定义（Context %s 缺少 %s 授权）", contextID, scope)
		}
	}
	if !found {
		return fmt.Errorf("数据外发被拒绝：Context %s 未授权向 %s 发送外部工具定义", contextID, origin)
	}
	return nil
}

func (s *Service) checkInitialGrant(ctx context.Context, sessionID string, source config.Model, profile storage.RunConfigSnapshot, initial storage.ExfilGrant) error {
	_, local, err := endpointOrigin(source.EffectiveBaseURL())
	if err != nil {
		return err
	}
	if local {
		return nil
	}
	scopes, err := s.sessionScopes(ctx, sessionID, profile)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, scope := range initial.Scopes {
		allowed[scope] = true
	}
	for _, scope := range scopes {
		if !allowed[scope] {
			return fmt.Errorf("数据外发范围超出本次任务开始时的授权，请在新任务中使用新增授权")
		}
	}
	return nil
}

func usageTokens(u model.Usage) int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	if u.PromptTokens >= 0 && u.CompletionTokens >= 0 {
		return u.PromptTokens + u.CompletionTokens
	}
	return 0
}
