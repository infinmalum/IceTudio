package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"icetudio/internal/config"
	"icetudio/internal/skills"
)

// DefaultConfigID identifies the built-in application execution config.
const DefaultConfigID = "default"

// RuntimeConfigData is the application execution combination: instructions,
// model binding, capability selection and budgets. It never holds instance
// data, history, secrets or authorization.
type RuntimeConfigData struct {
	SchemaVersion     int          `json:"schemaVersion"`
	Execution         Execution    `json:"execution"`
	Persona           string       `json:"persona"`
	Description       string       `json:"description"`
	SystemPrompt      string       `json:"systemPrompt"`
	Model             config.Model `json:"model"`
	AllMCPServers     bool         `json:"allMcpServers"`
	EnabledMCPServers []string     `json:"enabledMcpServers"`
	AllowedTools      []string     `json:"allowedTools"`
	// RestrictTools makes an empty allowlist an explicit denial of all tools.
	// It is also set on frozen projections whose selected intersection is empty.
	RestrictTools bool             `json:"restrictTools,omitempty"`
	AllSkills     bool             `json:"allSkills"`
	EnabledSkills []string         `json:"enabledSkills"`
	Delegation    DelegationPolicy `json:"delegation"`
	ContextBudget int              `json:"contextBudget"`
	// PlanEnabled is the config's preference for the optional plan
	// capability: the update_plan tool and the plan prompt fragment.
	PlanEnabled bool `json:"planEnabled"`
}

// RuntimeConfig is the named, versioned application execution config. Editing
// bumps the revision and affects every referencing session's subsequent runs.
type RuntimeConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Data      RuntimeConfigData `json:"data"`
	Revision  int               `json:"revision"`
	IsDefault bool              `json:"isDefault"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}

// RunConfigSnapshot is the resolved combination one Run executes with: the
// Context's identity and machine policy plus the frozen config revision.
type RunConfigSnapshot struct {
	Resources       *RunResources     `json:"-"`
	ConfigScopes    []string          `json:"configScopes"`
	ResourcesFrozen bool              `json:"-"`
	FrozenSkills    []skills.Snapshot `json:"-"`
	ContextID       string            `json:"contextId"`
	ContextName     string            `json:"contextName"`
	ConfigID        string            `json:"configId"`
	ConfigName      string            `json:"configName"`
	RuntimeConfigData
	Policy         ContextPolicy `json:"policy"`
	ConfigRevision int           `json:"configRevision"`
}

// BlankRuntimeConfig is the built-in blank combination: nothing enabled,
// everything explicit, manual approvals.
func BlankRuntimeConfig() RuntimeConfigData {
	return RuntimeConfigData{SchemaVersion: RuntimeConfigSchemaVersion, Execution: Execution{Workflow: "direct"}, AllowedTools: []string{}, EnabledSkills: []string{}, ContextBudget: 8192}
}

// ValidateRuntimeConfigData checks the stored combination shape.
func ValidateRuntimeConfigData(d RuntimeConfigData) error {
	return validateRuntimeConfigData(d)
}

func validateRuntimeConfigData(d RuntimeConfigData) error {
	if err := validateExecution(d); err != nil {
		return err
	}
	if len(d.Persona) > 32000 || len(d.SystemPrompt) > 32000 || len(d.Description) > 500 {
		return errors.New("系统提示词或说明过长")
	}
	if d.Model.Provider != "" {
		if err := d.Model.Validate(); err != nil {
			return err
		}
	}

	if d.Delegation.MaxDepth < 0 || d.Delegation.MaxDepth > MaxDelegationDepth {
		return errors.New("上下文预算或委派深度无效")
	}
	if d.Delegation.MaxConcurrency < 0 || d.Delegation.MaxConcurrency > 8 {
		return errors.New("委派并发上限无效")
	}
	if d.Delegation.MaxSeconds < 0 || (d.Delegation.MaxSeconds > 0 && d.Delegation.MaxSeconds < 30) || d.Delegation.MaxSeconds > maxDelegationSeconds {
		return errors.New("委派时间预算无效（30–600 秒）")
	}
	if d.ContextBudget < 1024 || d.ContextBudget > 1000000 {
		return errors.New("上下文预算或委派深度无效")
	}
	for _, name := range d.AllowedTools {
		if !strings.Contains(name, "__") {
			return fmt.Errorf("无效的工具名 %q", name)
		}
	}
	for _, name := range d.EnabledMCPServers {
		if name == "" || strings.Contains(name, "__") {
			return fmt.Errorf("无效的 MCP 服务名 %q", name)
		}
	}
	return nil
}

func (d RuntimeConfigData) AllowsTool(name string) bool {
	parts := strings.SplitN(name, "__", 2)
	if len(parts) != 2 || parts[1] == "" {
		return false
	}
	// The "project" family is the native music-editing capability, not an
	// MCP server: MCP enablement never hides it. The explicit AllowedTools
	// list can still restrict it.
	if !d.AllMCPServers && parts[0] != "project" {
		found := false
		for _, server := range d.EnabledMCPServers {
			if server == parts[0] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(d.AllowedTools) == 0 && !d.RestrictTools {
		return true
	}
	for _, tool := range d.AllowedTools {
		if tool == name {
			return true
		}
	}
	return false
}

func (d RuntimeConfigData) EnablesSkill(name string) bool {
	if d.AllSkills {
		return true
	}
	for _, skill := range d.EnabledSkills {
		if skill == name {
			return true
		}
	}
	return false
}

func scanRuntimeConfig(scanner interface{ Scan(...any) error }) (RuntimeConfig, error) {
	var c RuntimeConfig
	var configJSON string
	if err := scanner.Scan(&c.ID, &c.Name, &configJSON, &c.Revision, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return RuntimeConfig{}, err
	}
	if err := json.Unmarshal([]byte(configJSON), &c.Data); err != nil {
		return RuntimeConfig{}, fmt.Errorf("解析执行配置: %w", err)
	}
	return c, nil
}

func (s *Store) ListRuntimeConfigs(ctx context.Context) ([]RuntimeConfig, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, config, revision, is_default, created_at, updated_at FROM runtime_configs WHERE archived_at IS NULL ORDER BY CASE id WHEN 'default' THEN 0 ELSE 1 END, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RuntimeConfig{}
	for rows.Next() {
		c, err := scanRuntimeConfig(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *Store) GetRuntimeConfig(ctx context.Context, id string) (RuntimeConfig, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, config, revision, is_default, created_at, updated_at FROM runtime_configs WHERE id = ?`, id)
	c, err := scanRuntimeConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeConfig{}, errors.New("执行配置不存在")
	}
	return c, err
}

