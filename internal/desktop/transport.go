package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *Workspace) transport(c *ui.Context) {
	ui.Row(c).Height(36).Gap(8).AlignItems(ui.Center).Children(func() {
		t := c.Theme()
		ui.Row(c).Gap(2).Padding(2).Radius(t.Radius + 2).Background(t.Surface).AlignItems(ui.Center).Children(func() {
			ui.PrimaryButton(c, "播放工程").Disabled(a.service == nil || a.active.ID == "" || a.audioBusy || !a.audio.Available || a.closeFrozen).Tooltip("从保留位置播放；音频由原 FluidSynth 引擎调度").OnClick(func() { a.audioAction(false) })
			ui.Button(c, "暂停").Disabled(a.service == nil || a.closeFrozen || !a.audio.Playing).OnClick(func() { a.playPosition = int64(a.audio.Tick); a.audioAction(true) })
			ui.Button(c, "停止").Disabled(a.service == nil || a.closeFrozen).OnClick(func() { a.playPosition = 0; a.audioAction(true) })
		})
		ui.Button(c, "位置 / 循环").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(a.beginTransport)
		ui.Button(c, "录音").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(a.openRecording)
		ui.Button(c, "音频 / 音色").Disabled(a.service == nil || a.closeFrozen).OnClick(a.openAudioSettings)
		ui.Checkbox(c, &a.transportOptions.Metronome, "节拍器").Tooltip("下次播放时启用")
		ui.Checkbox(c, &a.transportOptions.CountIn, "预备拍").Tooltip("从目标位置按当前拍号预备一小节")
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
			} else if a.audio.Notice != "" {
				text += " · " + a.audio.Notice
			}
		}
		ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().MinWidth(0).Grow(1)
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
