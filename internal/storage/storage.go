package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"icetudio/internal/config"
	"icetudio/internal/materials"
)

// currentVersion is the schema version this binary writes. Opening a database
// with a different version is refused; development releases do not migrate data.
const currentVersion = 29

type Store struct {
	db   *sql.DB
	path string
}

type Session struct {
	ID        string `json:"id"`
	ContextID string `json:"contextId"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"createdAt"`
	// ProjectID is the explicit conversation→project binding; empty means
	// the conversation is not (yet) working on a project.
	ProjectID string `json:"projectId"`
}

// MessageToolCall mirrors a model tool call request persisted on an assistant
// message. Tool results live in ToolCall rows, never in this projection.
type MessageToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Attachments   []materials.File  `json:"attachments,omitempty"`
	ProviderState []json.RawMessage `json:"-"`
	ID            string            `json:"id"`
	SessionID     string            `json:"sessionId"`
	ContextID     string            `json:"contextId"`
	RunID         string            `json:"runId"`
	Seq           int               `json:"seq"`
	Role          string            `json:"role"`
	Content       string            `json:"content"`
	ToolCalls     []MessageToolCall `json:"toolCalls,omitempty"`
	CreatedAt     string            `json:"createdAt"`
}

type Run struct {
	WorkerName       string `json:"workerName,omitempty"`
	ComponentVersion int    `json:"componentVersion"`
	Component        string `json:"component"`
	ID               string `json:"id"`
	SessionID        string `json:"sessionId"`
	ContextID        string `json:"contextId"`
	ConfigID         string `json:"profileId"`
	ConfigRevision   int    `json:"profileRevision"`
	// ParentRunID links a delegated subtask run to the run whose tool call
	// started it; top-level runs leave it empty.
	ParentRunID string `json:"parentRunId,omitempty"`
	// Depth is 0 for top-level runs and increments per delegation level.
	Depth     int    `json:"depth"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.initialize(); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// withTx runs fn in a single transaction and commits it. Related facts must
// commit together or not at all, so every multi-row write goes through here.
func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateContextSession(ctx context.Context, contextID, profileID, title string) (Session, error) {
	if title == "" {
		title = "新会话"
	}
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	item := Session{ID: id, ContextID: contextID, Title: title, Kind: SessionKindNormal, CreatedAt: now()}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := activeRuntimeConfig(ctx, tx, profileID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, context_id, config_id, kind, title, created_at) SELECT ?, id, ?, 'normal', ?, ? FROM contexts WHERE id = ?`, item.ID, profileID, item.Title, item.CreatedAt, contextID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("本机上下文不存在")
		}
		return nil
	})
	return item, err
}