// CreateRuntimeConfig persists a new execution config. Only the host-owned
// tooling creates configs; the product exposes one auto-created default.
func (s *Store) CreateRuntimeConfig(ctx context.Context, name string, data RuntimeConfigData) (RuntimeConfig, error) {
	data = normalizeRuntimeConfig(data)
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return RuntimeConfig{}, errors.New("执行配置名称无效")
	}
	if err := validateRuntimeConfigData(data); err != nil {
		return RuntimeConfig{}, err
	}
	id, err := newID()
	if err != nil {
		return RuntimeConfig{}, err
	}
	created := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_configs(id,name,revision,is_default,created_at,updated_at) VALUES(?,?,1,0,?,?)`, id, name, created, created); err != nil {
			return err
		}
		if err := saveRuntimeConfigData(ctx, tx, id, data); err != nil {
			return err
		}
		return saveConfigRevision(ctx, tx, id, 1, name, data)
	})
	if err != nil {
		return RuntimeConfig{}, err
	}
	return s.GetRuntimeConfig(ctx, id)
}

// UpdateRuntimeConfig publishes component changes in one revision-checked
// transaction. Unchanged components are not rewritten.
func (s *Store) UpdateRuntimeConfig(ctx context.Context, c RuntimeConfig) (RuntimeConfig, error) {
	c.Data = normalizeRuntimeConfig(c.Data)
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 {
		return RuntimeConfig{}, errors.New("执行配置名称无效")
	}
	if c.ID == "" || c.Revision < 1 {
		return RuntimeConfig{}, errors.New("执行配置 ID 或版本无效")
	}
	old, err := s.GetRuntimeConfig(ctx, c.ID)
	if err != nil {
		return RuntimeConfig{}, err
	}
	if old.Revision != c.Revision {
		return RuntimeConfig{}, errors.New("执行配置已被其他操作修改，请刷新后重试")
	}
	if err := validateRuntimeConfigData(c.Data); err != nil {
		return RuntimeConfig{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE runtime_configs SET name=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND archived_at IS NULL`, strings.TrimSpace(c.Name), now(), c.ID, c.Revision)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("执行配置已被其他操作修改，请刷新后重试")
		}
		if err := saveRuntimeConfigData(ctx, tx, c.ID, c.Data); err != nil {
			return err
		}
		if err := saveConfigRevision(ctx, tx, c.ID, c.Revision+1, strings.TrimSpace(c.Name), c.Data); err != nil {
			return err
		}
		return copyConfigScopesInTx(ctx, tx, c.ID, c.Revision, c.ID, c.Revision+1)
	})
	if err != nil {
		return RuntimeConfig{}, err
	}
	return s.GetRuntimeConfig(ctx, c.ID)
}

// RestoreDefaultRuntimeConfigData resets the built-in config's combination to
// the shipped content.
func (s *Store) RestoreDefaultRuntimeConfigData(ctx context.Context, revision int) (RuntimeConfig, error) {
	old, err := s.GetRuntimeConfig(ctx, DefaultConfigID)
	if err != nil {
		return RuntimeConfig{}, errors.New("此配置没有内置预设")
	}
	if old.Revision != revision {
		return RuntimeConfig{}, errors.New("执行配置已被其他操作修改，请刷新后重试")
	}
	restored := old
	restored.Data = DefaultRuntimeConfig()
	return s.UpdateRuntimeConfig(ctx, restored)
}

func (s *Store) ResolveRunConfig(ctx context.Context, sessionID string) (RunConfigSnapshot, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return RunConfigSnapshot{}, err
	}
	conversation, err := s.GetConversation(ctx, sessionID)
	if err != nil {
		return RunConfigSnapshot{}, err
	}
	cfg, err := s.GetRuntimeConfig(ctx, conversation.ConfigID)
	if err != nil {
		return RunConfigSnapshot{}, err
	}
	a, err := s.GetLocalContext(ctx, session.ContextID)
	if err != nil {
		return RunConfigSnapshot{}, err
	}
	return RunConfigSnapshot{ContextID: a.ID, ContextName: a.Name, ConfigID: cfg.ID, ConfigName: cfg.Name, RuntimeConfigData: cfg.Data, Policy: a.Policy, ConfigRevision: cfg.Revision}, nil
}
