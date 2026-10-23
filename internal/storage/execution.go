package storage

import (
	"errors"
	"fmt"
	"regexp"

	"icetudio/internal/config"
)

const RuntimeConfigSchemaVersion = 1

// Execution is one independently persisted configuration group. Fixed stages
// and dynamic plans share host scheduling, claims, budgets and review evidence.
type Execution struct {
	Workflow        string          `json:"workflow"`
	Stages          []TaskSpec      `json:"stages,omitempty"`
	Main            ComponentConfig `json:"main"`
	Planner         ComponentConfig `json:"planner"`
	Reviewer        ComponentConfig `json:"reviewer"`
	Improver        ComponentConfig `json:"improver"`
	ImproverEnabled bool            `json:"improverEnabled"`
	Review          ReviewPolicy    `json:"review"`
	Budget          RootBudget      `json:"budget"`
	Worker          ComponentConfig `json:"worker"`
	WorkerSources   []WorkerSource  `json:"workerSources"`
}

// Empty Configuration selects this config's own worker component. Worker
// slots carry explicit portable configuration; there are no cross-config
// references.
type WorkerSource struct {
	Name          string               `json:"name"`
	Configuration *WorkerConfiguration `json:"configuration,omitempty"`
}

// Portable one-level worker configuration, excluding workflow, identity,
// history, authorization and recursive config dependencies.
type WorkerConfiguration struct {
	Name              string       `json:"name"`
	Persona           string       `json:"persona"`
	SystemPrompt      string       `json:"systemPrompt"`
	Model             config.Model `json:"model"`
	AllMCPServers     bool         `json:"allMcpServers"`
	EnabledMCPServers []string     `json:"enabledMcpServers"`
	AllowedTools      []string     `json:"allowedTools"`
	AllSkills         bool         `json:"allSkills"`
	EnabledSkills     []string     `json:"enabledSkills"`
	ContextBudget     int          `json:"contextBudget"`
}

func WorkerConfigurationFrom(c RuntimeConfig) (WorkerConfiguration, error) {
	d, err := c.Data.ResolveComponent("worker")
	if err != nil {
		return WorkerConfiguration{}, err
	}
	return WorkerConfiguration{Name: c.Name, Persona: d.Persona, SystemPrompt: d.SystemPrompt, Model: d.Model, AllMCPServers: d.AllMCPServers, EnabledMCPServers: d.EnabledMCPServers, AllowedTools: d.AllowedTools, AllSkills: d.AllSkills, EnabledSkills: d.EnabledSkills, ContextBudget: d.ContextBudget}, nil
}

func (c WorkerConfiguration) ConfigData() RuntimeConfigData {
	d := BlankRuntimeConfig()
	d.Persona = c.Persona
	d.SystemPrompt = c.SystemPrompt
	d.Model = c.Model
	d.AllMCPServers = c.AllMCPServers
	d.EnabledMCPServers = c.EnabledMCPServers
	d.AllowedTools = c.AllowedTools
	d.AllSkills = c.AllSkills
	d.EnabledSkills = c.EnabledSkills
	d.ContextBudget = c.ContextBudget
	return d
}

