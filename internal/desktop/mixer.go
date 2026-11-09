package desktop

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

type mixChoice struct{ id, name string }

func mixerChoices(st *project.State, m *project.Mixer) []mixChoice {
	out := []mixChoice{{project.MasterChannel, "Master · 主输出"}}
	for i, t := range st.Tracks {
		out = append(out, mixChoice{t.ID, fmt.Sprintf("%02d  %s", i+1, t.Name)})
	}
	for _, ch := range m.Channels {
		if ch.BusName != "" {
			out = append(out, mixChoice{ch.ID, "总线 · " + ch.BusName})
		}
	}
	return out
}

func mixSelect(c *ui.Context, label string, value *string, options []mixChoice) bool {
	p := ui.SelectBase(c.Key(label), value)
	name := "请选择"
	for _, option := range options {
		if option.id == *value {
			name = option.name
		}
	}
	p.Trigger.Label(label).Padding(5, 8).Background(c.Theme().SurfaceHover).Children(func() { ui.Text(c, name+" ▾").SingleLine() })
	p.Popup(func(panel ui.Element) {
		panel.Background(c.Theme().Surface)
		ui.Scroll(c).MaxHeight(220).Children(func() {
			for _, option := range options {
				p.Item(option.id).Padding(5, 8).Children(func() { ui.Text(c, option.name) })
			}
		})
	})
	return p.Trigger.Changed()
}

func (a *Workspace) mixerSelection() string {
	if a.mixTrack != "" && slices.ContainsFunc(a.active.State.Tracks, func(t project.Track) bool { return t.ID == a.mixTrack }) {
		return a.mixTrack
	}
	if t := a.preview.Context().Track; t.ID != "" {
		return t.ID
	}
	return project.MasterChannel
}

func (a *Workspace) mixerPane(c *ui.Context) {
	m := a.active.State.Mixer
	if m == nil {
		m = &project.Mixer{}
	}
	choices := mixerChoices(&a.active.State, m)
	ui.Column(c).MinHeight(0).Fill().Gap(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "混音台").Bold()
			ui.Button(c, "路由 / 效果 / 自动化").Disabled(!a.writable() || a.form != nil).OnClick(func() { a.openMixer(a.mixerSelection(), "通道") })
			ui.Button(c, "插件库").Disabled(a.service == nil || a.closeFrozen || a.form != nil).OnClick(a.openPluginCatalog)
			ui.Button(c, "重置峰值").Disabled(a.service == nil).OnClick(a.service.ResetMixerMeters)
			ui.Textf(c, "补偿 %d 帧 · 回调 p99 %d%% · 超时 %d / 丢块 %d",
				a.audio.MixerLatency, a.audio.CallbackP99, a.audio.Overruns, a.audio.MissedBlocks).FontSize(11).SingleLine().TextColor(c.Theme().TextMuted)
		})
		ui.Row(c).Grow(1).MinHeight(0).Gap(6).Children(func() {
			a.mixerStrip(c.Key("master"), m, choices[0], -1)
			ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Gap(5).Children(func() {
					for i, choice := range choices[1:] {
						a.mixerStrip(c.Key(choice.id), m, choice, i)
					}
				})
			})
		})
	})
}