// CreateDelegationSession opens a separate transcript under the root Context.
// It is excluded from the normal conversation list; messages remain for audit.
func (s *Store) CreateDelegationSession(ctx context.Context, contextID, profileID, title string) (Session, error) {
	if title == "" {
		title = "委派子任务"
	}
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	item := Session{ID: id, ContextID: contextID, Title: title, Kind: SessionKindDelegation, CreatedAt: now()}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, context_id, config_id, kind, title, created_at) SELECT ?, id, ?, 'delegation', ?, ? FROM contexts WHERE id = ?`, item.ID, profileID, item.Title, item.CreatedAt, contextID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("本机上下文不存在")
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return item, nil
}

// ListContextSessions returns the context's normal sessions. Delegation sessions
// are temporary subtask contexts owned by runs, not conversation history.
func (s *Store) ListContextSessions(ctx context.Context, contextID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, context_id, title, kind, created_at, COALESCE(project_id,'') FROM sessions WHERE context_id = ? AND kind = 'normal' ORDER BY created_at DESC, id DESC`, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Session{}
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.ContextID, &item.Title, &item.Kind, &item.CreatedAt, &item.ProjectID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListContextSessionsAll returns every session of the context including hidden
// delegation sessions; the approval sweep needs those to surface subtask
// approval requests.
func (s *Store) ListContextSessionsAll(ctx context.Context, contextID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, context_id, title, kind, created_at, COALESCE(project_id,'') FROM sessions WHERE (?='' OR context_id = ?) ORDER BY created_at DESC, id DESC`, contextID, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Session{}
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.ContextID, &item.Title, &item.Kind, &item.CreatedAt, &item.ProjectID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (Session, error) {
	var item Session
	err := s.db.QueryRowContext(ctx, `SELECT id, context_id, title, kind, created_at, COALESCE(project_id,'') FROM sessions WHERE id = ?`, sessionID).Scan(&item.ID, &item.ContextID, &item.Title, &item.Kind, &item.CreatedAt, &item.ProjectID)
	return item, err
}

// AddMessage persists a user or assistant message at the next per-session
// sequence number. Assistant tool call requests are persisted via
// AddAssistantMessage so the call rows and the message commit atomically.
func (s *Store) AddMessage(ctx context.Context, sessionID, runID, role, content string, files ...materials.File) (Message, error) {
	if role != RoleUser && role != RoleAssistant {
		return Message{}, errors.New("无效的消息角色")
	}
	id, err := newID()
	if err != nil {
		return Message{}, err
	}
	created := now()
	item := Message{ID: id, SessionID: sessionID, RunID: runID, Role: role, Content: content, CreatedAt: created}
	err = s.withTx(ctx, func(tx *sql.Tx) error { return addMessageInTx(ctx, tx, &item, files) })
	if err != nil {
		return Message{}, err
	}
	return item, nil
}

func addMessageInTx(ctx context.Context, tx *sql.Tx, item *Message, files []materials.File) error {
	sessionID, runID, role, content, created := item.SessionID, item.RunID, item.Role, item.Content, item.CreatedAt
	err := tx.QueryRowContext(ctx, `INSERT INTO messages (id, session_id, run_id, seq, role, content, tool_calls, created_at)
			SELECT ?, id, ?, COALESCE((SELECT MAX(seq) FROM messages WHERE session_id = ?), 0) + 1, ?, ?, '', ? FROM sessions WHERE id = ?
			RETURNING (SELECT context_id FROM sessions WHERE id=session_id), seq`,
		item.ID, runID, sessionID, role, content, created, sessionID).
		Scan(&item.ContextID, &item.Seq)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		if role != RoleUser {
			return errors.New("仅用户消息可包含附件")
		}
		raw, e := json.Marshal(files)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO message_files(message_id,payload) VALUES(?,?)`, item.ID, string(raw)); e != nil {
			return e
		}
		for _, scope := range []string{ScopePersonal, ScopeUnknown} {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_context_scopes(session_id,scope) VALUES(?,?)`, sessionID, scope); err != nil {
				return err
			}
		}
		for _, f := range files {
			item.Attachments = append(item.Attachments, f.Metadata())
		}
	}
	if role == RoleUser {
		title := []rune(strings.Join(strings.Fields(content), " "))
		if len(title) > 32 {
			title = append(title[:32], '…')
		}
		if len(title) > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET title=? WHERE id=? AND kind='normal' AND title='新对话' AND (SELECT count(*) FROM messages WHERE session_id=? AND role='user')=1`, string(title), sessionID, sessionID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE task_roots SET goal=? WHERE run_id=? AND goal=''`, content, runID)
	}
	return err
}

// AddAssistantMessage persists an assistant message together with the tool
// calls it requested. Every call gets a pending ToolCall row and a tool Step
// in the same transaction; if any part fails nothing is persisted, so a
// partial batch can never be replayed later.
func (s *Store) AddAssistantMessage(ctx context.Context, sessionID, runID, content string, calls []MessageToolCall, providerState []json.RawMessage) (Message, []ToolCall, error) {
	seen := map[string]bool{}
	for _, call := range calls {
		if call.ID == "" || seen[call.ID] {
			return Message{}, nil, errors.New("模型工具调用 ID 缺失或重复")
		}
		seen[call.ID] = true
	}
	id, err := newID()
	if err != nil {
		return Message{}, nil, err
	}
	created := now()
	item := Message{ID: id, SessionID: sessionID, RunID: runID, Role: RoleAssistant, Content: content, ProviderState: providerState, ToolCalls: append([]MessageToolCall{}, calls...), CreatedAt: created}
	payload, err := json.Marshal(item.ToolCalls)
	if err != nil {
		return Message{}, nil, err
	}
	stateRaw, err := json.Marshal(providerState)
	if err != nil {
		return Message{}, nil, err
	}
	if len(stateRaw) > 512<<10 {
		return Message{}, nil, errors.New("模型协议状态超过 512 KiB")
	}
	rows := make([]ToolCall, 0, len(calls))
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO messages (id, session_id, run_id, seq, role, content, tool_calls, created_at, provider_state)
			SELECT ?, id, ?, COALESCE((SELECT MAX(seq) FROM messages WHERE session_id = ?), 0) + 1, ?, ?, ?, ?, ? FROM sessions WHERE id = ?
			RETURNING (SELECT context_id FROM sessions WHERE id=session_id), seq`,
			item.ID, runID, sessionID, RoleAssistant, content, string(payload), created, string(stateRaw), sessionID).
			Scan(&item.ContextID, &item.Seq); err != nil {
			return err
		}
		for _, call := range calls {
			stepID, err := newID()
			if err != nil {
				return err
			}
			callRowID, err := newID()
			if err != nil {
				return err
			}
			callCreated := now()
			if _, err := tx.ExecContext(ctx, `INSERT INTO steps (id, run_id, seq, kind, status, error, payload, created_at, updated_at)
				SELECT ?, ?, COALESCE((SELECT MAX(seq) FROM steps WHERE run_id = ?), 0) + 1, 'tool', 'pending', '', '', ?, ?`,
				stepID, runID, runID, callCreated, callCreated); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO tool_calls (id, run_id, step_id, message_id, call_id, tool_name, arguments, state, result, error, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', '', '', ?, ?)`,
				callRowID, runID, stepID, item.ID, call.ID, call.Name, call.Arguments, callCreated, callCreated); err != nil {
				return err
			}
			rows = append(rows, ToolCall{
				ID: callRowID, RunID: runID, StepID: stepID, MessageID: item.ID,
				CallID: call.ID, ToolName: call.Name, Arguments: call.Arguments,
				State: "pending", CreatedAt: callCreated, UpdatedAt: callCreated,
			})
		}
		return nil
	})
	if err != nil {
		return Message{}, nil, err
	}
	return item, rows, nil
}

