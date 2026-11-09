package desktop

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func setAutomationPoint(points []project.AutomationPoint, p project.AutomationPoint) []project.AutomationPoint {
	i, found := slices.BinarySearchFunc(points, p, func(a, b project.AutomationPoint) int { return int(a.Tick - b.Tick) })
	if found {
		points[i] = p
		return points
	}
	return slices.Insert(points, i, p)
}

func automationLane(m *project.Mixer, target, param string) int {
	return slices.IndexFunc(m.Lanes, func(l project.Automation) bool { return l.Target == target && l.Parameter == param })
}

func putAutomation(m *project.Mixer, target, param string, point project.AutomationPoint) {
	i := automationLane(m, target, param)
	if i < 0 {
		m.Lanes = append(m.Lanes, project.Automation{ID: project.NewID("lane"), Target: target, Parameter: param, Points: []project.AutomationPoint{point}})
		return
	}
	m.Lanes[i].Points = setAutomationPoint(m.Lanes[i].Points, point)
}

func (a *Workspace) mixerAutomation(c *ui.Context, f *propertyForm, ch project.MixChannel, changed func()) {
	s, m := &f.mixer, f.values.Mixer
	targets := []mixChoice{{ch.ID, "通道推子 / 声像"}}
	for i, fx := range ch.Effects {
		targets = append(targets, mixChoice{fx.ID, fmt.Sprintf("%d · %s", i+1, mixEffectName(fx.Kind))})
	}
	if !slices.ContainsFunc(targets, func(p mixChoice) bool { return p.id == s.target }) {
		s.target = ch.ID
		s.parameter = ""
	}
	ui.Box(c).Disabled(s.write != nil).Children(func() {
		if mixSelect(c, "自动化对象", &s.target, targets) {
			s.parameter = ""
		}
		params := []mixChoice{{"gain", "推子"}, {"pan", "声像"}}
		if s.target != ch.ID {
			params = []mixChoice{{"amount", "效果强度"}, {"wet", "湿信号"}}
		}
		if s.parameter == "" {
			s.parameter = params[0].id
		}
		mixSelect(c, "参数", &s.parameter, params)
	})
	if s.parameter == "" {
		return
	}
	lo, hi, _ := project.MixRange(s.parameter)
	i := automationLane(m, s.target, s.parameter)
	var points []project.AutomationPoint
	if i >= 0 {
		points = m.Lanes[i].Points
	}
	ui.Text(c, "读取 · 首节点前使用面板值，末节点后保持。点击曲线新增节点，或输入精确位置。").FontSize(11).MaxWidth(400)
	extent := max(int64(a.active.PPQ)*4, a.audio.EndTick)
	if len(points) > 0 {
		extent = max(extent, points[len(points)-1].Tick)
	}
	tickValue := func(tick int64, value float64) {
		putAutomation(m, s.target, s.parameter, project.AutomationPoint{Tick: tick, Value: value, Step: s.step})
		changed()
	}
	graph := ui.Box(c).Height(92).FillWidth().Clip().Background(c.Theme().SurfaceHover)
	graph.Draw(func(p *ui.Painter, r ui.Rect) {
		x := func(t int64) float32 { return r.X + r.W*float32(float64(t)/float64(extent)) }
		y := func(v float64) float32 { return r.Y + 5 + (r.H-10)*(1-float32((v-lo)/(hi-lo))) }
		for j := 1; j < 8; j++ {
			xx := r.X + r.W*float32(j)/8
			p.Line(xx, r.Y, xx, r.Y+r.H, 1, c.Theme().Border)
		}
		for j, pt := range points {
			px, py := x(pt.Tick), y(pt.Value)
			if j > 0 {
				prev := points[j-1]
				if prev.Step {
					p.Line(x(prev.Tick), y(prev.Value), px, y(prev.Value), 1, c.Theme().Accent)
					p.Line(px, y(prev.Value), px, py, 1, c.Theme().Accent)
				} else {
					p.Line(x(prev.Tick), y(prev.Value), px, py, 1, c.Theme().Accent)
				}
			}
			p.Fill(ui.Rect{X: px - 2, Y: py - 2, W: 4, H: 4}, c.Theme().Accent, 1)
		}
		if a.audio.ProjectID == a.active.ID && a.audio.Playing {
			px := x(int64(a.audio.Tick))
			p.Line(px, r.Y, px, r.Y+r.H, 1, c.Theme().Warning)
		}
	}).HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputPointerDown || ev.Button != 0 || s.write != nil || f.applying || a.closeFrozen {
			return false
		}
		r := graph.Bounds()
		if r.W <= 0 || r.H <= 10 {
			return false
		}
		tick := int64(math.Round(float64(max(0, min(1, ev.X/r.W))) * float64(extent)))
		value := hi - (hi-lo)*float64(max(0, min(1, (ev.Y-5)/(r.H-10))))
		tickValue(tick, math.Round(value*1000)/1000)
		c.Invalidate()
		return true
	})
	ui.Box(c).Disabled(s.write != nil).Children(func() {
		ui.Column(c).Gap(5).Children(func() {
			ui.Row(c).Gap(5).Children(func() {
				ui.TextInput(c, &s.position).Label("节点位置").Placeholder("小节:拍:tick 或 tick").Grow(1)
				ui.TextInput(c, &s.pointValue).Label("节点值").Placeholder(fmt.Sprintf("%.0f … %.0f", lo, hi)).Width(90)
				ui.Checkbox(c, &s.step, "阶梯")
			})
			ui.Row(c).Gap(5).Children(func() {
				ui.Button(c, "写入 / 更新节点").OnClick(func() {
					tick, err := parseMusicTime(f.meter, s.position)
					val, e := strconv.ParseFloat(s.pointValue, 64)
					if err != nil || tick > project.MaxTick || e != nil || math.IsNaN(val) || val < lo || val > hi {
						f.err = "请输入有效位置及参数范围内的值"
						return
					}
					tickValue(tick, val)
				})
				ui.Button(c, "删除该位置节点").Disabled(i < 0).OnClick(func() {
					tick, err := parseMusicTime(f.meter, s.position)
					if err != nil {
						f.err = err.Error()
						return
					}
					m.Lanes[i].Points = slices.DeleteFunc(m.Lanes[i].Points, func(p project.AutomationPoint) bool { return p.Tick == tick })
					if len(m.Lanes[i].Points) == 0 {
						m.Lanes = slices.Delete(m.Lanes, i, i+1)
					}
					changed()
				})
			})
			if i >= 0 {
				ui.Row(c).Gap(5).Children(func() {
					ui.TextInput(c, &s.offset).Label("曲线复制偏移").Placeholder("正整数 tick").Width(126)
					ui.Button(c, "偏移复制节点").OnClick(func() {
						delta, err := strconv.ParseInt(s.offset, 10, 64)
						if err != nil || delta < 1 || delta > project.MaxTick-points[len(points)-1].Tick {
							f.err = "复制偏移必须为有效正整数 tick"
							return
						}
						out := slices.Clone(points)
						for _, p := range points {
							p.Tick += delta
							out = setAutomationPoint(out, p)
						}
						m.Lanes[i].Points = out
						changed()
					})
					ui.Button(c, "删除曲线").OnClick(func() { m.Lanes = slices.Delete(m.Lanes, i, i+1); changed() })
				})
			}
		})
	})
	a.mixerWriteFields(c, f, lo, hi)
}

