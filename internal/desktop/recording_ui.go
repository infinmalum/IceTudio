package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/audioasset"
)

type recordingForm struct {
	open, monitor, rebase, busy, loop  bool
	kind, track, device, position, err string
	devices                            []audioasset.Device
	takes                              []audioasset.TakeStatus
	sequence                           uint64
}

func (a *Workspace) openRecording() {
	if a.service == nil || a.active.ID == "" {
		return
	}
	if a.recording == nil {
		a.recording = &recordingForm{kind: "audio"}
	}
	f := a.recording
	f.open = true
	m, _ := newMeterProjection(a.active.PPQ, a.active.State.Meter)
	f.position = musicTime(m, a.playPosition)
	if len(a.active.State.Tracks) > 0 {
		f.track = a.active.State.Tracks[0].ID
	}
	a.refreshRecording(f)
}
func (a *Workspace) refreshRecording(f *recordingForm) {
	f.sequence++
	sequence := f.sequence
	kind, id := f.kind, a.active.ID
	go func() {
		devices, deviceErr := a.service.TakeDevices(kind)
		takes, takeErr := a.service.PendingTakes(id)
		a.update(func() {
			if f.sequence != sequence || a.active.ID != id {
				return
			}
			f.devices, f.takes = devices, takes
			f.err = ""
			if deviceErr != nil {
				f.err = "设备枚举：" + deviceErr.Error()
			}
			if takeErr != nil {
				f.err = "录音恢复：" + takeErr.Error()
			}
		})
	}()
}

func (a *Workspace) recordingDialog(c *ui.Context) {
	f := a.recording
	if f == nil || !f.open || a.closeFrozen {
		return
	}
	status := a.service.TakeStatus()
	ui.Modal(c, &f.open, func() {
		ui.Text(c, "录音与 Take 恢复").FontSize(18).Bold()
		ui.Column(c).Width(480).Gap(5).Children(func() {
			ui.Row(c).Gap(5).Disabled(status.Active || f.busy).Children(func() {
				ui.Select(c, &f.kind, []string{"audio", "midi"}).Label("录音类型").OnChange(func() { f.device = ""; a.refreshRecording(f) })
				audioTrackSelect(c, a.active.State.Tracks, &f.track, "")
				ui.Button(c, "刷新设备").OnClick(func() { a.refreshRecording(f) })
			})
			device := ui.SelectBase(c, &f.device)
			label := "默认音频输入"
			if f.kind == "midi" {
				label = "选择 MIDI 输入"
			}
			for _, d := range f.devices {
				if d.ID == f.device {
					label = d.Name
				}
			}
			device.Trigger.Label("录音输入设备").Disabled(status.Active || f.busy).Padding(5, 8).Background(c.Theme().SurfaceHover).Children(func() { ui.Text(c, label+" ▾") })
			device.Popup(func(panel ui.Element) {
				panel.Background(c.Theme().Surface)
				ui.Scroll(c).MaxHeight(130).Children(func() {
					if f.kind == "audio" {
						device.Item("").Padding(6, 8).Children(func() { ui.Text(c, "系统默认输入") })
					}
					for _, d := range f.devices {
						device.Item(d.ID).Padding(6, 8).Children(func() { ui.Text(c, d.Name) })
					}
				})
			})
			ui.TextInput(c, &f.position).Label("录音或恢复位置").Disabled(status.Active || f.busy)
			if f.kind == "audio" {
				ui.Checkbox(c, &f.monitor, "输入监听").Disabled(status.Active || f.busy)
				ui.Checkbox(c, &f.loop, "按传输循环窗口拆轮").Disabled(status.Active || f.busy || !a.transportOptions.Loop)
				ui.Text(c, "监听时请使用耳机。循环拆轮按采集帧计数。").FontSize(12)
			}
			if status.ID != "" {
				unit := "帧"
				if status.Kind == "midi" {
					unit = "条 MIDI 事件"
				}
				ui.Textf(c, "已采集 %d %s · 输入峰值 %d%%", status.Frames, unit, status.Peak*100/32768)
				if status.Error != "" {
					ui.Text(c, status.Error).MaxWidth(460).TextColor(c.Theme().Warning)
				}
			}
			if f.err != "" {
				ui.Text(c, f.err).MaxWidth(460).TextColor(c.Theme().Warning)
			}
			ui.Row(c).Gap(5).Children(func() {
				ui.PrimaryButton(c, "开始录音").Disabled(status.Active || f.busy || f.track == "" || f.kind == "midi" && f.device == "" || !a.writable()).OnClick(func() { a.recordTake(f) })
				ui.Button(c, "停止并保留 take").Disabled(!status.Active || f.busy).OnClick(func() {
					f.busy = true
					go func() { a.service.StopTake(); a.update(func() { f.busy = false; a.refreshRecording(f) }) }()
				})
				ui.Button(c, "返回").OnClick(func() { f.open = false })
			})
			ui.Button(c, "打开音频 Take Comp").Disabled(f.busy || status.Active || !a.writable()).OnClick(func() { f.open = false; a.openComp() })
			ui.Checkbox(c, &f.rebase, "明确按当前修订和位置恢复")
			ui.Scroll(c).MaxHeight(155).Children(func() {
				for _, take := range f.takes {
					if take.Active {
						continue
					}
					ui.Row(c.Key(take.ID)).Gap(4).Children(func() {
						label := fmt.Sprintf("%s · %d tick · 修订 %d", take.Kind, take.Tick, take.Revision)
						ui.Text(c, label).Grow(1)
						ui.Button(c, "提交 take").Disabled(f.busy || status.Active || take.Frames == 0 || !a.writable()).OnClick(func() { a.saveTake(f, take) })
					})
				}
			})
		})
	})
}

