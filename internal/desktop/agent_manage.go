package desktop

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/storage"
)

func (a *Workspace) manageAgent(run func() error, done func()) {
	if a.composerBlocked() {
		return
	}
	a.composer.busy = true
	go func() {
		err := run()
		a.update(func() {
			a.composer.busy = false
			if err != nil {
				a.composer.actionError = err.Error()
				return
			}
			a.composer.actionError = ""
			if done != nil {
				done()
			}
			a.refreshConversations()
			a.loadAgentHistory(false)
		})
	}()
}
func (a *Workspace) agentManagement(c *ui.Context) {
	if !a.composer.manage || a.closeFrozen {
		return
	}
	key := a.agent.selected
	ui.Modal(c, &a.composer.manage, func() {
		ui.Text(c, "对话设置").FontSize(18).Bold()
		ui.TextInput(c, &a.composer.title).Width(480).Label("对话名称").Disabled(a.composer.busy)
		ui.Button(c, "保存名称").Disabled(a.composerBlocked()).OnClick(func() {
			title := a.composer.title
			a.manageAgent(func() error { _, err := a.service.RenameConversation(key, title); return err }, nil)
		})
		ui.Button(c, "权限与材料外发设置").Disabled(a.composerBlocked()).OnClick(func() { a.composer.manage = false; a.openSettings("权限") })
		ui.Button(c, "反馈与方法").Disabled(a.composerBlocked()).OnClick(func() { a.composer.manage = false; a.openSkillPanel() })
		ui.Text(c, "工程绑定仅影响下一次任务；运行中的冻结范围保持不变。").MaxWidth(480)
		ui.Row(c).Gap(8).Children(func() {
			id := a.active.ID
			ui.Button(c, "绑定当前工程").Disabled(a.composerBlocked() || id == "").OnClick(func() { a.manageAgent(func() error { return a.service.SetConversationProject(key, id) }, nil) })
			ui.Button(c, "解除工程绑定").Disabled(a.composerBlocked()).OnClick(func() { a.manageAgent(func() error { return a.service.SetConversationProject(key, "") }, nil) })
		})
		if a.composer.actionError != "" {
			ui.Text(c, a.composer.actionError).MaxWidth(480).TextColor(c.Theme().Warning)
		}
		ui.Button(c, "删除对话…").Disabled(a.composerBlocked()).OnClick(func() {
			deleted := false
			a.manageAgent(func() error {
				answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Title: "删除对话", Message: "删除对话与其任务历史。音乐工程和 Native 未发送输入会保留；运行中或存在未知结果时由服务拒绝。", Buttons: []string{"取消", "删除对话"}})
				if err != nil {
					return err
				}
				if answer.Button != 1 {
					return nil
				}
				err = a.service.DeleteConversation(key)
				deleted = err == nil
				return err
			}, func() {
				if deleted {
					a.selectConversation("")
				}
				a.composer.manage = false
			})
		})
		ui.Button(c, "返回工作区").Disabled(a.composer.busy).OnClick(func() { a.composer.manage = false })
	})
}
func (a *Workspace) cancelNativeTask(run storage.Run) {
	a.manageAgent(func() error { return a.service.CancelTask(run.ID) }, nil)
}
func composerCloseState(x *agentComposer) (string, bool) {
	count, unknown := 0, 0
	for _, d := range x.entries {
		if d.Text != "" || len(d.Uploads) > 0 {
			count++
		}
		if d.Dispatching {
			unknown++
		}
	}
	return fmt.Sprintf("%t/%t/%d/%d/%d/%d/%d", x.ready, x.busy, x.writes, x.version, len(x.errors), count, unknown) + x.loadError, !x.ready || x.busy || x.writes > 0 || len(x.errors) > 0 || x.loadError != "" || count > 0 || unknown > 0
}
