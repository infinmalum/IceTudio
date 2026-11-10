package desktop

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func (a *Workspace) mixerRack(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	m, s := f.values.Mixer, &f.mixer
	track := ch.ID != project.MasterChannel && ch.BusName == ""
	a.rackSignalPath(c, m, ch, track)
	rack := ch.Rack.Copy()
	if rack == nil {
		ui.Button(c, "添加 MIDI / 宏控制").OnClick(func() {
			ch.Rack = &project.MixRack{Velocity: 1}
			m.SetChannel(ch)
			changed()
		})
	} else {
		save := func() { ch.Rack = rack; m.SetChannel(ch); changed() }
		if track {
			transpose := float64(rack.Transpose)
			mixSlider(c, "移调（半音）", &transpose, -48, 48, func() { rack.Transpose = int(math.Round(transpose)); save() })
			mixSlider(c, "力度比例", &rack.Velocity, .25, 2, save)
		}
		a.rackMacros(c, f, ch, rack, save)
		ui.Button(c, "清除 MIDI / 宏控制").OnClick(func() {
			ch.Rack = nil
			m.SetChannel(ch)
			changed()
		})
	}
	ui.Row(c).Gap(6).Children(func() {
		ui.TextInput(c, &s.rackName).Label("组合预设名称").Grow(1)
		name := strings.TrimSpace(s.rackName)
		i := slices.IndexFunc(m.RackPresets, func(p project.RackPreset) bool { return p.Name == name })
		ui.Button(c, "保存此组合").Disabled(name == "" || (i < 0 && len(m.RackPresets) >= 16)).OnClick(func() {
			p := ch.SaveRack(name)
			m.RackPresets = slices.Clone(m.RackPresets)
			if i >= 0 {
				m.RackPresets[i] = p
			} else {
				m.RackPresets = append(m.RackPresets, p)
			}
			changed()
		})
	})
	var presets []mixChoice
	for _, p := range m.RackPresets {
		presets = append(presets, mixChoice{p.Name, p.Name})
	}
	if len(presets) == 0 {
		return
	}
	ui.Row(c).Gap(6).Children(func() {
		mixSelect(c, "组合预设", &s.rackPreset, presets)
		i := slices.IndexFunc(m.RackPresets, func(p project.RackPreset) bool { return p.Name == s.rackPreset })
		empty := ch.Instrument == nil && ch.Synth == nil && len(ch.Effects) == 0 && ch.Rack == nil && ch.FrozenAssetID == ""
		ui.Button(c, "载入到空通道").Disabled(i < 0 || !empty).OnClick(func() {
			next, err := ch.LoadRack(m.RackPresets[i])
			if err != nil {
				f.err = err.Error()
				return
			}
			if !track && (next.Instrument != nil || next.Synth != nil ||
				(next.Rack != nil && (next.Rack.Transpose != 0 || next.Rack.Velocity != 1))) {
				f.err = "带 MIDI 节点或乐器的组合只能载入轨道"
				return
			}
			m.SetChannel(next)
			changed()
		})
		ui.Button(c, "删除预设").Disabled(i < 0).OnClick(func() {
			m.RackPresets = slices.Delete(m.RackPresets, i, i+1)
			s.rackPreset = ""
			changed()
		})
	})
}

func (a *Workspace) rackSignalPath(c *ui.Context, m *project.Mixer, ch project.MixChannel, track bool) {
	_, edges, err := project.MixOrder(&a.active.State, *m)
	if err != nil {
		ui.Text(c, err.Error()).TextColor(c.Theme().Warning)
	}
	names := map[string]string{}
	for _, choice := range mixerChoices(&a.active.State, m) {
		names[choice.id] = choice.name
	}
	var nodes []string
	if ch.FrozenAssetID != "" {
		nodes = append(nodes, "冻结音频")
	} else if track {
		nodes = append(nodes, "MIDI / 轨道音频")
		if ch.Rack != nil {
			nodes = append(nodes, fmt.Sprintf("MIDI 处理 · %+d 半音 / 力度 ×%.2f", ch.Rack.Transpose, ch.Rack.Velocity))
		}
		switch {
		case ch.Instrument != nil:
			nodes = append(nodes, effectTitle(*ch.Instrument))
		case ch.Synth != nil:
			p, _ := project.FindSoundPreset(ch.Synth.Preset)
			nodes = append(nodes, p.Name)
		default:
			nodes = append(nodes, "轨道原音源")
		}
	}
	if ch.FrozenAssetID == "" {
		for _, fx := range ch.Effects {
			label := effectTitle(fx)
			if fx.Bypass {
				label += " · 旁通"
			}
			nodes = append(nodes, label)
		}
	}
	nodes = append(nodes, "推子 / 声像")
	for _, edge := range edges {
		if edge.From == ch.ID {
			nodes = append(nodes, "↳ "+names[edge.To]+" · "+routeKindName(edge.Kind))
		}
	}
	ui.Column(c).Gap(2).Children(func() {
		for i, label := range nodes {
			if i > 0 && !strings.HasPrefix(label, "↳") {
				ui.Text(c, "↓").FontSize(11).TextColor(c.Theme().Accent)
			}
			ui.Text(c, label).FontSize(12).Padding(3, 9).Background(c.Theme().SurfaceHover)
		}
	})
}

