package desktop

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Native typed settings editor. No editable JSON or generic service dispatch:
// callers provide concrete application DTOs and explicit save callbacks.
type nativeForm struct {
	onChange func(string)
	value    any
	raw      map[string]string
	errors   map[string]string
	dirty    bool
	version  uint64
}

func newNativeForm(value any) *nativeForm {
	return &nativeForm{value: value, raw: map[string]string{}, errors: map[string]string{}}
}

var fieldNames = map[string]string{
	"Name": "名称", "Provider": "模型提供方", "BaseURL": "端点地址", "SystemPrompt": "系统指令", "Persona": "创作角色", "Description": "说明", "Model": "模型", "Execution": "执行组件", "Workflow": "执行方式", "Stages": "固定流程步骤", "Main": "主执行", "Planner": "规划", "Reviewer": "审查", "Improver": "方法改进", "Worker": "协作执行", "ImproverEnabled": "启用方法改进", "Review": "审查策略", "Budget": "任务预算", "WorkerSources": "协作配置", "Configuration": "独立协作配置", "AllMCPServers": "启用全部已安装 MCP", "EnabledMCPServers": "启用的 MCP 名称（每行一个）", "AllowedTools": "工具允许清单（每行一个）", "RestrictTools": "严格限制到工具允许清单", "AllSkills": "启用全部已安装方法", "EnabledSkills": "启用的方法名称（每行一个）", "Delegation": "协作调度", "Enabled": "启用", "MaxDepth": "最大嵌套深度", "MaxConcurrency": "最大并行数", "MaxSeconds": "最长秒数", "ContextBudget": "上下文预算", "PlanEnabled": "启用任务计划", "ApprovalMode": "审批模式", "Permissions": "本机资源权限", "Unrestricted": "一般资源不限范围（敏感与外发仍单独检查）", "WorkspacePaths": "工作区路径（每行一个）", "Commands": "可执行文件（每行一个）", "NetworkHosts": "网络目标（每行一个）", "Command": "启动程序", "Args": "启动参数（每行一个）", "WorkingDir": "工作目录", "Env": "可继承的环境变量名（不填写值，每行一个）", "TimeoutSeconds": "连接超时秒数", "WebSearchTools": "搜索工具名称（每行一个）", "Content": "方法正文 Markdown", "Summary": "说明", "Outcome": "结论类型", "EvidenceRefs": "证据引用（每行一个）", "Reasons": "判断依据（每行一个）", "Limitations": "适用限制（每行一个）", "ExpectedChecks": "验证标准（每行一个）", "Task": "工作要求", "Acceptance": "验收标准", "AccessMode": "访问模式", "DependsOn": "依赖步骤（每行一个）", "Component": "组件", "MaxSteps": "最多步骤", "MaxToolCalls": "最多工具调用", "MaxReworks": "最多返工次数", "Required": "必须执行", "Strategy": "策略", "Key": "步骤标识", "ID": "标识", "Revision": "修订", "Instructions": "指令", "Target": "目标", "MaxTokens": "最多 token", "MaxWorkers": "最多协作数", "MaxRuns": "最多运行数", "MaxTotalTokens": "总 token 上限", "MaxElapsedSeconds": "总秒数上限", "MaxAttempts": "最多尝试次数", "Output": "输出要求", "Goal": "目标",
}

