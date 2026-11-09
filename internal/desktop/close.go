package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

type pianoBlocker struct {
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	Version   string `json:"version"`
	Preserved bool   `json:"preserved"`
}
type pianoInspection struct {
	Token    string         `json:"token"`
	Version  string         `json:"version"`
	Blockers []pianoBlocker `json:"blockers"`
}

func (a *Workspace) requestClose(prevent func()) {
	if a.closeApproved || a.edits == nil {
		return
	}
	prevent()
	if a.closeFrozen {
		return
	}
	if a.form != nil {
		a.finishMixerWrite(a.form)
	}
	a.stopMaterialPreview()
	a.closeHadGesture = a.arrangementUI.gesture != nil || a.arrangementUI.marquee != nil || a.arrangementUI.patternDrag != nil || a.arrangementUI.materialDrag != nil || a.arrangementUI.audioDrag != nil
	a.closeFrozen = true
	a.closeDialogOpen = true
	a.closeToken = project.NewID("close")
	a.preview.freeze(true)
	a.closeVersion = a.edits.State().Version
	a.closeNativeVersion = a.nativeCloseVersion()
	a.inspectClose(false)
	a.win.Invalidate()
}
func (a *Workspace) inspectClose(confirm bool) {
	if a.closeInspectBusy {
		return
	}
	token := a.closeToken
	a.closeInspectBusy = true
	go func() {
		var report pianoInspection
		var err error
		if a.piano == nil {
			err = fmt.Errorf("卷帘尚未加载")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			encoded, _ := json.Marshal(token)
			var value any
			value, err = a.piano.Page().EvalContext(ctx, "JSON.stringify(window.__icetudioPiano.inspect("+string(encoded)+"))")
			if err == nil {
				raw, ok := value.(string)
				if !ok {
					err = fmt.Errorf("卷帘检查响应类型错误")
				} else if len(raw) > 1<<20 {
					err = fmt.Errorf("卷帘检查响应超限")
				} else {
					err = json.Unmarshal([]byte(raw), &report)
				}
			}
			if err == nil && report.Token != token {
				err = fmt.Errorf("卷帘关闭响应已过期")
			}
		}
		a.update(func() {
			if !a.closeFrozen || a.closeToken != token {
				return
			}
			a.closeInspectBusy = false
			state := a.edits.State()
			old := a.closeReport
			a.closeReport = report
			if confirm {
				if closeSnapshotChanged(a.closeVersion, state.Version, a.closeNativeVersion, a.nativeCloseVersion(), old.Version, report.Version, err == nil) {
					a.closeNativeVersion = a.nativeCloseVersion()
					a.closeVersion = state.Version
					a.closeError = "编辑状态已变化，请核对后再次确认退出"
					return
				}
				a.closeApproved = true
				mygo.App.Quit()
				return
			}
			a.closeVersion = state.Version
			if err != nil {
				a.closeError = "卷帘状态未知：" + err.Error()
				return
			}
			a.closeError = ""
			if !a.nativeCloseBlocked() && !a.closeHadGesture && !a.editBusy && state.Ready && !state.Busy && state.Pending == nil && len(report.Blockers) == 0 {
				a.closeApproved = true
				mygo.App.Quit()
			}
		})
	}()
}
func (a *Workspace) cancelClose() {
	a.cancelArrangement()
	token := a.closeToken
	a.closeFrozen = false
	a.closeDialogOpen = false
	a.closeToken = ""
	a.closeInspectBusy = false
	a.closeError = ""
	a.preview.freeze(a.form != nil || a.mapForm != nil || a.fileBusy)
	a.notifyPiano()
	if a.piano != nil {
		go func() {
			encoded, _ := json.Marshal(token)
			_, _ = a.piano.Page().Eval("window.__icetudioPiano?.resume(" + string(encoded) + ")")
		}()
	}
}
func (a *Workspace) closeDialog(c *ui.Context) {
	if !a.closeFrozen {
		return
	}
	if !a.closeDialogOpen {
		a.cancelClose()
		return
	}
	ui.Modal(c, &a.closeDialogOpen, func() {
		ui.Text(c, "退出前检查编辑状态").FontSize(18).Bold()
		state := a.edits.State()
		ui.Text(c, state.Message).MaxWidth(480)
		if a.savedMap != nil && a.savedMap.Payload != "" {
			ui.Text(c, "已保留尚未应用的时间图草稿；重开后显式恢复。").MaxWidth(480)
		}
		if a.mapForm != nil {
			ui.Text(c, "时间图表单仍打开，请核对持久保存状态后退出。").MaxWidth(480)
		}
		if a.mapError != "" {
			ui.Text(c, "时间图草稿检查失败："+a.mapError).MaxWidth(480)
		}
		if !a.mapReady {
			ui.Text(c, "时间图草稿尚未检查完成。")
		}
		if _, blocked := composerCloseState(&a.composer); a.service != nil && blocked {
			ui.Text(c, "Agent 输入含保留草稿、进行中操作或待核对发送；返回检查保存状态。").MaxWidth(480)
		}
		if _, blocked := a.panelCloseState(); blocked {
			ui.Text(c, "设置、反馈、任务或材料面板有未保存输入或进行中操作；返回核对后再退出。").MaxWidth(480)
		}
		if a.service != nil && a.service.TakeStatus().Active {
			ui.Text(c, "录音仍在运行。请返回，停止录音并保留 take。")
		}
		if a.materialShelf.dirty || a.materialShelf.busy {
			ui.Text(c, "素材索引有未保存标签或进行中的操作。返回素材栏可保存、丢弃或等待完成。").MaxWidth(480)
		}
		if a.audioBusy {
			ui.Text(c, "音频或音色操作仍在进行，请等待完成。").MaxWidth(480)
		}
		if a.fileBusy {
			ui.Text(c, "原生文件选择或工程操作仍在进行。请返回完成或取消。").MaxWidth(480)
		}
		if a.form != nil {
			ui.Text(c, "原生属性表单尚未应用；请返回核对草稿保存状态。").MaxWidth(480)
			if a.form.err != "" {
				ui.Text(c, a.form.err).MaxWidth(480)
			}
		}
		if a.draftError != "" {
			ui.Text(c, "原生音乐草稿读取失败："+a.draftError).MaxWidth(480)
		}
		if len(a.savedInputs) > 0 {
			ui.Textf(c, "保留 %d 项尚未应用的音乐输入；下次显式恢复，不自动提交。", len(a.savedInputs)).MaxWidth(480)
		}
		if a.closeHadGesture {
			ui.Text(c, "片段拖动尚未提交；返回后将取消预览，工程保持原值。").MaxWidth(480)
		}
		if a.closeInspectBusy {
			ui.Text(c, "正在冻结并检查卷帘…")
		}
		if a.closeError != "" {
			ui.Text(c, a.closeError).MaxWidth(480).TextColor(c.Theme().Warning)
		}
		for _, b := range a.closeReport.Blockers {
			ui.Text(c, b.Message).MaxWidth(480)
		}
		ui.Text(c, "仍然退出会保留已持久保存的草稿和恢复记录；未应用且未持久保存的输入可能丢失。重开查询原提交，不会重放。").MaxWidth(480)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "返回核对").OnClick(a.cancelClose)
			ui.Button(c, "仍然退出").Disabled(a.closeInspectBusy).OnClick(func() { a.inspectClose(true) })
		})
	})
}

