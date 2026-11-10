package desktop

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/storage"
)

type nativeSettings struct {
	open, busy         bool
	page, err, message string
	model              config.Model
	form               *nativeForm
	apiKey             string
	credentials        []app.CredentialStatus
	configs            []storage.RuntimeConfig
	selectedConfig     string
	endpoints          []app.MCPEndpointStatus
	servers            []config.MCPServer
	skills             []app.SkillInfo
	logs               []string
	context            *storage.LocalContext
	contextBase        *storage.LocalContext
	contextID          string
	grants             []storage.ExfilGrant
	destination        string
	scopes             []string
	checks             map[string]bool
}

func (a *Workspace) openSettings(page string) {
	if a.settings != nil && (a.settings.busy || a.settings.form != nil && a.settings.form.dirty) {
		a.settings.open = true
		return
	}
	d := &nativeSettings{open: true, page: page, model: a.service.ModelConfig(), checks: map[string]bool{}}
	a.settings = d
	a.reloadSettings(d)
}
func (a *Workspace) settingsAction(d *nativeSettings, run func() error, done func()) {
	if d.busy || a.closeFrozen {
		return
	}
	d.busy = true
	go func() {
		err := run()
		a.update(func() {
			d.busy = false
			if err != nil {
				d.err = err.Error()
				return
			}
			d.err = ""
			if done != nil {
				done()
			}
		})
	}()
}
func (a *Workspace) reloadSettings(d *nativeSettings) {
	if d.busy {
		return
	}
	session := a.agent.selected
	var conv storage.Conversation
	var context storage.LocalContext
	var grants []storage.ExfilGrant
	var servers []config.MCPServer
	var endpoints []app.MCPEndpointStatus
	var skills []app.SkillInfo
	var configs []storage.RuntimeConfig
	var credentials []app.CredentialStatus
	var logs []string
	a.settingsAction(d, func() error {
		var err error
		switch d.page {
		case "模型":
			credentials, err = a.service.ListCredentialStatus()
		case "执行配置":
			configs, err = a.service.ListRuntimeConfigs()
		case "扩展服务":
			servers = a.service.ListConfiguredMCPServers()
			endpoints, err = a.service.ListMCPEndpoints()
		case "工作方法":
			skills, err = a.service.ListSkills()
		case "权限":
			if session == "" {
				return errors.New("先选择对话，再设置本机权限与外发授权")
			}
			conv, err = a.service.GetConversation(session)
			if err == nil {
				context, err = a.service.GetLocalContext(conv.ContextID)
			}
			if err == nil {
				grants, err = a.service.ListExfilGrants(conv.ContextID)
			}
		case "运行日志":
			logs, err = a.service.ReadHostLogs(200)
		}
		return err
	}, func() {
		d.credentials = credentials
		d.configs = configs
		d.servers = servers
		d.endpoints = endpoints
		d.skills = skills
		d.logs = logs
		if d.page == "模型" {
			d.form = newNativeForm(&d.model)
			d.form.onChange = func(path string) {
				if path == "/Provider" {
					d.apiKey = ""
					d.model.BaseURL = config.DefaultModel(d.model.Provider).BaseURL
					d.model.Name = ""
				}
			}
		}
		if d.page == "权限" {
			d.context = &context
			base := context
			d.contextBase = &base
			d.contextID = conv.ContextID
			d.grants = grants
			d.form = newNativeForm(&d.context.Policy)
		}
	})
}
func (a *Workspace) settingsDialog(c *ui.Context) {
	d := a.settings
	if d == nil || !d.open || a.closeFrozen {
		return
	}
	windowWidth, windowHeight := c.Size()
	width := min(float32(790), windowWidth-64)
	pages := []string{"外观", "模型", "权限", "扩展服务", "工作方法", "执行配置", "运行日志"}
	disabled := d.busy || d.form != nil && d.form.dirty
	selectPage := func(page string) {
		d.page, d.form, d.err = page, nil, ""
		a.reloadSettings(d)
	}
	ui.Modal(c, &d.open, func() {
		ui.Column(c).Width(width).Gap(8).Children(func() {
			ui.Text(c, "设置").FontSize(18).Bold()
			if width < 650 {
				ui.PopoverMenuButton(c, d.page, func(m *ui.Menu) {
					for _, page := range pages {
						if m.Item(page).Radio(page == d.page).Disabled(disabled).Chosen() {
							selectPage(page)
						}
					}
				}).Label("设置页面")
			}
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Start).Children(func() {
				if width >= 650 {
					ui.Column(c).Width(128).Gap(6).Children(func() {
						for _, page := range pages {
							ui.Button(c, page).FillWidth().Disabled(disabled).OnClick(func() { selectPage(page) })
						}
					})
				}
				ui.Scroll(c).Grow(1).MinWidth(0).MaxHeight(min(540, windowHeight-190)).Children(func() {
					ui.Column(c).FillWidth().Gap(10).Children(func() {
						switch d.page {
						case "外观":
							a.appearanceSettings(c, d)
						case "模型":
							a.modelSettings(c, d)
						case "权限":
							a.permissionSettings(c, d)
						case "扩展服务":
							a.mcpSettings(c, d)
						case "工作方法":
							a.skillSettings(c, d)
						case "执行配置":
							a.runtimeSettings(c, d)
						case "运行日志":
							for _, line := range d.logs {
								ui.Text(c, line).FontSize(11)
							}
						}
						if d.err != "" {
							ui.Text(c, d.err).TextColor(c.Theme().Warning)
						}
						if d.message != "" {
							ui.Text(c, d.message)
						}
						if d.form != nil && d.form.dirty {
							ui.Text(c, "有未保存输入。关闭此窗口会在本次运行中保留；退出应用前请保存或明确放弃。")
						}
					})
				})
			})
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				ui.Button(c, "重新读取 / 放弃本页修改").Disabled(d.busy).OnClick(func() { d.form = nil; a.reloadSettings(d) })
				ui.Button(c, "返回工作区").Disabled(d.busy).OnClick(func() { d.open = false })
			})
		})
	})
}
func (a *Workspace) modelSettings(c *ui.Context, d *nativeSettings) {
	ui.Text(c, "模型与凭据").Bold()
	ui.Text(c, "选择实际提供方和端点。保存并检查连接会访问该地址；密钥只写入本机保护存储。").MaxWidth(630)
	if d.form != nil {
		d.form.draw(c, d.busy)
	}
	if config.IsRemoteProvider(d.model.Provider) {
		ui.TextInput(c, &d.apiKey).FillWidth().Password().Label("API 密钥（留空保留已保存密钥）").Disabled(d.busy)
	}
	ui.Button(c, "保存并检查连接").Disabled(d.busy || d.form == nil || len(d.form.errors) > 0).OnClick(func() {
		model, key := d.model, d.apiKey
		var status app.AppStatus
		a.settingsAction(d, func() error {
			if strings.TrimSpace(key) != "" {
				if err := a.service.SaveCredential(model.Provider, strings.TrimSpace(key)); err != nil {
					return err
				}
			}
			var err error
			status, err = a.service.SaveModelConfig(model)
			return err
		}, func() { d.apiKey = ""; d.form.dirty = false; d.message = status.Message })
	})
	ui.Button(c, "检查已保存的连接").Disabled(d.busy).OnClick(func() {
		var status app.AppStatus
		a.settingsAction(d, func() error { status = a.service.GetStatus(); return nil }, func() { d.message = status.Message + "\n可用模型：" + strings.Join(status.AvailableModels, "、") })
	})
	for _, cred := range d.credentials {
		status := "未保存密钥"
		if cred.Configured {
			status = "已保存密钥"
		}
		ui.Text(c, cred.Provider+" · "+status)
		ui.Button(c, "删除 "+cred.Provider+" 密钥").Disabled(d.busy).OnClick(func() {
			a.settingsAction(d, func() error {
				yes, err := confirmNative(a.win, "删除密钥", "使用此服务的后续任务需要重新填写密钥。")
				if err != nil || !yes {
					return err
				}
				return a.service.DeleteCredential(cred.Provider)
			}, func() { a.reloadSettings(d) })
		})
	}
}
func confirmNative(win *mygo.Window, title, message string) (bool, error) {
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: win, Title: title, Message: message, Buttons: []string{"取消", "确认"}, CancelButton: 0, DefaultButton: 0})
	return r.Button == 1, err
}