func nativeLabel(name string) string {
	if s, ok := fieldNames[name]; ok {
		return s
	}
	return name
}
func enumChoices(name string) []string {
	switch name {
	case "Provider":
		return []string{"", "ollama", "lmstudio", "openai", "anthropic", "gemini"}
	case "ApprovalMode":
		return []string{"manual", "semi", "automatic"}
	case "Workflow":
		return []string{"direct", "dynamic", "fixed"}
	case "AccessMode":
		return []string{"read", "write"}
	case "Outcome":
		return []string{"reusable", "one_off"}
	}
	return nil
}
func nativeSelect(c *ui.Context, label string, value *string, choices []string, disabled bool, change func()) {
	ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
	p := ui.SelectBase(c, value)
	p.Trigger.FillWidth().Padding(6).Border(1, c.Theme().Border).Label(label).Disabled(disabled).Children(func() { ui.Text(c, *value+" ▾").SingleLine() })
	p.Popup(func(panel ui.Element) {
		panel.Padding(4).Background(c.Theme().Surface)
		ui.Scroll(c).MaxHeight(260).Width(420).Children(func() {
			for _, s := range choices {
				p.Item(s).Padding(6).Children(func() { ui.Text(c, s).SingleLine() })
			}
		})
	})
	if p.Trigger.Changed() && change != nil {
		change()
	}
}
func (f *nativeForm) draw(c *ui.Context, disabled bool) {
	f.fields(c, "", reflect.ValueOf(f.value).Elem(), disabled)
}
func (f *nativeForm) fields(c *ui.Context, path string, v reflect.Value, disabled bool) {
	if v.Kind() == reflect.Struct {
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := typ.Field(i)
			if !v.Field(i).CanSet() || field.Tag.Get("json") == "-" {
				continue
			}
			name := field.Name
			if name == "SchemaVersion" || name == "CreatedAt" || name == "UpdatedAt" || name == "IsDefault" {
				continue
			}
			key := path + "/" + name
			ui.Column(c.Key(key)).FillWidth().Gap(4).Children(func() {
				val := v.Field(i)
				label := nativeLabel(name)
				if name == "ID" && path == "" || name == "Revision" {
					ui.Text(c, label+" · "+fmt.Sprint(val.Interface())).FontSize(11)
					return
				}
				if choices := enumChoices(name); val.Kind() == reflect.String && len(choices) > 0 {
					s := val.String()
					nativeSelect(c, label, &s, choices, disabled, func() {
						val.SetString(s)
						f.dirty = true
						f.version++
						if f.onChange != nil {
							f.onChange(key)
						}
					})
					return
				}
				switch val.Kind() {
				case reflect.Struct:
					ui.Text(c, label).Bold()
					f.fields(c, key, val, disabled)
				case reflect.Pointer:
					on := !val.IsNil()
					ui.Checkbox(c, &on, label+" · 独立覆盖").Disabled(disabled).OnChange(func() {
						if on {
							val.Set(reflect.New(val.Type().Elem()))
						} else {
							val.SetZero()
						}
						f.clearRaw(key)
						f.dirty = true
						f.version++
					})
					if !val.IsNil() {
						f.fields(c, key, val.Elem(), disabled)
					}
				case reflect.Bool:
					b := val.Bool()
					ui.Checkbox(c, &b, label).Disabled(disabled).OnChange(func() { val.SetBool(b); f.dirty = true; f.version++ })
				case reflect.String:
					s := val.String()
					ui.Text(c, label).FontSize(11)
					long := strings.Contains(name, "Prompt") || name == "Persona" || name == "Content" || name == "Task" || name == "Acceptance" || name == "Summary" || len(s) > 120
					input := ui.TextInput(c, &s).FillWidth()
					if long {
						input = ui.TextArea(c, &s).FillWidth().Height(100)
					}
					input.Label(label).Disabled(disabled).OnChange(func() {
						val.SetString(s)
						f.dirty = true
						f.version++
						if f.onChange != nil {
							f.onChange(key)
						}
					})
				case reflect.Int, reflect.Int64:
					s, ok := f.raw[key]
					if !ok {
						s = strconv.FormatInt(val.Int(), 10)
					}
					ui.Text(c, label).FontSize(11)
					ui.TextInput(c, &s).FillWidth().Label(label).Disabled(disabled).OnChange(func() {
						f.raw[key] = s
						f.dirty = true
						f.version++
						n, err := strconv.ParseInt(s, 10, val.Type().Bits())
						if err != nil {
							f.errors[key] = label + "需要整数"
						} else {
							delete(f.errors, key)
							val.SetInt(n)
						}
					})
					if msg := f.errors[key]; msg != "" {
						ui.Text(c, msg).TextColor(c.Theme().Warning)
					}
				case reflect.Slice:
					ui.Text(c, label).Bold()
					if val.Type().Elem().Kind() == reflect.String {
						ss := make([]string, val.Len())
						for j := range ss {
							ss[j] = val.Index(j).String()
						}
						s, exists := f.raw[key]
						if !exists {
							s = strings.Join(ss, "\n")
						}
						ui.TextArea(c, &s).FillWidth().Height(72).Label(label).Disabled(disabled).OnChange(func() {
							f.raw[key] = s
							items := []string{}
							for _, x := range strings.Split(s, "\n") {
								if x = strings.TrimSpace(x); x != "" {
									items = append(items, x)
								}
							}
							val.Set(reflect.ValueOf(items).Convert(val.Type()))
							f.dirty = true
							f.version++
						})
					} else {
						for j := 0; j < val.Len(); j++ {
							ui.Column(c.Key(fmt.Sprintf("%s/%d", key, j))).FillWidth().Padding(8).Border(1, c.Theme().Border).Gap(4).Children(func() {
								f.fields(c, fmt.Sprintf("%s/%d", key, j), val.Index(j), disabled)
								ui.Button(c, "移除这项"+label).Disabled(disabled).OnClick(func() {
									val.Set(reflect.AppendSlice(val.Slice(0, j), val.Slice(j+1, val.Len())))
									f.clearRaw(key)
									f.dirty = true
									f.version++
								})
							})
						}
						ui.Button(c, "添加"+label).Disabled(disabled).OnClick(func() { val.Set(reflect.Append(val, reflect.Zero(val.Type().Elem()))); f.dirty = true; f.version++ })
					}
				default:
					ui.Text(c, "此字段为只读："+fmt.Sprint(val.Interface())).MaxWidth(650)
				}
			})
		}
	}
}
func (f *nativeForm) clearRaw(prefix string) {
	for k := range f.raw {
		if strings.HasPrefix(k, prefix) {
			delete(f.raw, k)
			delete(f.errors, k)
		}
	}
}