func (a *Workspace) nativeCloseVersion() string {
	v := ""
	if a.savedMap != nil {
		v = a.savedMap.Token
	}
	if f := a.form; f != nil {
		v += fmt.Sprintf("%d/%d/%s/%t", f.version, f.writes, f.err, f.dirty)
	}
	if f := a.mapForm; f != nil {
		v += fmt.Sprintf("/map:%d/%d/%s/%t", f.version, f.writes, f.err, f.committed)
	}
	panels, _ := a.panelCloseState()
	composer, _ := composerCloseState(&a.composer)
	return panels + composer + fmt.Sprintf("%t/%s/%s/%d/%t/%s/%t/%t", a.fileBusy || a.audioBusy, v, a.draftError, len(a.savedInputs), a.mapReady, a.mapError, a.materialShelf.dirty, a.materialShelf.busy)
}
func (a *Workspace) nativeCloseBlocked() bool {
	if a.service != nil && (a.service.TakeStatus().Active || a.recording != nil && a.recording.busy) {
		return true
	}
	_, panelsBlocked := a.panelCloseState()
	_, chatBlocked := composerCloseState(&a.composer)
	return a.materialShelf.dirty || a.materialShelf.busy || panelsBlocked || (a.service != nil && chatBlocked) || (a.savedMap != nil && a.savedMap.Payload != "") || !a.mapReady || a.mapError != "" || a.mapForm != nil || a.fileBusy || a.audioBusy || a.form != nil || a.draftError != "" || len(a.savedInputs) > 0
}

// A confirmation covers only the inspected snapshot. Unknown page state still
// requires the explicit force-exit path; it is never reported as clean.
func closeSnapshotChanged(oldEdit, newEdit uint64, oldNative, newNative, oldPage, newPage string, pageKnown bool) bool {
	return oldEdit != newEdit || oldNative != newNative || pageKnown && oldPage != newPage
}
