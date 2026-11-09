package desktop

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (a *Workspace) beginTransport() {
	if !a.projectAvailable() || a.active.ID == "" {
		return
	}
	a.beginProperties("transport", project.Track{}, project.Clip{})
	if a.form == nil {
		return
	}
	v := &a.form.values
	v.Position = musicTime(a.form.meter, a.playPosition)
	v.LoopStart = musicTime(a.form.meter, 0)
	end := int64(a.active.PPQ) * 4
	for _, t := range a.active.State.Tracks {
		for _, clip := range t.Clips {
			end = max(end, clip.Start+clip.Length)
		}
	}
	v.LoopEnd = musicTime(a.form.meter, end)
	if a.audio.ProjectID == a.active.ID {
		v.Position = musicTime(a.form.meter, int64(a.audio.Tick))
		v.Loop = a.audio.Loop
		v.LoopStart = musicTime(a.form.meter, a.audio.LoopStart)
		if a.audio.LoopEnd > 0 {
			v.LoopEnd = musicTime(a.form.meter, a.audio.LoopEnd)
		}
	}
}
func transportRequest(f *propertyForm) (playback.Request, error) {
	r := playback.Request{ProjectID: f.entry.Target.ID, Revision: f.entry.Target.Revision, Loop: f.values.Loop}
	for _, field := range []struct {
		label, raw string
		value      *int64
	}{{"播放位置", f.values.Position, &r.Tick}, {"循环起点", f.values.LoopStart, &r.LoopStart}, {"循环终点", f.values.LoopEnd, &r.LoopEnd}} {
		v, err := parseMusicTime(f.meter, field.raw)
		if err != nil || v < 0 || v > project.MaxTick*2 {
			return r, fmt.Errorf("%s须为有效小节:拍:tick 或整数 tick", field.label)
		}
		*field.value = v
	}
	if r.Loop && r.LoopEnd <= r.LoopStart {
		return r, fmt.Errorf("循环终点必须大于起点")
	}
	return r, nil
}
func (a *Workspace) applyTransport(f *propertyForm, seek bool) {
	if f.writes > 0 || f.err != "" || a.closeFrozen || a.audioBusy {
		return
	}
	if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
		f.err = "工程或修订已变化，原位置输入保留；请核对后重新打开位置表单"
		return
	}
	r, err := transportRequest(f)
	if err != nil {
		f.err = err.Error()
		return
	}
	r.Metronome = a.transportOptions.Metronome
	r.CountIn = a.transportOptions.CountIn
	f.applying = true
	a.audioPending++
	a.audioBusy = true
	go func() {
		var err error
		if seek {
			err = a.service.SeekPlayback(r)
		} else {
			err = a.service.PlayProject(r)
		}
		status := a.service.PlaybackStatus()
		a.update(func() {
			a.audioPending--
			a.audioBusy = a.audioPending > 0
			f.applying = false
			a.audio = status
			if err != nil {
				f.err = err.Error()
				return
			}
			a.transportOptions = r
			a.playPosition = r.Tick
			a.clearForm(f, a.dismissForm)
		})
	}()
}
func (a *Workspace) audioSettings(c *ui.Context) {
	if !a.showAudio || a.closeFrozen {
		return
	}
	ui.Modal(c, &a.showAudio, func() {
		ui.Text(c, "音频与音色").FontSize(18).Bold()
		ui.Column(c).Width(600).Gap(8).Children(func() {
			ui.Textf(c, "%s · %d Hz · 音源 %s", a.audio.Engine, a.audio.SampleRate, a.audio.ResolvedFont)
			if a.audio.Error != "" {
				ui.Text(c, a.audio.Error).TextColor(c.Theme().Warning)
			}
			if a.audio.Notice != "" {
				ui.Text(c, a.audio.Notice).MaxWidth(580)
			}
			ui.Textf(c, "驱动：%s", a.audio.Settings.Driver)
			ui.Row(c).Gap(6).Children(func() {
				for _, driver := range []string{"pulseaudio", "pipewire", "alsa"} {
					ui.Button(c, "使用 "+driver).Disabled(a.audioBusy || !a.audio.Available).OnClick(func() {
						settings := a.audio.Settings
						settings.Driver = driver
						a.runAudioSetting(func() error { return a.service.ConfigureAudio(settings) })
					})
				}
			})
			ui.Row(c).Gap(6).Children(func() {
				ui.Button(c, "添加本地 SoundFont").Disabled(a.audioBusy || !a.audio.Available).OnClick(func() {
					a.runAudioSetting(func() error {
						paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择本地 SoundFont", Filters: []mygo.FileFilter{{Name: "SoundFont", Extensions: []string{"sf2", "sf3"}}}})
						if err != nil || len(paths) == 0 {
							return err
						}
						_, err = a.service.AddSoundFont(paths[0])
						return err
					})
				})
				ui.Button(c, "重置音频设置").Disabled(a.audioBusy || !a.audio.Available).OnClick(func() {
					a.runAudioSetting(func() error {
						res, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Title: "重置音频设置", Message: "恢复默认驱动和音色列表？原音色文件不会删除。", Buttons: []string{"保留", "重置"}, CancelButton: 0})
						if err != nil || res.Button != 1 {
							return err
						}
						return a.service.ResetAudioSettings()
					})
				})
			})
			ui.Scroll(c).Height(300).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					for _, font := range a.fonts.Fonts {
						ui.Column(c.Key(font.ID)).Gap(4).Children(func() {
							label := font.Name
							if font.IsDefault {
								label += " · 默认"
							}
							if font.Active {
								label += " · 已载入"
							}
							ui.Text(c, label).Bold()
							ui.Text(c, font.Path).FontSize(11).MaxWidth(580)
							if font.Error != "" {
								ui.Text(c, font.Error).TextColor(c.Theme().Warning)
							}
							ui.Row(c).Gap(6).Children(func() {
								ui.Button(c, "设为默认").Disabled(a.audioBusy).OnClick(func() { a.runAudioSetting(func() error { return a.service.SetDefaultSoundFont(font.ID) }) })
								ui.Button(c, "上移").Disabled(a.audioBusy).OnClick(func() { a.runAudioSetting(func() error { return a.service.MoveSoundFont(font.ID, -1) }) })
								ui.Button(c, "下移").Disabled(a.audioBusy).OnClick(func() { a.runAudioSetting(func() error { return a.service.MoveSoundFont(font.ID, 1) }) })
								ui.Button(c, "从列表移除").Disabled(a.audioBusy).OnClick(func() { a.runAudioSetting(func() error { return a.service.RemoveSoundFont(font.ID) }) })
							})
						})
					}
					ui.Text(c, "打包音色："+a.fonts.Bundled.Name+" · "+a.fonts.Bundled.Error)
					ui.Text(c, "基础波形始终作为最后兜底。").FontSize(11)
					ui.Button(c, "使用打包默认音色").Disabled(a.audioBusy).OnClick(func() { a.runAudioSetting(func() error { return a.service.SetDefaultSoundFont("") }) })
				})
			})
			ui.Button(c, "返回工作区").Disabled(a.audioBusy).OnClick(func() { a.showAudio = false })
		})
	})
}
func (a *Workspace) runAudioSetting(run func() error) {
	if a.audioBusy || a.closeFrozen {
		return
	}
	a.audioPending++
	a.audioBusy = true
	go func() {
		err := run()
		status := a.service.PlaybackStatus()
		fonts := a.service.SoundFonts()
		a.update(func() {
			a.audioPending--
			a.audioBusy = a.audioPending > 0
			a.audio = status
			a.fonts = fonts
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = "音频设置已更新"
			}
		})
	}()
}
func (a *Workspace) openAudioSettings() {
	if a.service == nil || a.closeFrozen {
		return
	}
	a.showAudio = true
	go func() { fonts := a.service.SoundFonts(); a.update(func() { a.fonts = fonts }) }()
}
