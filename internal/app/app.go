// Package app hosts the UI-agnostic application service: run orchestration,
// session and context management, startup recovery and the single-host
// guarantee. The Wails binding layer and the future CLI/IPC client both talk
// to this service instead of owning any run state themselves.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/materials"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/playback"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// Event is a post-commit notification about persisted state. Clients may
// receive duplicates; consumers deduplicate by session sequence and run
// status when they need exactly-once semantics.
type Event struct {
	Type      string `json:"type"` // run.*, message.*, project.changed, project.deleted
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	Detail    string `json:"detail,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
}

type Options struct {
	Playback   *playback.Manager
	Store      *storage.Store
	Runs       *agent.Runs
	Tools      agent.ToolExecutor
	Skills     *skills.Catalog
	Chat       agent.ChatModel
	Discovery  model.ModelLister
	Config     config.Config
	ConfigPath string
	// DataDir is the application data directory; it is protected as a
	// credential source by the tool classifier.
	DataDir string
	// MCPEndpoints is the attached MCP hub surface used to display servers
	// and keep their fingerprints registered. *mcptools.Hub satisfies it.
	MCPEndpoints MCPEndpointSource
	// MCPManager is the mutable hub surface used to save or remove MCP
	// servers and connect them on demand. *mcptools.Hub satisfies it; nil
	// disables server administration (tests).
	MCPManager MCPManager
	// CredentialPath is the data-directory file holding remote provider API
	// keys; empty disables the credential store (tests).
	CredentialPath string
	// InitError carries a startup failure (e.g. the database could not be
	// opened) so the UI can surface it while remaining operational enough
	// to report status.
	InitError error
}

type Service struct {
	playback   *playback.Manager
	store      *storage.Store
	runs       *agent.Runs
	tools      agent.ToolExecutor
	skills     *skills.Catalog
	chat       agent.ChatModel
	discovery  model.ModelLister
	configPath string
	initErr    error
	credStore  *credentialStore
	// protectedPaths are the locations the tool classifier always treats as
	// credential sources (the data dir, conventional credential stores).
	protectedPaths []string
	endpoints      MCPEndpointSource
	manager        MCPManager
	// dataDir hosts logs and the GUI recovery journal; empty disables both.
	dataDir           string
	projectRecoveryMu sync.Mutex

	mu         sync.RWMutex
	config     config.Config
	mcpChanged map[string]bool

	// muStreams guards the in-memory stream merge buffer: per active run,
	// the assistant text streamed so far that is not yet durably committed.
	// Reconnecting clients read it together with the persisted state; a
	// host crash loses it by design (the run is marked interrupted).
	muStreams sync.Mutex
	streams   map[string]*strings.Builder

	// ApprovalTimeout bounds each tool approval wait inside the run loop.
	// Tests shrink it to exercise the timeout path.
	ApprovalTimeout time.Duration

	skillPublishMu sync.Mutex
	taskMu         sync.Mutex
	running        map[string]string

	muApprovals sync.Mutex
	waiters     map[string]chan struct{}

	muSubs  sync.Mutex
	subs    map[int]func(Event)
	nextSub int
}

func New(opts Options) *Service {
	timeout := 5 * time.Minute
	svc := &Service{
		playback: opts.Playback,
		store:    opts.Store, runs: opts.Runs, tools: opts.Tools, skills: opts.Skills,
		chat: opts.Chat, discovery: opts.Discovery, config: opts.Config,
		configPath: opts.ConfigPath, initErr: opts.InitError, running: map[string]string{},
		credStore:       newCredentialStore(opts.CredentialPath),
		protectedPaths:  mcptools.DefaultProtectedPaths(opts.DataDir),
		endpoints:       opts.MCPEndpoints,
		manager:         opts.MCPManager,
		dataDir:         opts.DataDir,
		mcpChanged:      map[string]bool{},
		ApprovalTimeout: timeout,
		streams:         map[string]*strings.Builder{},
		waiters:         map[string]chan struct{}{},
		subs:            map[int]func(Event){},
	}
	if svc.store != nil && svc.skills != nil && svc.initErr == nil {
		if err := svc.restoreManagedSkills(); err != nil {
			svc.initErr = fmt.Errorf("恢复已发布Skill: %w", err)
		}
	}
	return svc
}

// Subscribe registers a listener for post-commit events and returns an
// unsubscribe function.
func (s *Service) Subscribe(fn func(Event)) func() {
	s.muSubs.Lock()
	defer s.muSubs.Unlock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	return func() {
		s.muSubs.Lock()
		defer s.muSubs.Unlock()
		delete(s.subs, id)
	}
}

func (s *Service) publish(evt Event) {
	s.muSubs.Lock()
	defer s.muSubs.Unlock()
	for _, fn := range s.subs {
		fn(evt)
	}
}

// Recover converges records left behind by an abrupt previous exit: running
// runs become interrupted, in-flight tool calls become execution_unknown and
// un-dispatched calls become cancelled. It must run before any new Run starts.
func (s *Service) Recover(ctx context.Context) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	return s.store.RecoverInterrupted(ctx)
}

func (s *Service) checkReady() error {
	if s.store == nil {
		return errors.New("本地数据库未就绪")
	}
	return nil
}

func (s *Service) GetStatus() AppStatus {
	s.mu.RLock()
	source := s.config.Model
	s.mu.RUnlock()
	status := AppStatus{Name: "IceTudio", StorageReady: s.initErr == nil && s.store != nil, RuntimeReady: s.initErr == nil && s.store != nil && source.Name != "" && s.tools != nil, Model: source, AvailableModels: []string{}}
	if s.initErr != nil {
		status.Message = "本地数据初始化失败：" + s.initErr.Error()
		return status
	}
	if s.discovery == nil {
		status.Message = "模型发现未配置"
		return status
	}
	models, err := s.discovery.List(context.Background(), source)
	if err != nil {
		status.Message = "模型未连接：" + err.Error()
		return status
	}
	status.ModelConnected = true
	status.AvailableModels = models
	if len(models) == 0 {
		status.Message = "模型服务已连接，但未发现模型。请在模型服务中加载模型。"
	} else {
		status.Message = fmt.Sprintf("模型服务已连接，发现 %d 个模型。选好模型后可发送任务。", len(models))
	}
	return status
}

type AppStatus struct {
	Name            string       `json:"name"`
	StorageReady    bool         `json:"storageReady"`
	RuntimeReady    bool         `json:"runtimeReady"`
	ModelConnected  bool         `json:"modelConnected"`
	Model           config.Model `json:"model"`
	AvailableModels []string     `json:"availableModels"`
	Message         string       `json:"message"`
}

func (s *Service) SaveModelConfig(next config.Model) (AppStatus, error) {
	if err := s.checkReady(); err != nil {
		return AppStatus{}, err
	}
	s.mu.RLock()
	updated := s.config
	s.mu.RUnlock()
	updated.Model = next
	if err := config.Save(s.configPath, updated); err != nil {
		return AppStatus{}, err
	}
	s.mu.Lock()
	s.config.Model = next
	s.mu.Unlock()
	return s.GetStatus(), nil
}

// ListMessages returns the display projection of a session: the persisted
// messages plus one synthetic entry per tool call. The synthetic entries keep
// the transcript readable without reintroducing free-form tool rows; the
// authoritative records stay in messages and tool_calls.
func (s *Service) ListMessages(sessionID string) ([]storage.Message, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if err := s.checkSessionExists(sessionID); err != nil {
		return nil, err
	}
	ctx := context.Background()
	messages, err := s.store.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	calls, err := s.store.ListSessionToolCalls(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	byMessage := map[string][]storage.ToolCall{}
	for _, call := range calls {
		byMessage[call.MessageID] = append(byMessage[call.MessageID], call)
	}
	display := make([]storage.Message, 0, len(messages)+len(calls))
	for _, item := range messages {
		display = append(display, item)
		for _, call := range byMessage[item.ID] {
			display = append(display, storage.Message{
				ID: call.ID, SessionID: sessionID, ContextID: item.ContextID, RunID: call.RunID,
				Role: storage.RoleTool, Content: displayToolCall(call), CreatedAt: call.CreatedAt,
			})
		}
	}
	return display, nil
}

func displayToolCall(call storage.ToolCall) string {
	switch call.State {
	case storage.CallSucceeded:
		return call.ToolName + ": " + call.Result
	case storage.CallFailed:
		return call.ToolName + ": 工具错误：" + call.Error
	case storage.CallCancelled:
		return call.ToolName + ": 调用已取消：" + call.Error
	case storage.CallExecuting:
		return call.ToolName + ": 执行中…"
	case storage.CallApproved:
		return call.ToolName + ": 已批准，待执行"
	case storage.CallPending:
		return call.ToolName + ": 待审批"
	case storage.CallExecutionUnknown:
		return call.ToolName + ": 结果未知：" + call.Error
	default:
		return call.ToolName + ": 未知状态 " + call.State
	}
}

func (s *Service) ListRuns(sessionID string) ([]storage.Run, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if err := s.checkSessionExists(sessionID); err != nil {
		return nil, err
	}
	return s.store.ListRuns(context.Background(), sessionID)
}

func (s *Service) GetRun(id string) (storage.Run, error) {
	if err := s.checkReady(); err != nil {
		return storage.Run{}, err
	}
	run, err := s.store.GetRun(context.Background(), id)
	if err != nil {
		return storage.Run{}, err
	}
	if err := s.checkSessionExists(run.SessionID); err != nil {
		return storage.Run{}, err
	}
	return run, nil
}

// ListChildRuns returns the delegated subtask runs of one run, oldest first.
// The task tree renders them under their parent with component, status
// and error.
func (s *Service) ListChildRuns(runID string) ([]storage.Run, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	run, err := s.store.GetRun(context.Background(), runID)
	if err != nil {
		return nil, err
	}
	if err := s.checkSessionExists(run.SessionID); err != nil {
		return nil, err
	}
	return s.store.ListChildRuns(context.Background(), runID)
}

// ListToolCalls returns the persisted tool call records of a session, oldest
// first. These are the authoritative records the model history is rebuilt
// from.
func (s *Service) ListToolCalls(sessionID string) ([]storage.ToolCall, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if err := s.checkSessionExists(sessionID); err != nil {
		return nil, err
	}
	return s.store.ListSessionToolCalls(context.Background(), sessionID)
}

// PendingApprovals walks every session owned by the context — including the
// hidden delegation sessions — and returns the calls needing user action:
// pending approvals and unresolved unknown-result calls.
func (s *Service) PendingApprovals(contextID string) ([]storage.ToolCall, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	if contextID != "" {
		if _, err := s.store.GetLocalContext(ctx, contextID); err != nil {
			return nil, err
		}
	}
	sessions, err := s.store.ListContextSessionsAll(ctx, contextID)
	if err != nil {
		return nil, err
	}
	pending := []storage.ToolCall{}
	for _, session := range sessions {
		calls, err := s.store.ListSessionToolCalls(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		for _, call := range calls {
			if call.State == storage.CallPending || call.State == storage.CallExecutionUnknown {
				pending = append(pending, call)
			}
		}
	}
	return pending, nil
}

// ResolveToolCall records an explicit user disposition for a call whose
// result is unknown. The host records the disposition against that exact call.
func (s *Service) ResolveToolCall(callID, state, result, note string) (storage.ToolCall, error) {
	if err := s.checkReady(); err != nil {
		return storage.ToolCall{}, err
	}
	call, err := s.store.GetToolCall(context.Background(), callID)
	if err != nil {
		return storage.ToolCall{}, err
	}
	run, err := s.store.GetRun(context.Background(), call.RunID)
	if err != nil {
		return storage.ToolCall{}, err
	}
	if err := s.checkSessionExists(run.SessionID); err != nil {
		return storage.ToolCall{}, err
	}
	if err := s.store.ResolveToolCall(context.Background(), callID, state, result, note); err != nil {
		return storage.ToolCall{}, err
	}
	return s.store.GetToolCall(context.Background(), callID)
}

func (s *Service) checkSessionExists(sessionID string) error {
	_, err := s.store.GetSession(context.Background(), sessionID)
	return err
}

// StartConversationTask starts a root run. selection freezes the run's
// project edit range (docs/p3-agent-scope.md): nil or empty means the whole
// bound project at the current revision; a selection requires a bound
// project and references only existing objects.
func (s *Service) StartConversationTask(sessionID, input string, selection *ProjectScopeSelection, uploads ...materials.Upload) (storage.Run, error) {
	if err := s.checkReady(); err != nil {
		return storage.Run{}, err
	}
	files, err := materials.Prepare(uploads)
	if err != nil {
		return storage.Run{}, err
	}
	if strings.TrimSpace(input) == "" && len(files) > 0 {
		input = "请分析所附文件"
	}
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 10000 {
		return storage.Run{}, errors.New("任务内容长度必须在 1 到 10000 字节之间")
	}
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil {
		return storage.Run{}, err
	}
	scopeJSON, err := s.buildRunScope(session, selection)
	if err != nil {
		return storage.Run{}, err
	}
	conversation, err := s.store.GetConversation(context.Background(), sessionID)
	if err != nil {
		return storage.Run{}, err
	}
	if conversation.Options.WebSearch {
		availability, err := s.ConversationSearchAvailability(sessionID)
		if err != nil {
			return storage.Run{}, err
		}
		if !availability.Available {
			return storage.Run{}, errors.New(availability.Reason)
		}
	}
	if session.Kind == storage.SessionKindDelegation {
		return storage.Run{}, errors.New("子任务会话仅供查看与审批，不能作为普通对话重新执行")
	}
	if s.runs == nil || s.tools == nil || s.chat == nil {
		return storage.Run{}, errors.New("Agent 尚未初始化")
	}
	// A session with unresolved tool calls must not auto-advance: the model
	// could continue a task whose side effects are unaccounted for, and a
	// retry under a fresh call ID would dodge that accounting.
	unresolved, err := s.store.HasUnresolvedToolCalls(context.Background(), sessionID)
	if err != nil {
		return storage.Run{}, err
	}
	if unresolved {
		return storage.Run{}, errors.New("会话存在结果未知的工具调用，请先处置后继续")
	}
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if previous := s.running[sessionID]; previous != "" {
		prior, err := s.store.GetRun(context.Background(), previous)
		if err != nil || prior.Status == "running" {
			return storage.Run{}, errors.New("该会话已有运行中的任务")
		}
		delete(s.running, sessionID)
	}
	s.mu.RLock()
	defaultModel := s.config.Model
	s.mu.RUnlock()
	item, runCtx, resolved, err := s.runs.StartPrepared(context.Background(), sessionID, defaultModel, s.skills.Snapshot, scopeJSON, s.configuredSearchTools()...)
	if err != nil {
		return storage.Run{}, err
	}
	if _, err := s.store.AddMessage(context.Background(), sessionID, item.ID, storage.RoleUser, input, files...); err != nil {
		_ = s.runs.Finish(item.ID, "failed", err.Error())
		return storage.Run{}, err
	}
	s.mu.RLock()
	source := s.config.Model
	s.mu.RUnlock()
	if resolved.Model.Provider != "" {
		source = resolved.Model
	}
	if source.Name == "" {
		_ = s.runs.Finish(item.ID, "failed", "请先选择模型 ID")
		return storage.Run{}, errors.New("请先选择模型 ID")
	}
	var initialGrant storage.ExfilGrant
	if origin, local, err := endpointOrigin(source.EffectiveBaseURL()); err != nil {
		_ = s.runs.Finish(item.ID, "failed", err.Error())
		return storage.Run{}, err
	} else if !local {
		var grantErr error
		initialGrant, _, grantErr = s.store.GetExfilGrant(context.Background(), resolved.ContextID, origin)
		if grantErr != nil {
			_ = s.runs.Finish(item.ID, "failed", grantErr.Error())
			return storage.Run{}, grantErr
		}
	}

	s.running[sessionID] = item.ID
	s.publish(Event{Type: "run.started", SessionID: sessionID, RunID: item.ID})
	go s.executeTask(runCtx, item.ID, sessionID, source, resolved, initialGrant)
	return item, nil
}

func (s *Service) executeTask(ctx context.Context, runID, sessionID string, source config.Model, profile storage.RunConfigSnapshot, initialGrant storage.ExfilGrant) {
	defer func() {
		s.taskMu.Lock()
		if s.running[sessionID] == runID {
			delete(s.running, sessionID)
		}
		s.taskMu.Unlock()
		s.clearStream(runID)
	}()
	history, err := s.buildRunContext(sessionID, profile)
	if err != nil {
		_ = s.runs.Finish(runID, "failed", err.Error())
		return
	}
	if profile.Execution.Workflow == "dynamic" || profile.Execution.Workflow == "fixed" {
		err := s.executeScheduled(ctx, runID, sessionID, source, profile, history.history, nil)
		if err == nil {
			repair := func(request reworkRequest) error {
				if err := s.store.NotifyCoordination(ctx, runID, "review.rework"); err != nil {
					return err
				}
				updated, err := s.buildRunContext(sessionID, profile)
				if err != nil {
					return err
				}
				return s.executeScheduled(ctx, runID, sessionID, source, profile, updated.history, &request)
			}
			if profile.Execution.Workflow == "fixed" {
				repair = nil
			}
			err = s.requireDeliveryReview(ctx, runID, sessionID, profile, repair)
		}
		if err != nil {
			g, e := s.store.GetTaskGraph(context.WithoutCancel(ctx), runID)
			if e == nil && g.RunStatus == "running" && g.Coordination.State != "waiting" && g.Coordination.State != "blocked" && g.Coordination.State != "failed" {
				_ = s.store.AcknowledgeCoordination(context.WithoutCancel(ctx), runID, g.Coordination.EventSeq, "failed", boundedText(err.Error(), 4000))
			}
		}
		status, detail := "completed", ""
		if err != nil {
			status = "failed"
			detail = err.Error()
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
		if err := s.runs.Finish(runID, status, detail); err != nil {
			_ = s.runs.Finish(runID, "failed", err.Error())
		}
		s.publish(Event{Type: "run.updated", SessionID: sessionID, RunID: runID, Detail: "流程调度结束"})
		return
	}
	runTools := s.tools
	if hub, ok := s.tools.(*mcptools.Hub); ok {
		all, names := profile.AllMCPServers, profile.EnabledMCPServers
		if len(profile.AllowedTools) > 0 {
			all = false
			names = nil
			seen := map[string]bool{}
			for _, tool := range profile.AllowedTools {
				if profile.AllowsTool(tool) {
					server := serverOf(tool)
					if !seen[server] {
						names = append(names, server)
						seen[server] = true
					}
				}
			}
		}
		runTools = hub.ForServers(all, names)
	}
	runner := newDiscoveryTools(bookkeepingTools{inner: runTools, svc: s, profile: profile, sessionID: sessionID, runID: runID})
	_, err = runner.Definitions(ctx)
	if err != nil {
		_ = s.runs.Finish(runID, "failed", err.Error())
		return
	}
	if err := s.SyncMCPEndpoints(ctx); err != nil {
		_ = s.runs.Finish(runID, "failed", err.Error())
		return
	}

	guarded := guardedChat{svc: s, runID: runID, inner: s.chat, payloadCheck: func(c context.Context, m []model.Message, t []model.Tool) error {
		return s.checkPayload(c, source, profile.ContextID, sessionID, m, t)
	}, check: func(checkCtx context.Context) error {
		// Every model request re-checks the exfiltration scope: tool
		// results and policy changes since the previous turn apply.
		if err := s.exfilCheck(checkCtx, sessionID, source, profile); err != nil {
			return err
		}
		return s.checkInitialGrant(checkCtx, sessionID, source, profile, initialGrant)
	}}
	engine := agent.Engine{
		Model:    guarded,
		Tools:    runner,
		Recorder: runRecorder{svc: s, runID: runID, sessionID: sessionID, classifier: s.classifierFor(profile)},
		Gate:     toolGate{svc: s, contextID: profile.ContextID, snapshot: &profile, runID: runID},
		Approver: s,
		OnDelta: func(text string) error {
			// Streamed fragments are broadcast live and merged in host
			// memory; the durable message commit stays with Recorder.
			s.appendStream(runID, sessionID, text)
			return nil
		},
		Compactor:      sessionCompactor{svc: s, runID: runID, sessionID: sessionID, chat: guarded},
		ContextBudget:  profile.ContextBudget,
		InitialSummary: history.summary,
	}
	s.mu.RLock()
	engine.ApprovalTimeout = s.ApprovalTimeout
	s.mu.RUnlock()
	_, err = engine.Run(ctx, source, history.history)
	s.publish(Event{Type: "run.updated", SessionID: sessionID, RunID: runID, Detail: "模型循环结束"})
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		_ = s.runs.Finish(runID, "failed", "根任务超过配置的时间预算")
		return
	}
	if err != nil {
		_ = s.runs.Finish(runID, "failed", err.Error())
		return
	}
	if err := s.requireDeliveryReview(ctx, runID, sessionID, profile, func(_ reworkRequest) error {
		updated, err := s.buildRunContext(sessionID, profile)
		if err != nil {
			return err
		}
		engine.InitialSummary = updated.summary
		_, err = engine.Run(ctx, source, updated.history)
		return err
	}); err != nil {
		_ = s.runs.Finish(runID, "failed", err.Error())
		return
	}
	if err := s.runs.Finish(runID, "completed", ""); err != nil {
		_ = s.runs.Finish(runID, "failed", err.Error())
	}
}

// appendStream merges one streamed fragment into the run's in-memory buffer
// and broadcasts it. The buffer only ever holds text that is not yet durably
// committed; Recorder clears it the moment the message lands.
func (s *Service) appendStream(runID, sessionID, text string) {
	s.muStreams.Lock()
	buffer, ok := s.streams[runID]
	if !ok {
		buffer = &strings.Builder{}
		s.streams[runID] = buffer
	}
	buffer.WriteString(text)
	s.muStreams.Unlock()
	s.publish(Event{Type: "stream.delta", SessionID: sessionID, RunID: runID, Detail: text})
}

// clearStream drops the run's in-memory text; it is either durable by now or
// the run is over.
func (s *Service) clearStream(runID string) {
	s.muStreams.Lock()
	delete(s.streams, runID)
	s.muStreams.Unlock()
}

// PendingStreamText returns the assistant text streamed for a run that has
// not been committed yet. Reconnecting clients render it together with the
// persisted messages; an empty result means everything is durable or the run
// is not streaming.
func (s *Service) PendingStreamText(runID string) (string, error) {
	if err := s.checkReady(); err != nil {
		return "", err
	}
	run, err := s.store.GetRun(context.Background(), runID)
	if err != nil {
		return "", err
	}
	if err := s.checkSessionExists(run.SessionID); err != nil {
		return "", err
	}
	s.muStreams.Lock()
	defer s.muStreams.Unlock()
	if buffer, ok := s.streams[runID]; ok {
		return buffer.String(), nil
	}
	return "", nil
}

// classifierFor builds the effect/sensitivity classifier for one run from
// the context's permission scope; the run keeps using this snapshot even if
// the policy changes mid-run.
func (s *Service) classifierFor(profile storage.RunConfigSnapshot) mcptools.Classifier {
	return mcptools.Classifier{Workspace: profile.Policy.Permissions.WorkspacePaths, Protected: s.protectedPaths}
}

func (s *Service) CancelTask(id string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	run, err := s.GetRun(id)
	if err != nil {
		return err
	}
	if err := s.runs.Cancel(id); err != nil {
		return err
	}
	s.publish(Event{Type: "run.updated", SessionID: run.SessionID, RunID: id, Detail: "已请求取消"})
	return nil
}

// WaitIdle blocks until no session owned by this service is executing. Tests
// use it to avoid sleeping on the asynchronous run loop.
func (s *Service) WaitIdle() {
	for {
		s.taskMu.Lock()
		count := len(s.running)
		s.taskMu.Unlock()
		if count == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
