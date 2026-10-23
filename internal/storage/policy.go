package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Data scope tokens used by the exfiltration gate. A scope describes where
// content in a session came from: user input is personal, tool results carry
// the sensitivity their tool access touched, and skills metadata is
// workspace-scoped.
const (
	ScopePublic     = "public"
	ScopeWorkspace  = "workspace"
	ScopePersonal   = "personal"
	ScopeCredential = "credential"
	ScopeUnknown    = "unknown"
)

var validScopes = map[string]bool{
	ScopePublic: true, ScopeWorkspace: true, ScopePersonal: true, ScopeCredential: true, ScopeUnknown: true,
}

// ValidScope reports whether scope is a known data scope token.
func ValidScope(scope string) bool { return validScopes[scope] }

// ExfilGrant is a persistent user authorization to send one destination
// (endpoint origin) the listed data scopes. A grant never widens what a tool
// may do; it only records that the user accepted this data leaving the
// machine for that endpoint.
type ExfilGrant struct {
	ContextID   string   `json:"contextId"`
	Destination string   `json:"destination"`
	Scopes      []string `json:"scopes"`
	CreatedAt   string   `json:"createdAt"`
}

func (s *Store) ListExfilGrants(ctx context.Context, contextID string) ([]ExfilGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT context_id, destination, scopes, created_at FROM exfil_grants WHERE context_id=? ORDER BY destination`, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ExfilGrant{}
	for rows.Next() {
		item, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanGrant(scanner interface{ Scan(...any) error }) (ExfilGrant, error) {
	var item ExfilGrant
	var payload string
	if err := scanner.Scan(&item.ContextID, &item.Destination, &payload, &item.CreatedAt); err != nil {
		return ExfilGrant{}, err
	}
	if err := json.Unmarshal([]byte(payload), &item.Scopes); err != nil {
		return ExfilGrant{}, fmt.Errorf("解析外发授权范围: %w", err)
	}
	return item, nil
}

func (s *Store) GetExfilGrant(ctx context.Context, contextID, destination string) (ExfilGrant, bool, error) {
	item, err := scanGrant(s.db.QueryRowContext(ctx, `SELECT context_id, destination, scopes, created_at FROM exfil_grants WHERE context_id=? AND destination = ?`, contextID, destination))
	if errors.Is(err, sql.ErrNoRows) {
		return ExfilGrant{}, false, nil
	}
	if err != nil {
		return ExfilGrant{}, false, err
	}
	return item, true, nil
}

// SetExfilGrant upserts the authorization for one destination.
func (s *Store) SetExfilGrant(ctx context.Context, grant ExfilGrant) error {
	if grant.ContextID == "" || grant.Destination == "" || len(grant.Scopes) == 0 {
		return errors.New("外发授权需要目的地和至少一个数据范围")
	}
	for _, scope := range grant.Scopes {
		if !ValidScope(scope) {
			return fmt.Errorf("未知的数据范围 %q", scope)
		}
	}
	payload, err := json.Marshal(grant.Scopes)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO exfil_grants(context_id,destination,scopes,created_at) VALUES(?,?,?,?) ON CONFLICT(context_id,destination) DO UPDATE SET scopes=excluded.scopes`, grant.ContextID, grant.Destination, string(payload), now()); err != nil {
			return err
		}
		return signalContextChange(ctx, tx, grant.ContextID, "grant.changed")
	})
}

