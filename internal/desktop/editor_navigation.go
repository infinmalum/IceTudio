package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (a *Workspace) editorKey(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputKeyDown || a.closeFrozen || a.form != nil || a.mapForm != nil || a.showKeyHelp {
		return false
	}
	mod := ev.Mods&(ui.Ctrl|ui.Cmd) != 0
	if mod && ev.Key == ui.KeyZ {
		if ev.Mods&ui.Shift != 0 {
			a.mutate("redo", nil)
		} else {
			a.mutate("undo", nil)
		}
		return true
	}
	if ev.Mods == 0 && ev.Key == ui.KeySpace {
		if a.audio.Playing {
			a.playPosition = int64(a.audio.Tick)
			a.audioAction(true)
		} else {
			a.audioAction(false)
		}
		return true
	}
	if ev.Mods == 0 && ev.Key == ui.KeyHome {
		a.seekPosition(0)
		a.arrangementUI.start = 0
		a.sendPianoCommand("locate", 0)
		return true
	}
	return false
}
func (a *Workspace) seekPosition(tick int64) {
	if a.service == nil || a.closeFrozen || a.active.ID == "" || a.audioBusy {
		return
	}
	a.playPosition = max(0, min(project.MaxTick*2, tick))
	if !a.audio.Playing || a.audio.ProjectID != a.active.ID {
		return
	}
	req := a.transportOptions
	req.ProjectID = a.active.ID
	req.Revision = a.active.Revision
	req.Tick = a.playPosition
	a.audioPending++
	a.audioBusy = true
	go func() {
		err := a.service.SeekPlayback(req)
		status := a.service.PlaybackStatus()
		a.update(func() {
			a.audioPending--
			a.audioBusy = a.audioPending > 0
			a.audio = status
			if err != nil {
				a.message = "定位失败：" + err.Error()
			}
		})
	}()
}
func (a *Workspace) navigationTools(c *ui.Context) {
	anchor := ui.Button(c, "导航 ▾").Label("导航和播放跟随").Disabled(a.closeFrozen).OnClick(func() { a.navigationOpen = !a.navigationOpen })
	ui.Popover(c, anchor, &a.navigationOpen, func() {
		ui.Column(c).Width(260).Gap(8).Children(func() {
			ui.Text(c, "定位小节与拍").Bold().FontSize(12)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &a.positionInput).Grow(1).MinWidth(0).Label("定位小节拍位置").Placeholder("小节:拍:tick")
				ui.Button(c, "定位").Disabled(a.active.ID == "" || a.closeFrozen).OnClick(func() {
					index, err := newMeterProjection(a.active.PPQ, a.active.State.Meter)
					var tick int64
					if err == nil {
						tick, err = parseMusicTime(index, a.positionInput)
					}
					if err != nil {
						a.positionError = err.Error()
						return
					}
					a.positionError = ""
					a.navigationOpen = false
					a.arrangementUI.start = tick
					a.followPaused = true
					a.seekPosition(tick)
					ctx := a.preview.Context()
					a.sendPianoCommand("locate", int(max(0, tick-ctx.Clip.Start)))
				})
			})
			follow := &a.preferences.Follow
			if *follow == "" {
				*follow = "关闭"
			}
			ui.Text(c, "播放跟随").FontSize(12)
			ui.Select(c, follow, []string{"关闭", "页跟随", "连续跟随"}).FillWidth().Label("播放跟随").OnChange(func() { a.followPaused = false; a.savePreferences() })
			if a.followPaused {
				ui.Button(c, "恢复跟随").OnClick(func() { a.followPaused = false })
			}
			if a.positionError != "" {
				ui.Text(c, a.positionError).FontSize(11).TextColor(c.Theme().Warning)
			}
		})
	})
}
func (a *Workspace) updatePlayhead(status playback.Status) {
	if f := a.form; f != nil && f.mixer.write != nil {
		w := f.mixer.write
		if !a.validMixerWrite(f, status) {
			a.finishMixerWrite(f)
		} else if w.start >= 0 && w.mode == "锁定" && int64(status.Tick)-w.last >= int64(max(1, a.active.PPQ/4)) && f.writes < 2 {
			a.captureMixerPoint(f, int64(status.Tick))
		}
	}
	a.audio = status
	v := &a.arrangementUI
	v.playing = status.Playing && status.ProjectID == a.active.ID
	v.playTick = status.Tick
	if v.playing && !a.followPaused && v.gesture == nil && a.preferences.Follow != "关闭" && a.preferences.Follow != "" && a.active.PPQ > 0 {
		span := max(1, int64(float64(v.width)/max(16, v.zoom)*float64(a.active.PPQ)))
		if a.preferences.Follow == "连续跟随" {
			v.start = max(0, int64(status.Tick)-span/3)
		} else if status.Tick < float64(v.start) || status.Tick >= float64(v.start+span) {
			v.start = max(0, int64(status.Tick))
		}
	}
	if a.piano != nil {
		command := a.preview.command("playhead", 0)
		command.Tick = status.Tick
		command.Playing = v.playing
		command.Follow = a.preferences.Follow
		if a.followPaused {
			command.Follow = "关闭"
		}
		pianoCommand.EmitPage(a.piano.Page(), command)
	}
}
func (a *Workspace) handlePianoNavigation(ctx PianoContext, name string, tick, end int64) {
	if a.closeFrozen {
		return
	}
	if ctx.ProjectID != a.active.ID || ctx.Revision != a.active.Revision {
		a.message = "工程已变化，请重新定位"
		return
	}
	switch name {
	case "toggle":
		a.editorKey(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySpace})
	case "home":
		a.seekPosition(0)
	case "seek":
		a.seekPosition(tick)
	case "loop":
		if end <= tick {
			return
		}
		a.transportOptions.Loop = true
		a.transportOptions.LoopStart = tick
		a.transportOptions.LoopEnd = end
		if a.audio.Playing {
			a.seekPosition(tick)
		}
	case "manual":
		a.followPaused = true
	default:
		a.message = fmt.Sprintf("不支持的导航：%s", name)
	}
}