const messageSelect = `SELECT id, session_id, (SELECT context_id FROM sessions WHERE id=messages.session_id), run_id, seq, role, content, tool_calls, created_at, provider_state, COALESCE((SELECT payload FROM message_files WHERE message_id=messages.id),'[]') FROM messages WHERE session_id = ?`

func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	return s.listMessages(ctx, messageSelect+` ORDER BY seq`, sessionID)
}

func (s *Store) ChatMessages(ctx context.Context, sessionID string, before, limit int) ([]Message, error) {
	if limit < 1 || limit > 101 || before < 0 {
		return nil, errors.New("无效的消息分页")
	}
	query := messageSelect + ` AND role IN ('user','assistant') AND tool_calls IN ('','[]') AND (content<>'' OR EXISTS(SELECT 1 FROM message_files WHERE message_id=messages.id))`
	args := []any{sessionID}
	if before > 0 {
		query += ` AND seq<?`
		args = append(args, before)
	}
	query += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit)
	items, err := s.listMessages(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	slices.Reverse(items)
	return items, nil
}

func (s *Store) listMessages(ctx context.Context, query string, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Message{}
	for rows.Next() {
		var item Message
		var payload string
		var stateRaw, filesRaw string
		if err := rows.Scan(&item.ID, &item.SessionID, &item.ContextID, &item.RunID, &item.Seq, &item.Role, &item.Content, &payload, &item.CreatedAt, &stateRaw, &filesRaw); err != nil {
			return nil, err
		}
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &item.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析消息工具调用: %w", err)
			}
		}
		if err := json.Unmarshal([]byte(stateRaw), &item.ProviderState); err != nil {
			return nil, err
		}
		var files []materials.File
		if err := json.Unmarshal([]byte(filesRaw), &files); err != nil {
			return nil, err
		}
		for _, f := range files {
			item.Attachments = append(item.Attachments, f.Metadata())
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreateRunOptions distinguishes top-level runs from delegated subtask runs.
type CreateRunOptions struct {
	Inspection     *InspectionRequest
	SearchTools    []string
	Component      string
	TaskSpec       *TaskSpec
	SnapshotSkills SkillSnapshotter
	DefaultModel   config.Model
	WorkerName     string
	ParentRunID    string
	Depth          int
}

// CreateRun starts a run for a session and resolves the owning Context's
// current Profile inside the same transaction: the run row records the
// profile id, revision and a full content snapshot, so in-flight approvals
// and historical runs read exactly what they started with, even after the
// Profile is later edited or deleted. The returned RunConfigSnapshot is the
// snapshot the engine must execute with.
func (s *Store) CreateRun(ctx context.Context, sessionID string) (Run, RunConfigSnapshot, error) {
	return s.CreateRunWithOptions(ctx, sessionID, CreateRunOptions{})
}

func (s *Store) CreateRunWithOptions(ctx context.Context, sessionID string, opts CreateRunOptions) (Run, RunConfigSnapshot, error) {
	id, err := newID()
	if err != nil {
		return Run{}, RunConfigSnapshot{}, err
	}
	item := Run{ID: id, SessionID: sessionID, ParentRunID: opts.ParentRunID, Depth: opts.Depth, Status: "running", CreatedAt: now(), UpdatedAt: now(), ComponentVersion: 1, WorkerName: opts.WorkerName}
	var resolved RunConfigSnapshot
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE session_id=? AND status='running'`, sessionID).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return errors.New("该会话已有运行中的任务")
		}
		var contextID, contextName, profileID, rawPolicy, kind string
		if err := tx.QueryRowContext(ctx, `SELECT a.id,a.name,s.config_id,a.policy,s.kind FROM sessions s JOIN contexts a ON a.id=s.context_id WHERE s.id=?`, sessionID).Scan(&contextID, &contextName, &profileID, &rawPolicy, &kind); err != nil {
			return err
		}
		var policy ContextPolicy
		if err := json.Unmarshal([]byte(rawPolicy), &policy); err != nil {
			return err
		}
		item.Component = "main"
		var frozen RuntimeConfigRevision
		var resources RunResources
		var resourcesRaw string
		if opts.Inspection != nil && (opts.ParentRunID != "" || opts.Component != "" || opts.Depth != 0) {
			return errors.New("独立审查/复盘只能创建根运行")
		}
		frozenMode := false
		if opts.TaskSpec != nil && (opts.ParentRunID == "" || opts.Depth != 1 || opts.WorkerName == "") {
			return errors.New("计划执行仅支持根任务的 worker 子运行")
		}
		if opts.ParentRunID != "" {
			item.Component = "worker"
			var parentStatus, parentContext string
			var parentDepth int
			if err := tx.QueryRowContext(ctx, `SELECT status,(SELECT context_id FROM sessions WHERE id=runs.session_id),depth FROM runs WHERE id=?`, opts.ParentRunID).Scan(&parentStatus, &parentContext, &parentDepth); err != nil {
				return err
			}
			if parentStatus != "running" || kind != SessionKindDelegation || opts.Depth != parentDepth+1 {
				return errors.New("子任务的父运行、会话或深度无效")
			}
			var err error
			resources, err = resourcesInTx(ctx, tx, opts.ParentRunID)
			if err != nil {
				return err
			}
			if opts.WorkerName != "" {
				if parentDepth != 0 || contextID != parentContext {
					return errors.New("worker 槽位仅用于同一上下文的直接子任务")
				}
				var ok bool
				if opts.Component == "planner" && opts.WorkerName == "@planner" && resources.Root.Data.Execution.Workflow == "dynamic" {
					frozen = resources.Planner
					ok = frozen.ID != ""
					item.Component = "planner"
				} else if opts.Component == "reviewer" && opts.WorkerName == "@reviewer" {
					frozen = resources.Reviewer
					ok = frozen.ID != ""
					item.Component = "reviewer"
				} else {
					frozen, ok = resources.Workers[opts.WorkerName]
				}
				if !ok {
					return errors.New("worker 未包含在任务起始快照中")
				}
				if resources.Root.Data.Execution.Workflow == "fixed" && item.Component == "worker" && opts.TaskSpec == nil {
					return errors.New("固定流程 worker 必须绑定已声明阶段")
				}
				policy = resources.RootPolicy
				frozenMode = true
			} else {
				return errors.New("子运行必须选择内置组件或 worker 槽位")
			}
		}
		if !frozenMode {
			var err error
			frozen, err = readConfigRevision(ctx, tx, profileID)
			if err != nil {
				return errors.New("会话没有可用的执行配置")
			}
			if opts.Component != "" && opts.Component != "main" && opts.Component != "improver" {
				return errors.New("根任务组件无效")
			}
			if opts.Component == "improver" {
				if !frozen.Data.Execution.ImproverEnabled {
					return errors.New("当前执行配置未启用配置草案组件")
				}
				item.Component = "improver"
			}
			resources, err = captureRunResources(ctx, tx, frozen, contextID, policy, opts.DefaultModel, opts.SnapshotSkills, item.Component)
			if err != nil {
				return err
			}
			var optionsRaw string
			if err := tx.QueryRowContext(ctx, `SELECT options FROM sessions WHERE id=?`, sessionID).Scan(&optionsRaw); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(optionsRaw), &resources.Options); err != nil {
				return err
			}
			resources.SearchTools = append([]string(nil), opts.SearchTools...)
			encoded, err := json.Marshal(resources)
			if err != nil {
				return err
			}
			resourcesRaw = string(encoded)
			frozen = resources.Root
			frozenMode = true
		}
		snapshot, err := json.Marshal(frozen.Data)
		if err != nil {
			return err
		}
		var parent any
		if opts.ParentRunID != "" {
			parent = opts.ParentRunID
		}
		if opts.ParentRunID != "" && item.Component == "worker" {
			root, err := rootInTx(ctx, tx, opts.ParentRunID)
			if err != nil {
				return err
			}
			b, err := budgetInTx(ctx, tx, root)
			if err != nil {
				return err
			}
			if err := checkBudget(b); err != nil {
				return err
			}
			var count int
			if err := tx.QueryRowContext(ctx, `WITH RECURSIVE tree(id) AS(SELECT ? UNION ALL SELECT r.id FROM runs r JOIN tree ON r.parent_run_id=tree.id) SELECT count(*) FROM runs WHERE id IN tree AND component='worker' `, root).Scan(&count); err != nil {
				return err
			}
			if count >= b.Limits.MaxAttempts {
				return errors.New("根任务 worker 尝试预算耗尽")
			}
		}
		item.ComponentVersion = componentVersion(item.Component)
		persistedComponent := item.Component
		_, err = tx.ExecContext(ctx, `INSERT INTO runs(id,session_id,config_id,config_revision,config_snapshot,parent_run_id,depth,status,created_at,updated_at,component,component_version,worker_name,resources_snapshot) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, sessionID, frozen.ID, frozen.Revision, string(snapshot), parent, opts.Depth, item.Status, item.CreatedAt, item.UpdatedAt, persistedComponent, item.ComponentVersion, opts.WorkerName, resourcesRaw)
		if err != nil {
			return err
		}
		if opts.ParentRunID == "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_roots(run_id) VALUES(?)`, id); err != nil {
				return err
			}
		}
		if opts.ParentRunID == "" {
			if err := initCoordination(ctx, tx, id, frozen.Data.Execution.Budget); err != nil {
				return err
			}
		}
		if opts.ParentRunID == "" && item.Component == "main" && opts.Inspection == nil && frozen.Data.Execution.Workflow == "fixed" {
			if err := initFixedGraph(ctx, tx, id, frozen.Data.Execution.Stages); err != nil {
				return err
			}
		}
		if opts.Inspection != nil {
			if err := initInspectionRequest(ctx, tx, id, *opts.Inspection); err != nil {
				return err
			}
		}
		if opts.TaskSpec != nil {
			if err := claimTaskInTx(ctx, tx, opts.ParentRunID, *opts.TaskSpec, id, opts.WorkerName); err != nil {
				return err
			}
		}
		if opts.WorkerName != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET config_id=? WHERE id=?`, frozen.ID, sessionID); err != nil {
				return err
			}
		}
		item.ContextID, item.ConfigID, item.ConfigRevision = contextID, frozen.ID, frozen.Revision
		configScopes := append(append([]string(nil), resources.Root.Scopes...), frozen.Scopes...)
		for _, worker := range resources.Workers {
			configScopes = append(configScopes, worker.Scopes...)
			for _, skill := range worker.Skills {
				configScopes = append(configScopes, skill.Scopes...)
			}
		}
		resolved = RunConfigSnapshot{ContextID: contextID, ContextName: contextName, ConfigID: frozen.ID, ConfigName: frozen.Name, ConfigRevision: frozen.Revision, RuntimeConfigData: frozen.Data, Policy: policy, ResourcesFrozen: frozenMode, FrozenSkills: frozen.Skills, ConfigScopes: configScopes}
		resolved.Resources = &resources
		return nil
	})
	if err != nil {
		return Run{}, RunConfigSnapshot{}, err
	}
	return item, resolved, nil
}

// ProfileSnapshot returns the resolved profile content a finished or running
// run recorded at start. The second result reports whether a snapshot exists
// is present; all current runs capture it at creation.
func (s *Store) ProfileSnapshot(ctx context.Context, runID string) (RuntimeConfigData, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT config_snapshot FROM runs WHERE id = ?`, runID).Scan(&raw)
	if err != nil {
		return RuntimeConfigData{}, false, err
	}
	if raw == "" {
		return RuntimeConfigData{}, false, nil
	}
	var data RuntimeConfigData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return RuntimeConfigData{}, false, fmt.Errorf("解析运行快照: %w", err)
	}
	return data, true, nil
}

