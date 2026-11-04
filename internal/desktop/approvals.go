package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

type nativeApproval struct {
	open, busy        bool
	call              storage.ToolCall
	preview           *app.ProjectEditPreview
	recovery          *storage.ProjectCallRecovery
	err, note, result string
	page, objectPage  int
	generation        uint64
	resolution        string
}

func (a *Workspace) openApproval(call storage.ToolCall) {
	if a.closeFrozen || a.approval != nil && a.approval.busy {
		return
	}
	a.approval = &nativeApproval{open: true, call: call}
}
func (a *Workspace) approvalAction(d *nativeApproval, run func() error, done func()) {
	if d.busy || a.closeFrozen {
		return
	}
	d.busy = true
	generation := d.generation
	go func() {
		err := run()
		a.update(func() {
			d.busy = false
			if generation != d.generation {
				a.loadAgentHistory(false)
				return
			}
			if err != nil {
				d.err = err.Error()
				return
			}
			d.err = ""
			if done != nil {
				done()
			}
			a.loadAgentHistory(false)
		})
	}()
}
func (a *Workspace) clearCandidate() {
	a.preview.mu.Lock()
	a.preview.value.Candidate = nil
	a.preview.mu.Unlock()
	a.notifyPiano()
}
func (a *Workspace) invalidateCandidate(projectID string) {
	if d := a.approval; d != nil && d.preview != nil && d.preview.ProjectID == projectID {
		d.generation++
		d.err = "工程已变化，候选基准已过时；请拒绝并让助手重新提出。"
		d.preview = nil
		call := d.call.ID
		go a.service.StopProjectEditPreview(call)
		a.clearCandidate()
	}
}
func approvalDescription(call storage.ToolCall) (string, string, string) {
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	name := call.ToolName
	parts := strings.Split(name, "__")
	name = parts[len(parts)-1]
	titles := map[string]string{"read_file": "读取文件或文件夹", "write_file": "写入文件", "edit_file": "修改文件", "exec": "执行本机命令", "delegate_task": "安排协作工作", "load_skill": "使用工作方法", "web_search": "联网查询", "send_material": "发送工程材料", "edit": "修改音乐工程", "transform": "变换音乐音符"}
	title := titles[name]
	if title == "" {
		title = "使用扩展服务"
	}
	target := ""
	for _, k := range []string{"path", "file_path", "filename", "url", "destination", "query", "task", "executable", "command", "cmd"} {
		if v, ok := args[k]; ok {
			target = fmt.Sprint(v)
			break
		}
	}
	if call.Material != nil {
		target = call.Material.Title + " → " + call.Material.Destination
	}
	detail := "只允许此次列出的具体操作；原权限与修订检查继续执行。"
	if name == "exec" {
		detail = "可能修改文件或访问网络，请核对命令、参数和执行目录。"
	}
	return title, target, detail
}
func noteDescription(n *project.Note) string {
	if n == nil {
		return "无"
	}
	return fmt.Sprintf("%s%d · 起点 %d · 时长 %d · 力度 %d · 通道 %d", []string{"C", "C♯", "D", "D♯", "E", "F", "F♯", "G", "G♯", "A", "A♯", "B"}[n.Pitch%12], n.Pitch/12-1, n.Start, n.Duration, n.Velocity, n.Channel+1)
}
func (a *Workspace) approvalDialog(c *ui.Context) {
	d := a.approval
	if d == nil || a.closeFrozen {
		return
	}
	if !d.open && !d.busy {
		go a.service.StopProjectEditPreview(d.call.ID)
		a.clearCandidate()
		a.approval = nil
		return
	}
	ui.Modal(c, &d.open, func() {
		title, target, detail := approvalDescription(d.call)
		ui.Text(c, title).FontSize(18).Bold()
		ui.Text(c, target).MaxWidth(740)
		ui.Text(c, detail).MaxWidth(740)
		ui.Scroll(c).Width(760).MaxHeight(460).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				call := d.call
				if call.Material != nil {
					ui.Text(c, "发送给："+call.Material.Destination+" · "+call.Material.Tool).MaxWidth(740)
					ui.Text(c, call.Material.Summary).MaxWidth(740)
					ui.Text(c, call.Material.Arguments).MaxWidth(740)
				}
				if call.MaterialError != "" {
					ui.Text(c, call.MaterialError).MaxWidth(740)
				}
				if call.ApprovalDestination != "" {
					ui.Text(c, "实际查询目的地："+call.ApprovalDestination).MaxWidth(740)
				}
				if call.State == storage.CallExecutionUnknown {
					ui.Text(c, "操作结果未知。核对原文件或服务；暂不确定会保留记录，不会重新执行。").MaxWidth(740)
					if storage.IsNativeProjectWrite(call) {
						ui.Button(c, "查询实际音乐提交").Disabled(d.busy).OnClick(func() {
							var value storage.ProjectCallRecovery
							a.approvalAction(d, func() error { var err error; value, err = a.service.ReconcileProjectToolCall(call.ID); return err }, func() { d.recovery = &value })
						})
						if d.recovery != nil {
							r := d.recovery
							if r.Result != nil && r.Outcome == "committed" {
								ui.Textf(c, "已查到「%s」修订 %d 的实际提交，未重放操作。", r.Title, r.Result.Revision).MaxWidth(740)
							} else {
								ui.Text(c, "查询结果："+r.Outcome+"；查无记录不证明未执行，来源冲突不能认定成功或未生效。").MaxWidth(740)
							}
						}
					}
					ui.TextArea(c, &d.note).Width(720).Height(64).Label("核对证据与方式").Disabled(d.busy)
					if !storage.IsNativeProjectWrite(call) {
						ui.TextArea(c, &d.result).Width(720).Height(64).Label("实际看到的结果").Disabled(d.busy)
					}
					ui.Row(c).Gap(8).Children(func() {
						if !storage.IsNativeProjectWrite(call) {
							ui.Button(c, "已核实完成").Disabled(d.busy || strings.TrimSpace(d.note) == "" || strings.TrimSpace(d.result) == "").OnClick(func() { a.resolveApproval(d, "succeeded") })
						}
						blocked := d.recovery != nil && (d.recovery.Outcome == "conflict" || d.recovery.Outcome == "committed")
						ui.Button(c, "有证据确认未完成").Disabled(d.busy || strings.TrimSpace(d.note) == "" || blocked).OnClick(func() { a.resolveApproval(d, "failed") })
					})
				} else if storage.IsNativeProjectWrite(call) {
					ui.Text(c, "先查看实际差异，再试听或采用；尚未写入工程，成功后可共同撤销。").MaxWidth(740)
					ui.Button(c, "查看修改差异").Disabled(d.busy).OnClick(func() {
						var p app.ProjectEditPreview
						a.approvalAction(d, func() error { var err error; p, err = a.service.PreviewProjectEdit(call.ID); return err }, func() { d.preview = &p; d.page = 0; d.objectPage = 0 })
					})
					if p := d.preview; p != nil {
						ui.Textf(c, "工程「%s」 · 基础修订 %d · 新增 %d / 修改 %d / 删除 %d 音符", p.Title, p.BaseRevision, p.Diff.Added, p.Diff.Updated, p.Diff.Removed).MaxWidth(740)
						ui.Textf(c, "对象变化 %d 项、音符变化 %d 项；分别展示服务返回的前 %d / %d 项，试听与采用处理整批。", p.Objects.Total, p.Diff.Total, len(p.Objects.Changes), len(p.Diff.Changes)).MaxWidth(740)
						pagedNative(c, "对象变化", len(p.Objects.Changes), &d.objectPage, func(lo, hi int) {
							for _, v := range p.Objects.Changes[lo:hi] {
								ui.Text(c, strings.Join([]string{v.TrackName, v.ClipName, v.Field}, " / ")+"\n原值："+v.Before+"\n候选："+v.After).MaxWidth(730)
							}
						})
						pagedNative(c, "音符变化", len(p.Diff.Changes), &d.page, func(lo, hi int) {
							for _, v := range p.Diff.Changes[lo:hi] {
								ui.Text(c, v.TrackName+" / "+v.ClipName+"\n原值："+noteDescription(v.Before)+"\n候选："+noteDescription(v.After)).MaxWidth(730)
							}
						})
						ui.Row(c).Gap(6).Children(func() {
							for _, variant := range []string{"original", "candidate"} {
								label := "试听原版"
								if variant == "candidate" {
									label = "试听候选"
								}
								ui.Button(c, label).Disabled(d.busy || d.err != "").OnClick(func() {
									a.approvalAction(d, func() error { return a.service.PlayProjectEditPreview(call.ID, variant) }, nil)
								})
							}
							ui.Button(c, "停止试听").OnClick(func() { a.approvalAction(d, func() error { return a.service.StopProjectEditPreview(call.ID) }, nil) })
							if p.NotesOnly {
								ui.Button(c, "卷帘叠加").Disabled(d.busy || d.err != "" || a.active.ID != p.ProjectID || a.active.Revision != p.BaseRevision).OnClick(func() { a.preview.mu.Lock(); a.preview.value.Candidate = p; a.preview.mu.Unlock(); a.notifyPiano() })
							}
						})
					}
				}
				ui.Text(c, "具体参数（只读）").Bold()
				ui.Text(c, call.Arguments).MaxWidth(740)
				if call.Error != "" {
					ui.Text(c, call.Error).MaxWidth(740)
				}
			})
		})
		if d.err != "" {
			ui.Text(c, d.err).MaxWidth(740).TextColor(c.Theme().Warning)
		}
		ui.Row(c).Gap(8).Children(func() {
			if d.call.State == storage.CallPending {
				native := storage.IsNativeProjectWrite(d.call)
				blocked := native && (d.preview == nil || d.err != "") || d.call.ToolName == "local__send_material" && d.call.Material == nil || d.call.ToolName == "local__web_search" && d.call.ApprovalDestination == ""
				ui.Button(c, "允许此次操作").Disabled(d.busy || blocked).OnClick(func() {
					hash := ""
					if d.preview != nil {
						hash = d.preview.ArgumentsHash
					}
					a.approvalAction(d, func() error {
						if native {
							_, err := a.service.ApproveProjectEditPreview(d.call.ID, hash)
							return err
						}
						_, err := a.service.ApproveToolCall(d.call.ID)
						return err
					}, func() { d.open = false })
				})
				ui.Button(c, "拒绝此次操作").Disabled(d.busy).OnClick(func() {
					a.approvalAction(d, func() error { _, err := a.service.DenyToolCall(d.call.ID, "用户拒绝本次操作"); return err }, func() { d.open = false })
				})
			}
			ui.Button(c, "暂不处理").Disabled(d.busy).OnClick(func() { d.open = false })
		})
	})
}
func (a *Workspace) resolveApproval(d *nativeApproval, state string) {
	note, result := d.note, d.result
	a.approvalAction(d, func() error { _, err := a.service.ResolveToolCall(d.call.ID, state, result, note); return err }, func() { d.open = false })
}
func pagedNative(c *ui.Context, label string, count int, page *int, draw func(int, int)) {
	lo := *page * 30
	if lo > count {
		*page = 0
		lo = 0
	}
	hi := lo + 30
	if hi > count {
		hi = count
	}
	draw(lo, hi)
	if count > 30 {
		ui.Row(c).Gap(6).Children(func() {
			ui.Button(c, "上一组"+label).Disabled(*page == 0).OnClick(func() { *page-- })
			ui.Textf(c, "%d/%d", *page+1, (count+29)/30)
			ui.Button(c, "下一组"+label).Disabled(hi == count).OnClick(func() { *page++ })
		})
	}
}
