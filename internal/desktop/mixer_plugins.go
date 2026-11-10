package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/pluginhost"
	"icetudio/internal/project"
)

func effectTitle(f project.MixEffect) string {
	if f.Plugin != nil {
		return f.Plugin.Name + " · " + strings.ToUpper(f.Plugin.Format)
	}
	return mixEffectName(f.Kind)
}

func editableEffect(f project.MixEffect) project.MixEffect {
	if f.Plugin != nil {
		p := *f.Plugin
		p.Parameters = slices.Clone(p.Parameters)
		f.Plugin = &p
	}
	return f
}

func cloneEffect(f project.MixEffect) project.MixEffect {
	f = editableEffect(f)
	if f.Plugin != nil {
		f.Plugin.State = slices.Clone(f.Plugin.State)
	}
	return f
}

func (a *Workspace) mixerInstrument(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	m := f.values.Mixer
	if ch.Instrument == nil {
		a.pluginPicker(c.Key("new-instrument"), f, "instrument", nil, func(p *project.PluginState) {
			current := m.Channel(ch.ID)
			current.Synth = nil
			current.RemoveMacroTarget(project.SynthMacroTarget)
			current.Instrument = &project.MixEffect{ID: project.NewID("inst"), Kind: "plugin", Wet: 1, Plugin: p}
			m.SetChannel(current)
			changed()
		})
		return
	}
	fx := editableEffect(*ch.Instrument)
	ui.Row(c).Gap(6).Children(func() {
		ui.Text(c, effectTitle(fx)).Grow(1).Bold()
		ui.Button(c, "卸载乐器").OnClick(func() {
			current := m.Channel(ch.ID)
			current.Instrument = nil
			current.RemoveMacroTarget(fx.ID)
			m.Lanes = slices.DeleteFunc(m.Lanes, func(l project.Automation) bool { return l.Target == fx.ID })
			m.SetChannel(current)
			changed()
		})
	})
	a.pluginFields(c.Key(fx.ID), f, &fx, "instrument", func() {
		current := m.Channel(ch.ID)
		current.Instrument = &fx
		m.SetChannel(current)
		changed()
	})
}

func (a *Workspace) pluginFields(c *ui.Context, f *propertyForm, slot *project.MixEffect, kind string, save func()) {
	p := slot.Plugin
	if p == nil {
		return
	}
	// IDs survive ordering and naming changes in the editor.
	var choices []mixChoice
	for _, param := range p.Parameters {
		choices = append(choices, mixChoice{project.PluginParameterKey(param.ID), pluginParameterLabel(param)})
	}
	selected := f.mixer.pluginParameter
	if mixSelect(c, "插件参数", &selected, choices) {
		f.mixer.pluginParameter = selected
	}
	for i := range p.Parameters {
		param := &p.Parameters[i]
		if project.PluginParameterKey(param.ID) == selected {
			mixSlider(c, pluginParameterLabel(*param), &param.Value, 0, 1, save)
			ui.Button(c, "恢复插件存档值").OnClick(func() { param.Value = param.Default; save() })
			break
		}
	}
	ui.Button(c, "打开插件编辑器").Disabled(f.preparing).OnClick(func() {
		a.editPluginWindow(f, *p, kind, func(edited *project.PluginState) {
			slot.Plugin = edited
			save()
		})
	})
	a.pluginPicker(c.Key("relink"), f, kind, p, func(replacement *project.PluginState) {
		slot.Plugin = replacement
		save()
	})
}

func pluginParameterLabel(p project.PluginParameter) string {
	if p.Name == "" {
		return fmt.Sprintf("参数 %d", p.ID)
	}
	return p.Name
}

func (a *Workspace) pluginPicker(c *ui.Context, f *propertyForm, kind string, previous *project.PluginState, apply func(*project.PluginState)) {
	if a.service == nil {
		return
	}
	s := &f.mixer
	if !s.pluginLoaded {
		ui.Button(c, "从本机插件库选择").Disabled(f.preparing).OnClick(func() {
			f.preparing = true
			go func() {
				items, err := a.service.PluginCatalog()
				a.update(func() {
					if a.form != f {
						return
					}
					f.preparing = false
					if err != nil {
						f.err = err.Error()
						return
					}
					s.plugins, s.pluginLoaded = items, true
				})
			}()
		})
		return
	}
	query := strings.ToLower(strings.TrimSpace(s.pluginQuery))
	var choices []mixChoice
	type entry struct {
		identity pluginhost.Identity
		name     string
	}
	entries := map[string]entry{}
	for _, module := range s.plugins {
		if module.Blocked || module.Status != pluginhost.Ready {
			continue
		}
		for _, class := range module.Classes {
			if class.Kind != kind || (previous != nil && (previous.Format != module.Format || previous.Class != class.ID || previous.SHA256 == module.SHA256)) {
				continue
			}
			text := class.Name + " · " + strings.ToUpper(module.Format) + " · " + class.Vendor + " " + class.Version
			if !strings.Contains(strings.ToLower(text), query) {
				continue
			}
			id := module.Format + "/" + module.SHA256 + "/" + class.ID
			if _, ok := entries[id]; ok {
				continue
			}
			entries[id] = entry{pluginhost.Identity{Format: module.Format, Class: class.ID, SHA256: module.SHA256}, class.Name}
			choices = append(choices, mixChoice{id, text})
		}
	}
	action := "挂载插件乐器"
	if kind == "effect" {
		action = "添加插件效果"
	}
	if previous != nil {
		action = "迁移到已扫描的新版本"
	}
	ui.TextInput(c, &s.pluginQuery).Label("筛选已扫描的插件").Placeholder("名称 / 格式 / 厂商")
	selected := s.pluginChoice
	if mixSelect(c, action, &selected, choices) {
		s.pluginChoice = selected
	}
	item, ok := entries[selected]
	ui.Button(c, action).Disabled(!ok || f.preparing).OnClick(func() {
		if ok {
			a.preparePluginDraft(f, item.identity, kind, item.name, previous, apply)
		}
	})
	if len(choices) == 0 {
		hint := "请先在音频插件库扫描兼容模块"
		if previous != nil {
			hint = "未发现同一插件的新版本。原二进制移动后，重扫新目录可恢复相同版本。"
		}
		ui.Text(c, hint).FontSize(11).TextColor(c.Theme().TextMuted)
	}
}

