package desktop

import (
	"context"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func (a *Workspace) mixerSounds(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	m, s := f.values.Mixer, &f.mixer
	if !slices.ContainsFunc(a.active.State.Tracks, func(t project.Track) bool { return t.ID == ch.ID }) {
		ui.Text(c, "请选择 MIDI 轨道以使用内置音色。")
		return
	}
	if ch.Instrument != nil {
		ui.Text(c, "此轨正在使用 "+ch.Instrument.Plugin.Name+"。卸载插件乐器后可选择内置音色。")
		return
	}
	ui.TextInput(c, &s.soundQuery).Label("搜索音色").Placeholder("名称 / 合成类别")
	ui.Checkbox(c, &s.soundFavorites, "只显示收藏")
	query := strings.ToLower(strings.TrimSpace(s.soundQuery))
	for _, preset := range project.SoundPresets() {
		favorite := slices.Contains(m.FavoriteSounds, preset.ID)
		if s.soundFavorites && !favorite || !strings.Contains(strings.ToLower(preset.Name+" "+preset.Category), query) {
			continue
		}
		patch := project.SynthPatch{Preset: preset.ID, Tone: preset.Tone, Release: preset.Release}
		if ch.Synth != nil && ch.Synth.Preset == preset.ID {
			patch = *ch.Synth
		}
		ui.Row(c.Key(preset.ID)).Gap(6).Children(func() {
			ui.Text(c, preset.Name+" · "+preset.Category).Grow(1)
			label := "收藏"
			if favorite {
				label = "取消收藏"
			}
			ui.Button(c, label).OnClick(func() {
				if favorite {
					m.FavoriteSounds = slices.DeleteFunc(m.FavoriteSounds, func(id string) bool { return id == preset.ID })
				} else {
					m.FavoriteSounds = append(m.FavoriteSounds, preset.ID)
				}
				changed()
			})
			ui.Button(c, "试听").OnClick(func() {
				go func() {
					err := a.service.PreviewSynth(context.Background(), patch)
					if err != nil && err != context.Canceled {
						a.update(func() {
							if a.form == f {
								f.err = err.Error()
							}
						})
					}
				}()
			})
			ui.Button(c, "加载音色").Disabled(ch.Synth != nil && ch.Synth.Preset == preset.ID).OnClick(func() {
				ch.Synth = &patch
				m.SetChannel(ch)
				changed()
			})
		})
	}
	ui.Button(c, "停止试听").OnClick(a.stopMaterialPreview)
	if ch.Synth != nil {
		patch := *ch.Synth
		save := func() { ch.Synth = &patch; m.SetChannel(ch); changed() }
		mixSlider(c, "音色", &patch.Tone, 0, 1, save)
		mixSlider(c, "释放", &patch.Release, 0, 1, save)
		ui.Button(c, "恢复轨道原音源").OnClick(func() { ch.Synth = nil; ch.RemoveMacroTarget(project.SynthMacroTarget); m.SetChannel(ch); changed() })
	}
}