func (a *Workspace) mixerStrip(c *ui.Context, m *project.Mixer, choice mixChoice, index int) {
	ch := m.Channel(choice.id)
	var track *project.Track
	color := c.Theme().Accent
	if index >= 0 && index < len(a.active.State.Tracks) {
		track = &a.active.State.Tracks[index]
		color = displayObjectColor("", track.Color, index)
	}
	var meter playback.Meter
	if a.audio.ProjectID == a.active.ID {
		meter = a.audio.Meters[choice.id]
	}
	output := project.MasterChannel
	for _, r := range m.Routes {
		if r.From == choice.id && r.Kind == "output" {
			output = r.To
		}
	}
	if bus := m.Channel(output); bus.BusName != "" {
		output = bus.BusName
	} else {
		output = "Master"
	}
	bg := c.Theme().Surface
	if a.mixerSelection() == choice.id {
		bg = c.Theme().SurfaceHover
	}
	ui.Column(c).Width(132).Gap(4).Padding(6).Background(bg).Children(func() {
		ui.Box(c).Height(3).FillWidth().Background(color)
		ui.Button(c, choice.name).Label("编辑混音 " + choice.name).FillWidth().Disabled(!a.writable() || a.form != nil).OnClick(func() {
			if track != nil {
				a.focusMixerTrack(*track)
			}
			a.openMixer(choice.id, "通道")
		})
		if choice.id != project.MasterChannel {
			ui.Text(c, "→ "+output).SingleLine().FontSize(11)
		}
		ui.Box(c).FillWidth().Height(9).Background(c.Theme().Border).Draw(func(p *ui.Painter, r ui.Rect) {
			level := func(v float64) float32 { return float32(max(0, min(1, (60+20*math.Log10(max(v, 0.001)))/60))) }
			meterColor := color
			if meter.Hold > 1 {
				meterColor = c.Theme().Warning
			}
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * level(meter.Peak), H: r.H}, meterColor, 0)
			x := r.X + r.W*level(meter.Hold)
			p.Line(x, r.Y, x, r.Y+r.H, 1, c.Theme().Text)
		})
		if meter.Hold > 0 {
			ui.Textf(c, "%.1f dBFS · 削波 %d", 20*math.Log10(meter.Hold), meter.Clips).FontSize(10)
		} else {
			ui.Text(c, "−∞ dBFS").FontSize(10)
		}
		ui.Textf(c, "推子 %.0f%% · 声像 %+.0f", ch.Gain*100, ch.Pan*100).FontSize(11)
		ui.Button(c, fmt.Sprintf("%d 个插入效果", len(ch.Effects))).Disabled(!a.writable() || a.form != nil).OnClick(func() { a.openMixer(choice.id, "效果") })
		if track != nil {
			ui.Row(c).Gap(4).Children(func() {
				mute, solo := !track.Muted, !track.Solo
				ui.Button(c, mixToggle("M", track.Muted)).Label("静音 " + choice.name).Disabled(!a.writable() || a.form != nil).OnClick(func() {
					a.mutate("edit", []project.Op{{Kind: "track.update", TrackID: track.ID, TrackPatch: &project.TrackPatch{Muted: &mute}}})
				})
				ui.Button(c, mixToggle("S", track.Solo)).Label("独奏 " + choice.name).Disabled(!a.writable() || a.form != nil).OnClick(func() {
					a.mutate("edit", []project.Op{{Kind: "track.update", TrackID: track.ID, TrackPatch: &project.TrackPatch{Solo: &solo}}})
				})
			})
		}
	})
}

func mixToggle(name string, set bool) string {
	if set {
		return name + " ●"
	}
	return name
}

func (a *Workspace) focusMixerTrack(t project.Track) {
	a.mixTrack = t.ID
	if len(t.Clips) > 0 && a.preview.Context().Track.ID != t.ID {
		a.selectClip(t, t.Clips[0])
	}
}

type mixerControls struct {
	tab, channel, routeKind, routeTarget, effect, preset, presetName, busName string
	parameter, target, position, pointValue, offset, mode                     string
	step                                                                      bool
	live                                                                      float64
	write                                                                     *mixWrite
}
type mixWrite struct {
	target, parameter, mode string
	start, last, loops      int64
	value, base             float64
	original                []project.AutomationPoint
}

func (a *Workspace) openMixer(channel, tab string) {
	a.beginProperties("mixer", project.Track{}, project.Clip{})
	if f := a.form; f != nil && f.kind == "mixer" && f.values.Mixer == nil {
		m := project.Mixer{}
		if a.active.State.Mixer != nil {
			m = a.active.State.Mixer.Copy()
		}
		f.values.Mixer = &m
		f.mixer = mixerControls{channel: channel, tab: tab}
	}
}

func (a *Workspace) mixerFields(c *ui.Context, f *propertyForm) {
	m := f.values.Mixer
	if m == nil {
		ui.Text(c, "混音草稿缺少快照")
		return
	}
	s := &f.mixer
	if s.channel == "" {
		s.channel = project.MasterChannel
	}
	if s.tab == "" {
		s.tab = "通道"
	}
	ui.Row(c).Gap(6).Children(func() {
		if s.write == nil && mixSelect(c, "混音通道", &s.channel, mixerChoices(&a.active.State, m)) {
			s.effect, s.target = "", ""
		}
		ui.Select(c, &s.tab, []string{"通道", "路由", "效果", "自动化"}).Label("混音页面").Disabled(s.write != nil)
	})
	ch := m.Channel(s.channel)
	changed := func() { a.changedForm(f) }
	ui.Scroll(c).FillWidth().MaxHeight(390).Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			switch s.tab {
			case "通道":
				mixSlider(c, "推子", &ch.Gain, 0, 2, func() { m.SetChannel(ch); changed() })
				mixSlider(c, "声像", &ch.Pan, -1, 1, func() { m.SetChannel(ch); changed() })
				if ch.BusName != "" {
					ui.TextInput(c, &ch.BusName).Label("总线名称").OnChange(func() {
						if strings.TrimSpace(ch.BusName) != "" {
							m.SetChannel(ch)
							changed()
						}
					})
					ui.Button(c, "移除总线及关联路由 / 曲线").OnClick(func() { m.RemoveChannel(ch.ID); s.channel = project.MasterChannel; changed() })
				}
				ui.Row(c).Gap(6).Children(func() {
					ui.TextInput(c, &s.busName).Grow(1).Label("新总线名称")
					ui.Button(c, "新增总线").OnClick(func() {
						name := strings.TrimSpace(s.busName)
						if name == "" {
							name = "总线"
						}
						s.channel = project.NewID("bus")
						m.SetChannel(project.MixChannel{ID: s.channel, Gain: 1, BusName: name})
						s.busName = ""
						changed()
					})
				})
			case "路由":
				a.mixerRoutes(c, f, ch, changed)
			case "效果":
				a.mixerEffects(c, f, ch, changed)
			case "自动化":
				a.mixerAutomation(c, f, ch, changed)
			}
		})
	})
}