func (a *Workspace) mixerWriteFields(c *ui.Context, f *propertyForm, lo, hi float64) {
	s := &f.mixer
	if s.mode == "" {
		s.mode = "触碰"
	}
	ui.Text(c, "实时写入 · 播放当前修订后启用。触碰在释放时恢复读取，锁定保持至结束写入。").FontSize(11).MaxWidth(400)
	ui.Row(c).Gap(5).Children(func() {
		ui.Select(c, &s.mode, []string{"触碰", "锁定"}).Disabled(s.write != nil).Label("自动化写入模式")
		status := a.audio
		if s.write == nil {
			ui.Button(c, "开始写入").Disabled(!status.Playing || status.CountIn || status.ProjectID != f.entry.Target.ID || status.Revision != f.entry.Target.Revision || status.PreviewID != "").OnClick(func() {
				s.write = &mixWrite{target: s.target, parameter: s.parameter, mode: s.mode, start: -1, last: int64(status.Tick), loops: status.Loops}
				w := s.write
				w.base = mixerParameter(mixerState(a.active.State.Mixer), w.target, w.parameter)
				active := mixerState(a.active.State.Mixer)
				if idx := automationLane(active, w.target, w.parameter); idx >= 0 {
					w.original = slices.Clone(active.Lanes[idx].Points)
				}
				s.live = automationValue(w.original, w.base, int64(status.Tick))
			})
		} else {
			label := "释放并停止写入"
			if s.write.mode == "锁定" {
				label = "结束锁定写入"
			}
			ui.Button(c, label).OnClick(func() { a.finishMixerWrite(f) })
		}
	})
	if s.write == nil {
		return
	}
	slider := ui.Slider(c, &s.live, lo, hi).Label("实时自动化推子").FillWidth()
	record := func() {
		w := s.write
		if w == nil {
			return
		}
		status := a.service.PlaybackStatus()
		if !a.validMixerWrite(f, status) {
			a.finishMixerWrite(f)
			return
		}
		if err := a.service.SetMixerOverride(f.entry.Target.ID, f.entry.Target.Revision, w.target, w.parameter, s.live); err != nil {
			f.err = err.Error()
			return
		}
		tick := int64(status.Tick)
		w.value = s.live
		if w.start < 0 {
			w.start, w.last = tick, tick
			if tick > 0 {
				v, _ := automationAt(w.original, w.base, tick-1)
				putAutomation(f.values.Mixer, w.target, w.parameter, project.AutomationPoint{Tick: tick - 1, Value: v, Step: true})
			}
		}
		// From the first moved sample through release, existing points are
		// replaced as the write head crosses them. Points beyond it survive.
		a.captureMixerPoint(f, tick)
	}
	slider.OnChange(record)
	if s.write.mode == "触碰" {
		slider.HandleInput(func(ev ui.InputEvent) bool {
			if s.write == nil {
				return false
			}
			switch ev.Kind {
			case ui.InputPointerDown, ui.InputPointerMove:
				if ev.Button != 0 {
					return false
				}
				w := slider.Bounds().W
				if w > 0 {
					s.live = lo + (hi-lo)*float64(max(0, min(1, ev.X/w)))
					record()
				}
				return true
			case ui.InputPointerUp:
				if ev.Button == 0 {
					a.finishMixerWrite(f)
					return true
				}
			case ui.InputKeyUp:
				switch ev.Key {
				case ui.KeyLeft, ui.KeyRight, ui.KeyUp, ui.KeyDown, ui.KeyHome, ui.KeyEnd:
					a.finishMixerWrite(f)
				}
			}
			return false
		})
	}
}

