package desktop

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *Workspace) taskAction(d *nativeTaskDetails, kind string) {
	if d.actionBusy || a.closeFrozen {
		return
	}
	input := d.input
	run, session := d.run.ID, d.run.SessionID
	task := d.graph.WaitingTaskID
	d.actionBusy = true
	go func() {
		var err error
		if kind != "clearPlan" {
			err = ensureNativeConsent(a.service, a.win, session, "main", false)
		}
		if err == nil {
			switch kind {
			case "continue":
				if strings.TrimSpace(input) == "" {
					err = errors.New("请输入继续任务的要求")
				} else {
					_, err = a.service.ContinueConversationTask(run, task, input)
				}
			case "review":
				_, err = a.service.StartConversationReview(session, run)
			case "retrospect":
				_, err = a.service.StartConversationRetrospective(session, run)
			case "clearPlan":
				err = a.service.ClearSessionPlan(session)
			}
		}
		a.update(func() {
			d.actionBusy = false
			if err != nil {
				d.actionError = err.Error()
				return
			}
			d.actionError = ""
			if kind == "continue" {
				d.input = ""
			}
			a.loadAgentHistory(false)
		})
	}()
}
func (a *Workspace) taskActions(c *ui.Context, d *nativeTaskDetails) {
	ui.TextArea(c, &d.input).Width(700).Height(80).Label("继续原任务的补充要求").Disabled(d.actionBusy)
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "继续此任务").Disabled(d.actionBusy || strings.TrimSpace(d.input) == "").OnClick(func() { a.taskAction(d, "continue") })
		ui.Button(c, "审查此任务").Disabled(d.actionBusy).OnClick(func() { a.taskAction(d, "review") })
		ui.Button(c, "复盘此任务").Disabled(d.actionBusy).OnClick(func() { a.taskAction(d, "retrospect") })
		ui.Button(c, "清除此会话计划").Disabled(d.actionBusy).OnClick(func() { a.taskAction(d, "clearPlan") })
	})
	if d.actionError != "" {
		ui.Text(c, d.actionError).MaxWidth(700).TextColor(c.Theme().Warning)
	}
}