// runTransitions guards the run lifecycle.
var runTransitions = map[string][]string{
	"running": {"completed", "cancelled", "failed", "interrupted"},
}

func (s *Store) SetRunStatus(ctx context.Context, id, status, detail string) error {
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return err
	}
	allowed, ok := runTransitions[run.Status]
	if !ok {
		return fmt.Errorf("任务状态 %q 不能变更为 %q", run.Status, status)
	}
	valid := false
	for _, next := range allowed {
		if next == status {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("任务状态 %q 不能变更为 %q", run.Status, status)
	}
	// Run state and the state of its in-flight tool calls commit in one
	// transaction: a finished run never leaves un-dispatched or in-flight
	// calls behind without an explicit, persisted disposition.
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if status == "completed" && run.ParentRunID == "" {
			if err := enforceRequiredReview(ctx, tx, id); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE runs SET status = ?, error = ?, updated_at = ? WHERE id = ? AND status = ?`, status, detail, now(), id, run.Status)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("任务不存在或已结束")
		}
		if err := sweepRunArtifacts(ctx, tx, id); err != nil {
			return err
		}
		if err := settleTaskAttempt(ctx, tx, id); err != nil {
			return err
		}
		return settleTaskRoot(ctx, tx, id)
	})
}

// sweepRunArtifacts gives every non-terminal tool call and step of a finished
// run an explicit persisted state. Pending calls were never dispatched, so
// they become cancelled; executing calls may have taken effect, so they
// become execution_unknown and block further progress until resolved.
func sweepRunArtifacts(ctx context.Context, tx *sql.Tx, runID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE search_requests SET state='execution_unknown',error='任务结束时原生搜索请求未完成，结果与用量未知' WHERE run_id=? AND state='executing'`, runID); err != nil {
		return err
	}
	ts := now()
	if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state='cancelled', error='任务已结束，调用未执行', updated_at=? WHERE run_id=? AND state IN ('pending','approved')`, ts, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state='execution_unknown', error='任务结束时工具仍在执行，结果未知', updated_at=? WHERE run_id=? AND state='executing'`, ts, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steps SET status='cancelled', error='任务已结束，调用未执行', updated_at=? WHERE run_id=? AND kind='tool' AND status IN ('pending','running') AND id IN (SELECT step_id FROM tool_calls WHERE run_id=? AND state='cancelled')`, ts, runID, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steps SET status='paused', error='结果未知，等待处置', updated_at=? WHERE run_id=? AND kind='tool' AND status IN ('pending','running') AND id IN (SELECT step_id FROM tool_calls WHERE run_id=? AND state='execution_unknown')`, ts, runID, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steps SET status='failed', error='任务已结束，模型请求未完成', updated_at=? WHERE run_id=? AND kind='model' AND status='running'`, ts, runID); err != nil {
		return err
	}
	return nil
}

const runColumns = `id, session_id, (SELECT context_id FROM sessions WHERE id=runs.session_id), config_id, config_revision, parent_run_id, depth, CASE WHEN status='failed' AND (SELECT state FROM task_coordination WHERE root_run_id=runs.id) IN ('waiting','blocked') THEN (SELECT state FROM task_coordination WHERE root_run_id=runs.id) ELSE status END, error, created_at, updated_at, component, component_version, worker_name`

func scanRun(scanner interface{ Scan(...any) error }) (Run, error) {
	var item Run
	var parent sql.NullString
	err := scanner.Scan(&item.ID, &item.SessionID, &item.ContextID, &item.ConfigID, &item.ConfigRevision, &parent, &item.Depth, &item.Status, &item.Error, &item.CreatedAt, &item.UpdatedAt, &item.Component, &item.ComponentVersion, &item.WorkerName)
	if err != nil {
		return Run{}, err
	}
	item.ParentRunID = parent.String
	return item, nil
}

func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
}

func (s *Store) ListRuns(ctx context.Context, sessionID string) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM runs WHERE session_id = ? ORDER BY created_at DESC, id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Run{}
	for rows.Next() {
		item, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListChildRuns returns the delegated subtask runs of one parent run, oldest
// first; the task tree renders them under their parent.
func (s *Store) ListChildRuns(ctx context.Context, parentRunID string) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM runs WHERE parent_run_id = ? ORDER BY created_at, id`, parentRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Run{}
	for rows.Next() {
		item, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AncestorRunIDs walks the parent chain of a run, oldest ancestor first, and
// includes the run itself. Delegation uses it to refuse cycles.
func (s *Store) AncestorRunIDs(ctx context.Context, runID string) ([]string, error) {
	chain := []string{runID}
	seen := map[string]bool{runID: true}
	for {
		current := chain[0]
		var parent sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT parent_run_id FROM runs WHERE id = ?`, current).Scan(&parent)
		if err != nil {
			return nil, err
		}
		if !parent.Valid || parent.String == "" {
			return chain, nil
		}
		if seen[parent.String] {
			return chain, nil
		}
		seen[parent.String] = true
		chain = append([]string{parent.String}, chain...)
	}
}

// FirstUserMessage returns the session's first user message — the task text a
// delegated subtask was started with — for duplicate-delegation detection.
func (s *Store) FirstUserMessage(ctx context.Context, sessionID string) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx, `SELECT content FROM messages WHERE session_id = ? AND role = 'user' ORDER BY seq LIMIT 1`, sessionID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return content, err
}