func (a *Workspace) validMixerWrite(f *propertyForm, status playback.Status) bool {
	w := f.mixer.write
	return w != nil && status.Playing && !status.CountIn && status.ProjectID == f.entry.Target.ID &&
		status.Revision == f.entry.Target.Revision && status.PreviewID == "" && status.Loops == w.loops && int64(status.Tick) >= w.last
}

func (a *Workspace) captureMixerPoint(f *propertyForm, tick int64) {
	w, m := f.mixer.write, f.values.Mixer
	if w.start < 0 {
		return
	}
	i := automationLane(m, w.target, w.parameter)
	if i >= 0 && tick > w.last {
		m.Lanes[i].Points = slices.DeleteFunc(m.Lanes[i].Points, func(p project.AutomationPoint) bool { return p.Tick > w.last && p.Tick <= tick })
	}
	putAutomation(m, w.target, w.parameter, project.AutomationPoint{Tick: tick, Value: w.value, Step: true})
	w.last = tick
	a.changedForm(f)
}

func (a *Workspace) finishMixerWrite(f *propertyForm) {
	w := f.mixer.write
	if w == nil {
		return
	}
	if a.service != nil {
		status := a.service.PlaybackStatus()
		if w.start >= 0 {
			if a.validMixerWrite(f, status) && int64(status.Tick) > w.last {
				a.captureMixerPoint(f, int64(status.Tick))
			}
			// Resume the pre-write curve immediately after the recorded range. The
			// next original node remains intact, including its interpolation.
			if end := w.last + 1; end <= project.MaxTick {
				v, step := automationAt(w.original, w.base, end)
				putAutomation(f.values.Mixer, w.target, w.parameter, project.AutomationPoint{Tick: end, Value: v, Step: step})
				a.changedForm(f)
			}
		}
		a.service.ClearMixerOverride()
	}
	f.mixer.write = nil
}

func mixerState(m *project.Mixer) *project.Mixer {
	if m == nil {
		return &project.Mixer{}
	}
	return m
}

func mixerParameter(m *project.Mixer, target, param string) float64 {
	switch param {
	case "gain":
		return m.Channel(target).Gain
	case "pan":
		return m.Channel(target).Pan
	}
	for _, ch := range m.Channels {
		for _, fx := range ch.Effects {
			if fx.ID == target {
				if param == "wet" {
					return fx.Wet
				}
				return fx.Amount
			}
		}
	}
	return 0
}

func automationValue(points []project.AutomationPoint, base float64, tick int64) float64 {
	value, _ := automationAt(points, base, tick)
	return value
}

func automationAt(points []project.AutomationPoint, base float64, tick int64) (float64, bool) {
	i, exact := slices.BinarySearchFunc(points, project.AutomationPoint{Tick: tick}, func(a, b project.AutomationPoint) int { return int(a.Tick - b.Tick) })
	if exact {
		return points[i].Value, points[i].Step
	}
	if i == 0 {
		return base, true
	}
	prev := points[i-1]
	if i == len(points) || prev.Step {
		return prev.Value, true
	}
	next := points[i]
	return prev.Value + (next.Value-prev.Value)*float64(tick-prev.Tick)/float64(next.Tick-prev.Tick), false
}