var workerName = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_-]{0,31}$`)

type ComponentConfig struct {
	SystemPrompt  string        `json:"systemPrompt"`
	Model         *config.Model `json:"model,omitempty"`
	RestrictTools bool          `json:"restrictTools"`
	AllowedTools  []string      `json:"allowedTools"`
}

type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Available   bool   `json:"available"`
	Description string `json:"description"`
}

// Components are program-owned definitions, independent of stored configs.
func Components() []Component {
	return []Component{
		{"main", "主执行", 2, true, "直接处理用户任务，使用共享模型和工具循环"},
		{"worker", "子任务执行", 2, true, "按固定配置执行子任务，支持受控并行"},
		{"planner", "规划", 1, true, "提取目标、生成依赖计划并按关键事件重规划"},
		{"reviewer", "审查", 1, true, "独立上下文审查交付与已有工具证据，不执行工具"},
		{"improver", "配置改进", 2, true, "按需求及明确选择的任务证据生成执行配置/Skill 草案，仅手动采用，不执行工具或自动发布"},
	}
}

func normalizeRuntimeConfig(data RuntimeConfigData) RuntimeConfigData {
	if data.SchemaVersion == 0 {
		data.SchemaVersion = RuntimeConfigSchemaVersion
	}
	if data.Execution.Workflow == "" {
		data.Execution.Workflow = "direct"
	}
	return data
}

func validateExecution(data RuntimeConfigData) error {
	if len(data.Execution.WorkerSources) > 8 {
		return errors.New("最多配置 8 个 worker 来源")
	}
	seen := map[string]bool{}
	for _, source := range data.Execution.WorkerSources {
		if source.Configuration != nil {
			if len(source.Configuration.Name) > 100 {
				return errors.New("worker 配置名称过长")
			}
			if err := validateRuntimeConfigData(source.Configuration.ConfigData()); err != nil {
				return err
			}
		}
		if !workerName.MatchString(source.Name) || seen[source.Name] {
			return errors.New("worker 名称需唯一，使用文字、数字、下划线或连字符，最多 32 字符")
		}
		seen[source.Name] = true
	}
	if data.SchemaVersion != 0 && data.SchemaVersion != RuntimeConfigSchemaVersion {
		return errors.New("不支持的执行配置 schemaVersion")
	}
	if data.Execution.Workflow != "" && data.Execution.Workflow != "direct" && data.Execution.Workflow != "dynamic" && data.Execution.Workflow != "fixed" {
		return errors.New("流程必须为 direct、dynamic 或 fixed")
	}
	if err := validateFixedWorkflow(data); err != nil {
		return err
	}
	if err := data.Execution.Review.Validate(); err != nil {
		return err
	}
	if err := data.Execution.Budget.Validate(); err != nil {
		return err
	}
	if data.Execution.Workflow == "dynamic" && (!data.PlanEnabled || !data.Delegation.Enabled || data.Delegation.MaxDepth < 1 || len(data.Execution.WorkerSources) == 0) {
		return errors.New("动态流程需要规划、委派、至少一级深度与 worker 槽位")
	}
	if data.Execution.Workflow == "dynamic" {
		for _, tool := range []string{"local__delegate_task", "local__task_plan"} {
			allowed := data.AllowsTool(tool)
			if data.Execution.Main.RestrictTools {
				found := false
				for _, name := range data.Execution.Main.AllowedTools {
					if name == tool {
						found = true
					}
				}
				allowed = allowed && found
			}
			if !allowed {
				return errors.New("动态流程需要规划与委派能力；当前公共或 main 工具限制禁用了它们")
			}
		}
	}
	for _, c := range []ComponentConfig{data.Execution.Main, data.Execution.Worker, data.Execution.Planner, data.Execution.Reviewer, data.Execution.Improver} {
		if len(c.SystemPrompt) > 32000 {
			return errors.New("组件提示词过长")
		}
		if c.Model != nil {
			if c.Model.Provider == "" {
				return errors.New("组件模型需明确提供方")
			}
			if err := c.Model.Validate(); err != nil {
				return err
			}
		}
		if len(c.AllowedTools) > 200 {
			return errors.New("组件工具列表过长")
		}
		for _, tool := range c.AllowedTools {
			if !data.AllowsTool(tool) {
				return fmt.Errorf("组件不能增加执行配置未选择的工具 %q", tool)
			}
		}
	}
	return nil
}

// ResolveComponent makes a single-level immutable execution projection.
// An explicit empty component tool subset means no tools; the common allowlist
// only narrows the selected catalogs when it is nonempty.
func (data RuntimeConfigData) ResolveComponent(role string) (RuntimeConfigData, error) {
	if err := validateExecution(data); err != nil {
		return RuntimeConfigData{}, err
	}
	var c ComponentConfig
	switch role {
	case "main":
		c = data.Execution.Main
	case "worker":
		c = data.Execution.Worker
	case "planner":
		c = data.Execution.Planner
	case "reviewer":
		c = data.Execution.Reviewer
	case "improver":
		c = data.Execution.Improver
	default:
		return RuntimeConfigData{}, fmt.Errorf("组件 %q 尚不可执行", role)
	}
	out := data
	if c.SystemPrompt != "" {
		out.SystemPrompt = c.SystemPrompt
	}
	if out.SystemPrompt == "" {
		if role == "worker" {
			out.SystemPrompt = "完成明确交给你的子任务，使用获准工具；遇到范围之外的问题时报告阻塞，不自行扩大任务。"
		} else {
			out.SystemPrompt = "你是 IceTudio 助手。理解用户任务，使用获准工具完成工作；无法完成或缺少信息时如实说明。"
		}
	}
	if role == "reviewer" && c.SystemPrompt == "" {
		out.SystemPrompt = "独立评估交付是否满足用户目标，指出具体缺陷与无法确认的事项。"
	}
	if c.Model != nil {
		out.Model = *c.Model
	}
	if role == "improver" && c.SystemPrompt == "" {
		out.SystemPrompt = "根据用户需求提出可检查的执行配置或 Skill 草案，不执行任务或修改权限。"
	}
	if role == "planner" || role == "reviewer" || role == "improver" {
		out.PlanEnabled = false
		out.Delegation = DelegationPolicy{}
		out.AllSkills = false
		out.EnabledSkills = nil
		out.AllMCPServers = false
		out.EnabledMCPServers = nil
		out.AllowedTools = nil
	}
	if c.RestrictTools && role != "planner" && role != "reviewer" && role != "improver" {
		out.AllowedTools = append([]string(nil), c.AllowedTools...)
		if len(c.AllowedTools) == 0 {
			out.AllMCPServers = false
			out.EnabledMCPServers = nil
		}
	}
	return out, nil
}

func componentVersion(role string) int {
	for _, c := range Components() {
		if c.ID == role {
			return c.Version
		}
	}
	return 1
}