func mixSlider(c *ui.Context, label string, value *float64, lo, hi float64, changed func()) {
	v := *value
	ui.Row(c.Key(label)).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Textf(c, "%s %.2f", label, v).Width(108)
		ui.Slider(c, &v, lo, hi).Label(label).Grow(1).OnChange(func() {
			*value = math.Round(float64(v)*1000) / 1000
			changed()
		})
	})
}

func (a *Workspace) mixerRoutes(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	s, m := &f.mixer, f.values.Mixer
	choices := mixerChoices(&a.active.State, m)
	name := func(id string) string {
		for _, p := range choices {
			if p.id == id {
				return p.name
			}
		}
		return id
	}
	ui.Text(c, "主输出默认到 Master。发送取推子后信号，侧链只控制目标压缩器。").FontSize(11).MaxWidth(390)
	for i := range m.Routes {
		r := &m.Routes[i]
		if r.From != ch.ID && r.To != ch.ID {
			continue
		}
		ui.Column(c.Key(r.From + "/" + r.To + "/" + r.Kind)).Gap(3).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Textf(c, "%s → %s · %s", name(r.From), name(r.To), routeName(r.Kind)).Grow(1).SingleLine()
				ui.Button(c, "移除").OnClick(func() { m.Routes = slices.Delete(m.Routes, i, i+1); changed() })
			})
			if r.Kind != "output" {
				mixSlider(c, "路由增益", &r.Gain, 0, 2, changed)
			}
		})
	}
	if ch.ID == project.MasterChannel {
		return
	}
	if s.routeKind == "" {
		s.routeKind = "output"
	}
	mixSelect(c, "路由类型", &s.routeKind, []mixChoice{{"output", "主输出"}, {"send", "发送"}, {"sidechain", "侧链"}})
	targets := []mixChoice{}
	for _, item := range choices {
		if item.id == ch.ID {
			continue
		}
		dst := m.Channel(item.id)
		bus := dst.BusName != ""
		if (s.routeKind == "output" && (bus || item.id == project.MasterChannel)) ||
			(s.routeKind == "send" && bus) ||
			(s.routeKind == "sidechain" && slices.ContainsFunc(dst.Effects, func(f project.MixEffect) bool { return f.Kind == "compressor" })) {
			targets = append(targets, item)
		}
	}
	mixSelect(c, "路由目标", &s.routeTarget, targets)
	ui.Button(c, "设置路由").Disabled(!slices.ContainsFunc(targets, func(p mixChoice) bool { return p.id == s.routeTarget })).OnClick(func() {
		next := m.Copy()
		next.Routes = slices.DeleteFunc(next.Routes, func(r project.MixRoute) bool {
			return r.Kind == s.routeKind && ((r.From == ch.ID && (r.Kind == "output" || r.To == s.routeTarget)) || (r.Kind == "sidechain" && r.To == s.routeTarget))
		})
		if !(s.routeKind == "output" && s.routeTarget == project.MasterChannel) {
			next.Routes = append(next.Routes, project.MixRoute{From: ch.ID, To: s.routeTarget, Kind: s.routeKind, Gain: 1})
		}
		if _, _, err := project.MixOrder(&a.active.State, next); err != nil {
			f.err = err.Error()
			return
		}
		*m = next
		changed()
	})
}

func routeName(kind string) string {
	switch kind {
	case "output":
		return "主输出"
	case "send":
		return "发送"
	default:
		return "侧链"
	}
}

var mixEffects = []mixChoice{
	{"eq", "倾斜均衡"}, {"compressor", "压缩器"}, {"limiter", "限制器"}, {"delay", "延迟"}, {"reverb", "混响"},
	{"chorus", "立体声合唱"}, {"lowpass", "低通滤波"}, {"drive", "软削波失真"}, {"gate", "噪声门"},
}