func (a *Workspace) preparePluginDraft(f *propertyForm, identity pluginhost.Identity, kind, name string, previous *project.PluginState, apply func(*project.PluginState)) {
	if f.preparing || a.form != f || a.closeFrozen {
		return
	}
	f.preparing = true
	version := f.version
	ctx, cancel := context.WithCancel(context.Background())
	f.cancelPrepare = cancel
	go func() {
		defer cancel()
		p, err := a.service.PreparePluginSlot(ctx, identity, kind, name, previous)
		a.update(func() {
			if a.form != f {
				return
			}
			f.preparing, f.cancelPrepare = false, nil
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					f.err = err.Error()
				}
				return
			}
			if a.closeFrozen || f.version != version || a.active.ID != f.entry.Target.ID || a.active.Revision != f.entry.Target.Revision {
				f.err = "混音草稿或工程修订已变化，请重新选择插件"
				return
			}
			f.err = ""
			apply(p)
		})
	}()
}

func (a *Workspace) mixerFreeze(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	m := f.values.Mixer
	if ch.FrozenAssetID != "" {
		ui.Text(c, "已冻结。播放使用缓存音频；解冻后使用当前源片段与插件。").FontSize(11)
		ui.Button(c, "解冻轨道").Disabled(f.preparing).OnClick(func() {
			current := m.Channel(ch.ID)
			current.FrozenAssetID = ""
			m.SetChannel(current)
			changed()
		})
		return
	}
	if ch.Instrument == nil && !slices.ContainsFunc(ch.Effects, func(fx project.MixEffect) bool { return fx.Plugin != nil && !fx.Bypass }) {
		return
	}
	ui.Button(c, "渲染并冻结轨道").Disabled(f.preparing || f.dirty).OnClick(func() {
		if f.preparing || f.dirty || a.form != f {
			return
		}
		f.preparing = true
		target := f.entry.Target
		version := f.version
		ctx, cancel := context.WithCancel(context.Background())
		f.cancelPrepare = cancel
		go func() {
			defer cancel()
			asset, err := a.service.PrepareTrackFreeze(ctx, target.ID, target.Revision, ch.ID)
			a.update(func() {
				if a.form != f {
					return
				}
				f.preparing, f.cancelPrepare = false, nil
				if err != nil {
					f.err = err.Error()
					return
				}
				if a.closeFrozen || f.version != version || a.active.ID != target.ID || a.active.Revision != target.Revision {
					f.err = "工程已变化，请重新渲染冻结"
					return
				}
				f.err = ""
				current := m.Channel(ch.ID)
				current.FrozenAssetID = asset.ID
				m.SetChannel(current)
				if f.values.FreezeAssets == nil {
					f.values.FreezeAssets = &[]project.AudioAsset{}
				}
				*f.values.FreezeAssets = append(*f.values.FreezeAssets, asset)
				changed()
			})
		}()
	})
	if f.dirty {
		ui.Text(c, "请先应用混音草稿，再生成冻结音频。").FontSize(11)
	}
}

func (a *Workspace) editPluginWindow(f *propertyForm, p project.PluginState, kind string, apply func(*project.PluginState)) {
	if a.form != f || f.preparing || a.closeFrozen {
		return
	}
	f.preparing = true
	version := f.version
	ctx, cancel := context.WithCancel(context.Background())
	f.cancelPrepare = cancel
	capture := make(chan struct{})
	go func() {
		defer cancel()
		result, err := a.service.EditPluginSlot(ctx, p, kind, capture, func() {
			a.update(func() {
				if a.form == f && ctx.Err() == nil {
					f.capturePlugin = func() { close(capture) }
				}
			})
		})
		a.update(func() {
			if a.form != f {
				return
			}
			f.preparing, f.cancelPrepare, f.capturePlugin = false, nil, nil
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					f.err = err.Error()
				}
				return
			}
			if a.closeFrozen || f.version != version || a.active.ID != f.entry.Target.ID || a.active.Revision != f.entry.Target.Revision {
				f.err = "工程修订已变化，插件窗口结果未应用"
				return
			}
			f.err = ""
			apply(result)
		})
	}()
}
