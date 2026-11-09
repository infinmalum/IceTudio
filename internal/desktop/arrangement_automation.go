package desktop

import (
	"fmt"
	"math"
	"sort"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

const automationRowHeight = 38

func trackAutomation(m *project.Mixer, id string) []project.Automation {
	if m == nil {
		return nil
	}
	targets := map[string]bool{id: true}
	for _, fx := range m.Channel(id).Effects {
		targets[fx.ID] = true
	}
	var out []project.Automation
	for _, lane := range m.Lanes {
		if targets[lane.Target] && len(lane.Points) > 0 {
			out = append(out, lane)
		}
	}
	return out
}

func automationLabel(lane project.Automation, ch project.MixChannel) string {
	switch lane.Parameter {
	case "gain":
		return "推子"
	case "pan":
		return "声像"
	}
	label := "湿信号"
	if lane.Parameter == "amount" {
		label = "强度"
	}
	for i, fx := range ch.Effects {
		if fx.ID == lane.Target {
			return fmt.Sprintf("%d %s · %s", i+1, mixEffectName(fx.Kind), label)
		}
	}
	return label
}

func (v *arrangementView) drawAutomation(c *ui.Context, lane project.Automation, track string, name string,
	top float32, start, end int64, ppq int, actions arrangementActions) {
	lo, hi, ok := project.MixRange(lane.Parameter)
	if !ok || end <= start {
		return
	}
	pts := lane.Points
	color := ui.Hex("#9d94ef")
	label := "编辑自动化 " + name
	card := ui.Box(c.Key(lane.ID)).Absolute().Left(0).Top(top).Width(v.width).Height(automationRowHeight-2).
		Background(ui.RGBA(110, 99, 190, .14)).Border(1, ui.RGBA(157, 148, 239, .45)).Clip().Focusable().Label(label)
	card.Draw(func(p *ui.Painter, r ui.Rect) {
		x := func(t int64) float32 { return r.X + float32(float64(t-start)/float64(ppq)*v.zoom) }
		y := func(value float64) float32 { return r.Y + 3 + (r.H-6)*(1-float32((value-lo)/(hi-lo))) }
		first := max(start, pts[0].Tick)
		if first >= end {
			return
		}
		py := y(automationValue(pts, lo, first))
		px := x(first)
		i := sort.Search(len(pts), func(i int) bool { return pts[i].Tick > first })
		for ; i < len(pts) && pts[i].Tick <= end; i++ {
			nx, ny := x(pts[i].Tick), y(pts[i].Value)
			if i > 0 && pts[i-1].Step {
				p.Line(px, py, nx, py, 1, color)
				p.Line(nx, py, nx, ny, 1, color)
			} else {
				p.Line(px, py, nx, ny, 1, color)
			}
			p.Fill(ui.Rect{X: nx - 2, Y: ny - 2, W: 4, H: 4}, color, 1)
			px, py = nx, ny
		}
		ey := y(automationValue(pts, lo, end))
		p.Line(px, py, x(end), ey, 1, color)
	}).HandleInput(func(ev ui.InputEvent) bool {
		if actions.AutomationOpen == nil || !actions.Writable() {
			return false
		}
		if ev.Kind == ui.InputPointerDown && ev.Button == 0 && ev.Mods&ui.Shift == 0 {
			tick := start + int64(math.Round(float64(ev.X)/v.zoom*float64(ppq)))
			actions.AutomationOpen(track, lane, max(0, min(project.MaxTick, tick)))
			return true
		}
		if ev.Kind == ui.InputKeyDown && (ev.Key == ui.KeyEnter || ev.Key == ui.KeySpace) {
			actions.AutomationOpen(track, lane, max(start, pts[0].Tick))
			return true
		}
		return false
	})
}