func mixEffectName(kind string) string {
	for _, item := range mixEffects {
		if item.id == kind {
			return item.name
		}
	}
	return kind
}

func (a *Workspace) mixerEffects(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	s, m := &f.mixer, f.values.Mixer
	ch.Effects = slices.Clone(ch.Effects)
	for i := range ch.Effects {
		fx := &ch.Effects[i]
		save := func() { m.SetChannel(ch); changed() }
		ui.Column(c.Key(fx.ID)).FillWidth().Gap(4).Padding(6).Background(c.Theme().SurfaceHover).Children(func() {
			ui.Row(c).Gap(4).Children(func() {
				ui.Text(c, mixEffectName(fx.Kind)).Grow(1).Bold()
				ui.Checkbox(c, &fx.Bypass, "旁通").OnChange(save)
				ui.Button(c, "↑").Disabled(i == 0).OnClick(func() { ch.Effects[i], ch.Effects[i-1] = ch.Effects[i-1], ch.Effects[i]; save() })
				ui.Button(c, "↓").Disabled(i+1 == len(ch.Effects)).OnClick(func() { ch.Effects[i], ch.Effects[i+1] = ch.Effects[i+1], ch.Effects[i]; save() })
				ui.Button(c, "移除").OnClick(func() {
					id := fx.ID
					ch.Effects = slices.Delete(ch.Effects, i, i+1)
					m.Lanes = slices.DeleteFunc(m.Lanes, func(l project.Automation) bool { return l.Target == id })
					if !slices.ContainsFunc(ch.Effects, func(e project.MixEffect) bool { return e.Kind == "compressor" }) {
						m.Routes = slices.DeleteFunc(m.Routes, func(r project.MixRoute) bool { return r.Kind == "sidechain" && r.To == ch.ID })
					}
					save()
				})
			})
			amountLabel := "强度"
			switch fx.Kind {
			case "chorus":
				amountLabel = "调制深度"
			case "lowpass":
				amountLabel = "截止频率（归一化）"
			case "drive":
				amountLabel = "驱动量"
			case "gate":
				amountLabel = "阈值（归一化）"
			}
			mixSlider(c, amountLabel, &fx.Amount, 0, 1, save)
			mixSlider(c, "湿信号", &fx.Wet, 0, 1, save)
			ui.Button(c, "将此效果存为预设").Disabled(strings.TrimSpace(s.presetName) == "" || len(m.Presets) >= 32).OnClick(func() {
				p := project.MixPreset{Name: strings.TrimSpace(s.presetName), Effect: *fx}
				p.Effect.ID = ""
				m.Presets = slices.DeleteFunc(m.Presets, func(old project.MixPreset) bool { return old.Name == p.Name })
				m.Presets = append(m.Presets, p)
				changed()
			})
		})
	}
	ui.TextInput(c, &s.presetName).Label("预设名称").Placeholder("命名后可保存效果")
	ui.Row(c).Gap(6).Children(func() {
		mixSelect(c, "效果类型", &s.effect, mixEffects)
		ui.Button(c, "添加效果").Disabled(s.effect == "" || len(ch.Effects) >= 8).OnClick(func() {
			wet := 1.0
			if s.effect == "delay" || s.effect == "reverb" || s.effect == "chorus" {
				wet = .3
			}
			ch.Effects = append(ch.Effects, project.MixEffect{ID: project.NewID("fx"), Kind: s.effect, Amount: .5, Wet: wet})
			m.SetChannel(ch)
			changed()
		})
	})
	if len(m.Presets) > 0 {
		var presets []mixChoice
		for _, p := range m.Presets {
			presets = append(presets, mixChoice{p.Name, p.Name})
		}
		ui.Row(c).Gap(6).Children(func() {
			mixSelect(c, "工程效果预设", &s.preset, presets)
			ui.Button(c, "载入新槽").Disabled(s.preset == "" || len(ch.Effects) >= 8).OnClick(func() {
				for _, p := range m.Presets {
					if p.Name != s.preset {
						continue
					}
					fx := p.Effect
					fx.ID = project.NewID("fx")
					ch.Effects = append(ch.Effects, fx)
					m.SetChannel(ch)
					changed()
					break
				}
			})
			ui.Button(c, "删除预设").Disabled(s.preset == "").OnClick(func() {
				m.Presets = slices.DeleteFunc(m.Presets, func(p project.MixPreset) bool { return p.Name == s.preset })
				s.preset = ""
				changed()
			})
		})
	}
}
