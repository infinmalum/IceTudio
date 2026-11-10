package desktop

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

type mixerGesture struct {
	target  EditTarget
	channel string
	gain    bool
	value   float64
}

func mixerMeter(c *ui.Context, meter playback.Meter) {
	t := c.Theme()
	ui.Box(c).Width(28).Label("峰值电平").Draw(func(p *ui.Painter, r ui.Rect) {
		level := func(v float64) float32 {
			return float32(max(0, min(1, (60+20*math.Log10(max(v, .001)))/60)))
		}
		top, h := r.Y+6, r.H-12
		p.Fill(ui.Rect{X: r.X, Y: top, W: 8, H: h}, t.Background, 2)
		for _, db := range []int{0, -12, -24, -48} {
			y := top + h*float32(-db)/60
			p.Line(r.X+9, y, r.X+12, y, 1, t.Border)
			p.Text(r.X+13, y-5, fmt.Sprint(db), 9, t.TextMuted)
		}
		filled := h * level(meter.Peak)
		color := t.Accent
		if meter.Hold > 1 {
			color = t.Warning
		}
		if filled > 0 {
			p.Fill(ui.Rect{X: r.X, Y: top + h - filled, W: 8, H: filled}, color, 2)
		}
		if meter.Hold > 0 {
			y := top + h*(1-level(meter.Hold))
			p.Line(r.X, y, r.X+8, y, 1, t.Text)
		}
	})
}

func (a *Workspace) mixerFader(c *ui.Context, choice mixChoice, value float64, gain bool) {
	t := c.Theme()
	lo, hi, reset := -1.0, 1.0, 0.0
	label := "声像"
	if gain {
		lo, hi, reset, label = 0, 2, 1, "推子"
	}
	target := EditTarget{a.active.ID, a.active.Revision}
	active := a.mixGesture
	held := active != nil && active.channel == choice.id && active.gain == gain
	writable := a.writable() && a.form == nil && a.mapForm == nil
	shown := value
	if held {
		shown = active.value
	}
	box := ui.Box(c).Role(ui.RoleSlider).Range(lo, hi, shown).Label(label + " " + choice.name).
		Tooltip("拖动或用方向键调整；Home 恢复默认值").Focusable().Disabled(!writable && !held)
	size := ui.Rect{W: 108, H: 23}
	if gain {
		size.W, size.H = 64, 96
	}
	box.Size(size.W, size.H)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		f := float32((shown - lo) / (hi - lo))
		handle := t.Accent
		if !writable && !held {
			handle = t.TextMuted
		}
		if gain {
			x := r.X + 20
			top, h := r.Y+7, r.H-14
			y := top + h*(1-f)
			p.Fill(ui.Rect{X: x - 2, Y: top, W: 4, H: h}, t.Border, 2)
			p.Line(x-9, top+h/2, x+9, top+h/2, 1, t.TextMuted)
			p.Fill(ui.Rect{X: x - 12, Y: y - 5, W: 24, H: 10}, handle, 3)
			p.Text(x+15, y-5, fmt.Sprintf("%.0f%%", shown*100), 9, t.TextMuted)
			return
		}
		y, left, span := r.Y+14, r.X+6, r.W-12
		p.Text(r.X+4, r.Y, fmt.Sprintf("声像 %+.0f", shown*100), 10, t.Text)
		p.Line(left, y, left+span, y, 2, t.Border)
		p.Line(left+span/2, y-3, left+span/2, y+3, 1, t.TextMuted)
		p.Fill(ui.Rect{X: left + span*f - 4, Y: y - 4, W: 8, H: 8}, handle, 4)
	})
	box.HandleInput(func(ev ui.InputEvent) bool {
		active := a.mixGesture
		held := active != nil && active.channel == choice.id && active.gain == gain
		switch ev.Kind {
		case ui.InputPointerDown:
			if !writable || active != nil || ev.Button != 0 {
				return false
			}
			a.mixGesture = &mixerGesture{target: target, channel: choice.id, gain: gain, value: mixerFaderPosition(size, ev, gain, lo, hi)}
			return true
		case ui.InputPointerMove:
			if held {
				active.value = mixerFaderPosition(size, ev, gain, lo, hi)
			}
			return held
		case ui.InputPointerUp:
			if !held {
				return false
			}
			a.mixGesture = nil
			if !ev.Canceled {
				a.commitMixerFader(active.target, choice.id, gain, value, mixerFaderPosition(size, ev, gain, lo, hi))
			}
			return true
		case ui.InputKeyDown:
			if held && ev.Key == ui.KeyEscape {
				a.mixGesture = nil
				return true
			}
			if !writable || ev.Repeat {
				return false
			}
			next := value
			switch ev.Key {
			case ui.KeyUp, ui.KeyRight:
				next += .05
			case ui.KeyDown, ui.KeyLeft:
				next -= .05
			case ui.KeyHome:
				next = reset
			default:
				return false
			}
			a.commitMixerFader(target, choice.id, gain, value, max(lo, min(hi, next)))
			return true
		}
		return false
	})
}

func mixerFaderPosition(bounds ui.Rect, ev ui.InputEvent, gain bool, lo, hi float64) float64 {
	f := (ev.X - 6) / max(1, bounds.W-12)
	if gain {
		f = 1 - (ev.Y-7)/max(1, bounds.H-14)
	}
	return lo + float64(max(0, min(1, f)))*(hi-lo)
}

func (a *Workspace) commitMixerFader(target EditTarget, channel string, gain bool, previous, value float64) {
	value = math.Round(value*1000) / 1000
	if value == previous || a.form != nil || a.mapForm != nil {
		return
	}
	if target.ProjectID != a.active.ID || target.Revision != a.active.Revision {
		a.message = "工程已变化，本次混音调整未应用"
		return
	}
	mix := project.Mixer{}
	if a.active.State.Mixer != nil {
		mix = a.active.State.Mixer.Copy()
	}
	ch := mix.Channel(channel)
	if gain {
		ch.Gain = value
	} else {
		ch.Pan = value
	}
	mix.SetChannel(ch)
	a.mutateAt(target, "edit", []project.Op{{Kind: "mixer.set", Mixer: &mix}})
}
