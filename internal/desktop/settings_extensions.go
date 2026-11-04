package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/storage"
)

func (a *Workspace) mcpSettings(c *ui.Context, d *nativeSettings) {
	ui.Text(c, "MCP 扩展服务").Bold()
	ui.Text(c, "保存并连接会立即启动所列程序。安装/连接、配置启用和访问授权是不同操作；连接不会自动授予文件或外发权限。").MaxWidth(630)
	for _, s := range d.servers {
		ui.Column(c.Key(s.Name)).FillWidth().Gap(4).Border(1, c.Theme().Border).Padding(8).Children(func() {
			ui.Text(c, s.Name+" · "+s.Command+" "+strings.Join(s.Args, " ")).MaxWidth(610)
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "编辑此服务").Disabled(d.busy || d.form != nil && d.form.dirty).OnClick(func() { copy := cloneNative(s); d.form = newNativeForm(&copy) })
				ui.Button(c, "断开并删除配置").Disabled(d.busy).OnClick(func() {
					name := s.Name
					a.settingsAction(d, func() error {
						yes, err := confirmNative(a.win, "删除 MCP 配置", name+" 的进程将停止，配置及指纹登记被移除。")
						if err != nil || !yes {
							return err
						}
						_, err = a.service.DeleteMCPServer(name)
						return err
					}, func() { a.reloadSettings(d) })
				})
			})
		})
	}
	ui.Button(c, "添加扩展服务").Disabled(d.busy || d.form != nil && d.form.dirty).OnClick(func() { d.form = newNativeForm(&config.MCPServer{}) })
	if d.form != nil {
		if server, ok := d.form.value.(*config.MCPServer); ok {
			d.form.draw(c, d.busy)
			ui.Button(c, "保存并连接（启动程序）").Disabled(d.busy || len(d.form.errors) > 0).OnClick(func() {
				next := cloneNative(*server)
				a.settingsAction(d, func() error {
					yes, err := confirmNative(a.win, "启动 MCP 服务", next.Command+"\n参数："+strings.Join(next.Args, "\n")+"\n工作目录："+next.WorkingDir+"\n可继承环境变量名："+strings.Join(next.Env, "、"))
					if err != nil || !yes {
						return err
					}
					_, err = a.service.SaveMCPServer(next)
					return err
				}, func() { d.form.dirty = false; a.reloadSettings(d) })
			})
		}
	}
	for _, ep := range d.endpoints {
		ui.Text(c, ep.Name+" · "+ep.Command+"\n二进制："+ep.BinarySHA256+"\n工具定义："+ep.SchemaSHA256+"\n工具："+strings.Join(ep.Tools, "、")).MaxWidth(630)
		if ep.Changed {
			ui.Button(c, "核对后信任当前指纹").Disabled(d.busy).OnClick(func() {
				name := ep.Name
				a.settingsAction(d, func() error {
					yes, err := confirmNative(a.win, "重新信任扩展指纹", name+"\n"+ep.BinarySHA256+"\n"+ep.SchemaSHA256)
					if err != nil || !yes {
						return err
					}
					_, err = a.service.ConfirmMCPEndpoint(name)
					return err
				}, func() { a.reloadSettings(d) })
			})
		}
	}
}
func (a *Workspace) runtimeSettings(c *ui.Context, d *nativeSettings) {
	ui.Text(c, "开发者 · 执行配置").Bold()
	ui.Text(c, "仅影响引用配置的后续运行；在途任务继续使用已冻结版本。此处包含主执行、规划、协作、审查、改进、固定步骤与预算全部设置，资源授权仍单独管理。").MaxWidth(630)
	for _, item := range d.configs {
		ui.Button(c, item.Name+fmt.Sprintf(" · r%d", item.Revision)).FillWidth().Disabled(d.busy || d.form != nil && d.form.dirty).OnClick(func() { next := cloneNative(item); d.selectedConfig = next.ID; d.form = newNativeForm(&next) })
	}
	if d.form == nil {
		return
	}
	next, ok := d.form.value.(*storage.RuntimeConfig)
	if !ok {
		return
	}
	ui.Text(c, "主执行 / 规划 / 协作 / 审查 / 改进组件均由原运行时执行；零预算沿用宿主默认。").MaxWidth(630)
	for _, component := range storage.Components() {
		ui.Text(c, component.Name+" · "+component.Description).MaxWidth(630).FontSize(11)
	}
	d.form.draw(c, d.busy)
	ui.Button(c, "保存执行配置的新修订").Disabled(d.busy || len(d.form.errors) > 0).OnClick(func() {
		copy := cloneNative(*next)
		var saved storage.RuntimeConfig
		a.settingsAction(d, func() error { var err error; saved, err = a.service.UpdateRuntimeConfig(copy); return err }, func() { d.form = newNativeForm(&saved); a.reloadSettings(d) })
	})
	if next.IsDefault {
		ui.Button(c, "恢复内置配置默认值").Disabled(d.busy).OnClick(func() {
			revision := next.Revision
			var saved storage.RuntimeConfig
			a.settingsAction(d, func() error {
				yes, err := confirmNative(a.win, "恢复内置执行配置", "将创建新的配置修订；引用此配置的后续任务使用恢复结果。")
				if err != nil || !yes {
					return err
				}
				saved, err = a.service.RestoreRuntimeConfigDefault(revision)
				return err
			}, func() { d.form = newNativeForm(&saved); a.reloadSettings(d) })
		})
	}
}
func (a *Workspace) skillSettings(c *ui.Context, d *nativeSettings) {
	ui.Text(c, "已安装的工作方法").Bold()
	ui.Text(c, "启用不扩大本机权限；运行使用冻结版本。方法提炼、采用与撤销在“反馈与方法”中进行。").MaxWidth(630)
	for _, skill := range d.skills {
		ui.Column(c.Key(skill.Name)).FillWidth().Gap(4).Children(func() {
			ui.Text(c, skill.Name).Bold()
			ui.Text(c, skill.Description).MaxWidth(630)
			on := !skill.Disabled
			ui.Checkbox(c, &on, "允许执行配置使用此方法（不自动加入任务）").Disabled(d.busy).OnChange(func() {
				name, disabled := skill.Name, !on
				a.settingsAction(d, func() error { return a.service.SetSkillDisabled(name, disabled) }, func() { a.reloadSettings(d) })
			})
		})
	}
	ui.Button(c, "反馈与方法草案").Disabled(d.busy).OnClick(func() { d.open = false; a.openSkillPanel() })
}

// Used by settings readers and tests; model keys are never returned here.
var _ []app.CredentialStatus