func (a *Workspace) recordingPosition(f *recordingForm) (int64, error) {
	m, err := newMeterProjection(a.active.PPQ, a.active.State.Meter)
	if err != nil {
		return 0, err
	}
	return parseMusicTime(m, f.position)
}
func (a *Workspace) recordTake(f *recordingForm) {
	tick, err := a.recordingPosition(f)
	if err != nil {
		f.err = err.Error()
		return
	}
	loopEnd := int64(0)
	if f.kind == "audio" && f.loop {
		if !a.transportOptions.Loop || a.transportOptions.LoopEnd <= a.transportOptions.LoopStart {
			f.err = "请先设置传输循环窗口"
			return
		}
		tick, loopEnd = a.transportOptions.LoopStart, a.transportOptions.LoopEnd
	}
	t := audioasset.Take{Kind: f.kind, ProjectID: a.active.ID, TrackID: f.track, Revision: a.active.Revision, Tick: tick, Device: f.device, Monitor: f.monitor && f.kind == "audio"}
	f.busy = true
	go func() {
		err := a.service.StartTakeLoop(t, loopEnd)
		a.update(func() {
			f.busy = false
			if err != nil {
				f.err = err.Error()
			} else {
				f.err = ""
			}
		})
	}()
}
func (a *Workspace) saveTake(f *recordingForm, take audioasset.TakeStatus) {
	tick := take.Tick
	if f.rebase {
		var err error
		tick, err = a.recordingPosition(f)
		if err != nil {
			f.err = err.Error()
			return
		}
	}
	id, revision, accept := a.active.ID, a.active.Revision, f.rebase
	f.busy = true
	a.editBusy = true
	a.preview.freeze(true)
	a.notifyPiano()
	go func() {
		_, err := a.service.SaveTake(id, take.ID, revision, tick, accept)
		a.update(func() {
			f.busy = false
			a.editBusy = false
			a.preview.freeze(a.closeFrozen)
			a.notifyPiano()
			if err != nil {
				f.err = err.Error()
				return
			}
			f.err = ""
			a.open(id)
			a.refreshRecording(f)
		})
	}()
}
