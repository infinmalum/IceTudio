package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *Workspace) transport(c *ui.Context) {
	ui.Row(c).FillWidth().Wrap().Gap(5).AlignItems(ui.Center).Children(func() {
		t := c.Theme()
		ui.Row(c).Gap(2).Padding(2).Radius(6).Background(t.Background).AlignItems(ui.Center).Children(func() {
			ui.PrimaryButton(c, "▶").Size(30, 25).Label("播放工程").Disabled(a.service == nil || a.active.ID == "" || a.audioBusy || !a.audio.Available || a.closeFrozen).Tooltip("从保留位置播放；音频由原 FluidSynth 引擎调度").OnClick(func() { a.audioAction(false) })
			ui.Button(c, "Ⅱ").Size(28, 25).Label("暂停").Tooltip("暂停").Disabled(a.service == nil || a.closeFrozen || !a.audio.Playing).OnClick(func() { a.playPosition = int64(a.audio.Tick); a.audioAction(true) })
			ui.Button(c, "■").Size(28, 25).Label("停止").Tooltip("停止").Disabled(a.service == nil || a.closeFrozen).OnClick(func() { a.playPosition = 0; a.audioAction(true) })
		})
		a.transportReadout(c)
		loop := ui.Button(c, "循环").Height(27).Label("位置 / 循环")
		if a.transportOptions.Loop {
			loop.Background(t.Selection)
		}
		loop.Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(a.beginTransport)
		ui.Button(c, "录音").Height(27).Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(a.openRecording)
		a.navigationTools(c)
		ui.PopoverMenuButton(c, "播放选项", func(m *ui.Menu) {
			if m.Item("节拍器").Checked(a.transportOptions.Metronome).Tooltip("下次播放时启用").Chosen() {
				a.transportOptions.Metronome = !a.transportOptions.Metronome
			}
			if m.Item("预备拍").Checked(a.transportOptions.CountIn).Tooltip("从目标位置按当前拍号预备一小节").Chosen() {
				a.transportOptions.CountIn = !a.transportOptions.CountIn
			}
			m.Separator()
			if m.Item("音频 / 音色").Disabled(a.service == nil || a.closeFrozen).Chosen() {
				a.openAudioSettings()
			}
		}).Height(27).Disabled(a.closeFrozen)
		a.editToolbar(c)
		text := "视觉样例不启动音频"
		if a.service != nil {
			text = "就绪"
			if !a.audio.Available {
				text = "本平台原生音频不可用"
			} else if a.audioBusy {
				text = "正在处理音频…"
			} else if a.audio.Playing {
				text = "正在播放"
				if a.audio.ProjectID == a.active.ID && a.active.PPQ > 0 {
					text = fmt.Sprintf("正在播放 · %s", func() string {
						m, _ := newMeterProjection(a.active.PPQ, a.active.State.Meter)
						return musicTime(m, int64(a.audio.Tick))
					}())
				}
			}
			if a.service != nil && a.service.TakeStatus().Active {
				text = "正在录音 · 打开录音面板停止"
			}
			if a.audio.CountIn {
				text = "预备拍 · 等待进入工程"
			}
			if a.audio.Error != "" {
				text += " · " + a.audio.Error
			} else if len(a.audio.PluginIssues) > 0 {
				text += " · " + a.audio.PluginIssues[0]
			} else if a.audio.Notice != "" {
				text += " · " + a.audio.Notice
			}
		}
		ui.Text(c, text).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().MaxWidth(260).MinWidth(0).Grow(1).Tooltip(text)
	})
}
func (a *Workspace) transportReadout(c *ui.Context) {
	position, tempo, meterLabel := "—", "—", "—"
	if a.active.PPQ > 0 {
		tick := a.playPosition
		if a.audio.Playing && a.audio.ProjectID == a.active.ID {
			tick = int64(a.audio.Tick)
		}
		meter, _ := newMeterProjection(a.active.PPQ, a.active.State.Meter)
		position = musicTime(meter, tick)
		us := 500000
		for _, point := range a.active.State.Tempo {
			if point.Tick > tick {
				break
			}
			if point.USPerQuarter > 0 {
				us = point.USPerQuarter
			}
		}
		tempo = fmt.Sprintf("%.4g", 60000000/float64(us))
		num, denom := 4, 4
		for _, point := range a.active.State.Meter {
			if point.Tick > tick {
				break
			}
			num, denom = point.Numerator, point.Denominator
		}
		meterLabel = fmt.Sprintf("%d/%d", num, denom)
	}
	t := c.Theme()
	ui.Row(c).Height(29).Padding(3, 7).Gap(8).AlignItems(ui.Center).Background(t.Background).Border(1, t.Border).Radius(5).Children(func() {
		ui.Text(c, position).Width(74).SingleLine().Bold().TextColor(t.Accent).Tooltip("音乐位置 " + position)
		ui.Text(c, tempo+" BPM").Width(61).SingleLine().FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, meterLabel).Width(28).SingleLine().FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *Workspace) audioAction(stop bool) {
	if a.service == nil || a.closeFrozen {
		return
	}
	target := a.active
	position := a.playPosition
	request := a.transportOptions
	request.ProjectID = target.ID
	request.Revision = target.Revision
	request.Tick = position
	a.audioSerial++
	serial := a.audioSerial
	a.audioPending++
	a.audioBusy = true
	go func() {
		var err error
		if stop {
			err = a.service.StopPlayback()
		} else {
			err = a.service.PlayProject(request)
		}
		status := a.service.PlaybackStatus()
		a.update(func() {
			a.audioPending--
			a.audioBusy = a.audioPending > 0
			if serial != a.audioSerial {
				return
			}
			a.audio = status
			if err != nil {
				a.message = "音频操作失败：" + err.Error()
			}
		})
	}()
}
func (a *Workspace) watchAudio(stop <-chan struct{}) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			status := a.service.PlaybackStatus()
			a.update(func() { a.updatePlayhead(status) })
		}
	}
}