// Merge only explicitly changed fields, rejecting conflicting concurrent
// permission edits rather than restoring an old complete policy.
func mergeNativePolicy(base, edited, current storage.ContextPolicy) (storage.ContextPolicy, error) {
	merge := func(b, e, c reflect.Value) error {
		for i := 0; i < e.NumField(); i++ {
			if reflect.DeepEqual(b.Field(i).Interface(), e.Field(i).Interface()) {
				continue
			}
			if !reflect.DeepEqual(c.Field(i).Interface(), b.Field(i).Interface()) && !reflect.DeepEqual(c.Field(i).Interface(), e.Field(i).Interface()) {
				return errors.New("此项权限已被其他操作修改，请重新读取后核对")
			}
			c.Field(i).Set(e.Field(i))
		}
		return nil
	}
	permissions := current.Permissions
	if err := merge(reflect.ValueOf(base.Permissions), reflect.ValueOf(edited.Permissions), reflect.ValueOf(&permissions).Elem()); err != nil {
		return current, err
	}
	base.Permissions = permissions
	edited.Permissions = permissions
	current.Permissions = permissions
	err := merge(reflect.ValueOf(base), reflect.ValueOf(edited), reflect.ValueOf(&current).Elem())
	return current, err
}
func (a *Workspace) permissionSettings(c *ui.Context, d *nativeSettings) {
	if d.context == nil {
		return
	}
	ui.Text(c, "此对话的本机权限").Bold()
	if d.form != nil {
		d.form.draw(c, d.busy)
	}
	ui.Button(c, "选择并添加工作文件夹").Disabled(d.busy).OnClick(func() {
		var paths []string
		a.settingsAction(d, func() error {
			var err error
			paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "允许访问的工作文件夹", Directory: true})
			return err
		}, func() {
			if len(paths) > 0 {
				d.context.Policy.Permissions.WorkspacePaths = append(d.context.Policy.Permissions.WorkspacePaths, paths[0])
				d.form.dirty = true
			}
		})
	})
	ui.Button(c, "保存本机权限").Disabled(d.busy || d.form == nil || len(d.form.errors) > 0).OnClick(func() {
		base, edited, id := d.contextBase.Policy, d.context.Policy, d.contextID
		a.settingsAction(d, func() error {
			latest, err := a.service.GetLocalContext(id)
			if err != nil {
				return err
			}
			merged, err := mergeNativePolicy(base, edited, latest.Policy)
			if err != nil {
				return err
			}
			_, err = a.service.UpdateContextPolicy(id, merged)
			return err
		}, func() { d.form.dirty = false; a.reloadSettings(d) })
	})
	ui.Text(c, "材料外发授权").Bold()
	for _, grant := range d.grants {
		ui.Text(c, grant.Destination+" · "+nativeScopeNames(grant.Scopes)).MaxWidth(630)
		ui.Button(c, "撤销此目的地授权").Disabled(d.busy).OnClick(func() {
			id, dest := d.contextID, grant.Destination
			a.settingsAction(d, func() error { return a.service.RevokeExfil(id, dest) }, func() { a.reloadSettings(d) })
		})
	}
	ui.TextInput(c, &d.destination).FillWidth().Label("外发端点 http(s) 地址").Disabled(d.busy)
	for _, scope := range []string{"public", "personal", "workspace", "unknown", "credential"} {
		on := d.checks[scope]
		ui.Checkbox(c, &on, nativeScopeNames([]string{scope})).Disabled(d.busy).OnChange(func() { d.checks[scope] = on })
	}
	ui.Button(c, "授权选定范围到此目的地").Disabled(d.busy || d.destination == "").OnClick(func() {
		scopes := []string{}
		for s, on := range d.checks {
			if on {
				scopes = append(scopes, s)
			}
		}
		id, dest := d.contextID, d.destination
		a.settingsAction(d, func() error {
			if len(scopes) == 0 {
				return errors.New("请选择具体材料范围")
			}
			yes, err := confirmNative(a.win, "允许材料外发", dest+"\n"+nativeScopeNames(scopes))
			if err != nil || !yes {
				return err
			}
			_, err = a.service.GrantExfil(id, dest, scopes)
			return err
		}, func() { a.reloadSettings(d) })
	})
}
func cloneNative[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}