func routeKindName(kind string) string {
	switch kind {
	case "output":
		return "主输出"
	case "send":
		return "发送"
	default:
		return "侧链"
	}
}

func (a *Workspace) rackMacros(c *ui.Context, f *propertyForm, ch project.MixChannel, rack *project.MixRack, save func()) {
	labels := map[string]string{}
	bindings := map[string]project.MacroBinding{}
	var choices []mixChoice
	add := func(target, param, label string) {
		id := target + "/" + param
		choices = append(choices, mixChoice{id, label})
		bindings[id] = project.MacroBinding{Target: target, Parameter: param, Max: 1}
		labels[id] = label
	}
	if ch.Synth != nil {
		add(project.SynthMacroTarget, "tone", "内置音色 · 音色")
		add(project.SynthMacroTarget, "release", "内置音色 · 释放")
	}
	fx := slices.Clone(ch.Effects)
	if ch.Instrument != nil {
		fx = append(fx, *ch.Instrument)
	}
	for _, slot := range fx {
		title := effectTitle(slot)
		if ch.Instrument == nil || ch.Instrument.ID != slot.ID {
			add(slot.ID, "wet", title+" · 湿信号")
		}
		if slot.Plugin == nil {
			add(slot.ID, "amount", title+" · 强度")
			continue
		}
		for _, param := range slot.Plugin.Parameters {
			add(slot.ID, project.PluginParameterKey(param.ID), title+" · "+pluginParameterLabel(param))
		}
	}
	used := map[string]bool{}
	for _, macro := range rack.Macros {
		for _, b := range macro.Bindings {
			used[b.Target+"/"+b.Parameter] = true
		}
	}
	choices = slices.DeleteFunc(choices, func(x mixChoice) bool { return used[x.id] })
	for i := range rack.Macros {
		macro := &rack.Macros[i]
		ui.Column(c.Key(fmt.Sprintf("macro-%d", i))).Gap(5).Padding(6).Background(c.Theme().Surface).Children(func() {
			ui.Row(c).Gap(5).Children(func() {
				ui.TextInput(c, &macro.Name).Label("宏名称").OnChange(save)
				ui.Button(c, "删除宏").OnClick(func() { rack.Macros = slices.Delete(rack.Macros, i, i+1); save() })
			})
			mixSlider(c, "宏值", &macro.Value, 0, 1, save)
			for j := range macro.Bindings {
				b := &macro.Bindings[j]
				k := b.Target + "/" + b.Parameter
				ui.Column(c.Key(k)).Gap(4).Children(func() {
					ui.Row(c).Gap(5).Children(func() {
						ui.Text(c, labels[k]).Grow(1)
						ui.Button(c, "解除映射").OnClick(func() { macro.Bindings = slices.Delete(macro.Bindings, j, j+1); save() })
					})
					mixSlider(c, "范围起点", &b.Min, 0, 1, save)
					mixSlider(c, "范围终点", &b.Max, 0, 1, save)
				})
			}
			ui.Row(c).Gap(5).Children(func() {
				mixSelect(c, "映射目标", &f.mixer.rackBinding, choices)
				b, ok := bindings[f.mixer.rackBinding]
				ui.Button(c, "添加映射").Disabled(!ok || used[f.mixer.rackBinding] || len(macro.Bindings) >= 8).OnClick(func() {
					macro.Bindings = append(macro.Bindings, b)
					f.mixer.rackBinding = ""
					save()
				})
			})
		})
	}
	ui.Button(c, "新增宏").Disabled(len(rack.Macros) >= 8).OnClick(func() {
		name := "宏"
		for i := 1; slices.ContainsFunc(rack.Macros, func(m project.MixMacro) bool { return m.Name == name }); i++ {
			name = fmt.Sprintf("宏 %d", i+1)
		}
		rack.Macros = append(rack.Macros, project.MixMacro{Name: name, Value: .5})
		save()
	})
}