func (s *Store) DeleteExfilGrant(ctx context.Context, contextID, destination string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM exfil_grants WHERE context_id=? AND destination=?`, contextID, destination)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("该目的地没有已登记的外发授权")
		}
		return signalContextChange(ctx, tx, contextID, "grant.revoked")
	})
}

// SessionDataScopes reconstructs the accumulated data scope of a session's
// context from the persisted records: whether user input exists and which
// sensitivities successful tool results carried. The system prompt's skills
// summary is accounted for by the caller, which knows the profile.
func (s *Store) SessionDataScopes(ctx context.Context, sessionID string) (hasUserInput bool, toolScopes []string, err error) {
	var userCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id = ? AND role = 'user' `, sessionID).Scan(&userCount); err != nil {
		return false, nil, err
	}
	hasUserInput = userCount > 0
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT CASE WHEN c.source_scope='' THEN 'unknown' ELSE c.source_scope END FROM tool_calls c JOIN runs r ON r.id=c.run_id WHERE r.session_id=? UNION SELECT scope FROM session_context_scopes WHERE session_id=?`, sessionID, sessionID)
	if err != nil {
		return hasUserInput, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return hasUserInput, nil, err
		}
		toolScopes = append(toolScopes, scope)
	}
	return hasUserInput, toolScopes, rows.Err()
}

// ToolResultScopes lists the distinct data scopes the session's persisted
// tool results carried. Unlike SessionDataScopes it excludes the unknown
// scope that tool-call arguments accumulate: delegation uses it to pass the
// narrowest honest provenance of a subtask's returned result to the parent.
func (s *Store) ToolResultScopes(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT CASE WHEN c.source_scope='' THEN 'unknown' ELSE c.source_scope END FROM tool_calls c JOIN runs r ON r.id=c.run_id WHERE r.session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := []string{}
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

// MCPEndpoint is the fingerprint registry of one configured stdio MCP
// server: the binary hash, the tools/schema hash observed at registration
// and the server-reported version. A mismatch against a live connection
// pauses automatic execution based on the old registration.
type MCPEndpoint struct {
	Name         string `json:"name"`
	Command      string `json:"command"`
	BinarySHA256 string `json:"binarySha256"`
	SchemaSHA256 string `json:"schemaSha256"`
	Version      string `json:"version"`
	UpdatedAt    string `json:"updatedAt"`
}

func (s *Store) GetMCPEndpoint(ctx context.Context, name string) (MCPEndpoint, error) {
	var item MCPEndpoint
	err := s.db.QueryRowContext(ctx, `SELECT name, command, binary_sha256, schema_sha256, version, updated_at FROM mcp_endpoints WHERE name = ?`, name).
		Scan(&item.Name, &item.Command, &item.BinarySHA256, &item.SchemaSHA256, &item.Version, &item.UpdatedAt)
	return item, err
}

func (s *Store) SetMCPEndpoint(ctx context.Context, item MCPEndpoint) error {
	if item.Name == "" {
		return errors.New("MCP 服务名为空")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_endpoints (name, command, binary_sha256, schema_sha256, version, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET command = excluded.command, binary_sha256 = excluded.binary_sha256, schema_sha256 = excluded.schema_sha256, version = excluded.version, updated_at = excluded.updated_at`,
		item.Name, item.Command, item.BinarySHA256, item.SchemaSHA256, item.Version, now())
	return err
}

func (s *Store) ListMCPEndpoints(ctx context.Context) ([]MCPEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, command, binary_sha256, schema_sha256, version, updated_at FROM mcp_endpoints ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MCPEndpoint{}
	for rows.Next() {
		var item MCPEndpoint
		if err := rows.Scan(&item.Name, &item.Command, &item.BinarySHA256, &item.SchemaSHA256, &item.Version, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DeleteMCPEndpoint drops one server's registration, e.g. after the user
// removed the server; a later re-addition starts a fresh trust baseline.
func (s *Store) DeleteMCPEndpoint(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("MCP 服务名为空")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM mcp_endpoints WHERE name = ?`, name)
	return err
}

// AccumulateContextScopes preserves restrictions across config switches,
// summaries and subsequent Runs without tracking per-word provenance.
func (s *Store) AccumulateContextScopes(ctx context.Context, sessionID string, scopes []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, scope := range scopes {
			if !ValidScope(scope) {
				return fmt.Errorf("无效来源范围 %q", scope)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_context_scopes(session_id,scope) VALUES(?,?)`, sessionID, scope); err != nil {
				return err
			}
		}
		return nil
	})
}

// PermissionScope is the machine-local resource authorization of one Context.
type PermissionScope struct {
	Unrestricted   bool     `json:"unrestricted"`
	WorkspacePaths []string `json:"workspacePaths"`
	Commands       []string `json:"commands"`
	NetworkHosts   []string `json:"networkHosts"`
}

// DelegationPolicy is the execution-config switch for worker delegation; the
// worker candidates and execution limits are captured when a root run starts.
// Workers are selected from configured slots. Zero concurrency/time use host
// defaults.
type DelegationPolicy struct {
	Enabled        bool `json:"enabled"`
	MaxDepth       int  `json:"maxDepth"`
	MaxConcurrency int  `json:"maxConcurrency"`
	MaxSeconds     int  `json:"maxSeconds"`
}

const (
	// DefaultDelegationConcurrency caps concurrent subtask runs per parent.
	DefaultDelegationConcurrency = 2
	// maxDelegationSeconds mirrors the engine's per-run timeout (10
	// minutes): a subtask can never outlive the run limit, so the config
	// budget only tightens it.
	maxDelegationSeconds = 600
	// MaxDelegationDepth bounds recursive delegation.
	MaxDelegationDepth = 4
)

// ContextPolicy is local authorization, independent of the execution config.
type ContextPolicy struct {
	ApprovalMode string          `json:"approvalMode"`
	Permissions  PermissionScope `json:"permissions"`
}

func validatePolicy(p ContextPolicy) error {
	if p.ApprovalMode != "manual" && p.ApprovalMode != "semi" && p.ApprovalMode != "automatic" {
		return errors.New("无效的审批模式")
	}

	return nil
}
