package desktop

import (
	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
	"icetudio/internal/storage"
	"math"
)

func (v *arrangementView) navigation(ev ui.InputEvent, p storage.Project, actions arrangementActions) bool {
	if v.gesture != nil {
		return ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom
	}
	if actions.Keyboard != nil && actions.Keyboard(ev) {
		return true
	}
	if ev.Kind == ui.InputZoom || ev.Kind == ui.InputScroll && ev.Mods&ui.Ctrl != 0 {
		anchor := float64(v.start) + float64(ev.X)/v.zoom*float64(p.PPQ)
		factor := float64(ev.Scale)
		if ev.Kind == ui.InputScroll {
			factor = math.Exp(-float64(ev.DY) / 400)
		}
		v.zoom = max(16, min(200, v.zoom*factor))
		v.start = max(0, int64(math.Round(anchor-float64(ev.X)/v.zoom*float64(p.PPQ))))
		if actions.SnapChanged != nil {
			actions.SnapChanged()
		}
		if actions.Navigate != nil {
			actions.Navigate()
		}
		return true
	}
	if ev.Kind == ui.InputScroll {
		delta := ev.DX
		if ev.Mods&ui.Shift != 0 && delta == 0 {
			delta = ev.DY
		}
		if delta == 0 {
			return false
		}
		v.start = max(0, min(project.MaxTick*2, v.start+int64(math.Round(float64(delta)/v.zoom*float64(p.PPQ)))))
		if actions.Navigate != nil {
			actions.Navigate()
		}
		return true
	}
	return false
}
func (v *arrangementView) moveGesture(x float32, p storage.Project) {
	g := v.gesture
	if g == nil {
		return
	}
	g.PointerX = x
	// The hit box stays fixed during capture. Edge scrolling changes only the
	// view origin; delta is still measured against the original revision/time.
	pointer := g.Left + x
	if pointer > v.width-24 {
		v.start = min(project.MaxTick*2, v.start+int64(math.Ceil(24/g.Zoom*float64(g.PPQ))))
	}
	if pointer < 24 {
		v.start = max(0, v.start-int64(math.Ceil(24/g.Zoom*float64(g.PPQ))))
	}
	g.move(x + float32(float64(v.start-g.ViewStart)/float64(g.PPQ)*g.Zoom))
}
func (v *arrangementView) ruler(c *ui.Context, p storage.Project, start, end int64, width float32, actions arrangementActions) {
	box := ui.Box(c).Width(width).Height(arrangementRulerHeight).Focusable().Clip()
	box.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerDown && ev.Button == 0 && v.gesture == nil {
			tick := max(0, min(project.MaxTick*2, start+int64(math.Round(float64(ev.X)/v.zoom*float64(p.PPQ)))))
			if ev.Clicks == 2 && actions.Seek != nil {
				actions.Seek(tick)
				return true
			}
			v.rulerDown = true
			v.rulerTarget = EditTarget{p.ID, p.Revision}
			v.rulerTick = tick
			v.rulerX = ev.X
			return true
		}
		if ev.Kind == ui.InputPointerUp && v.rulerDown {
			v.rulerDown = false
			if ev.Canceled {
				return true
			}
			if v.rulerTarget.ProjectID != p.ID || v.rulerTarget.Revision != p.Revision {
				if actions.Error != nil {
					actions.Error("工程已变化，循环手势未应用")
				}
				return true
			}
			endTick := max(0, start+int64(math.Round(float64(ev.X)/v.zoom*float64(p.PPQ))))
			if math.Abs(float64(ev.X-v.rulerX)) > 4 && actions.Loop != nil {
				actions.Loop(min(v.rulerTick, endTick), max(v.rulerTick, endTick))
			}
			return true
		}
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape {
			v.rulerDown = false
			return true
		}

		return v.navigation(ev, p, actions)
	}).Children(func() { drawArrangementRuler(c, p, start, end, v.zoom) })
}

type arrangementMarquee struct {
	target           EditTarget
	x, y, endX, endY float32
}

func (v *arrangementView) marqueeInput(ev ui.InputEvent, c *ui.Context, p storage.Project, tracks []project.Track, start, end int64, actions arrangementActions) bool {
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 || ev.Mods&ui.Shift == 0 || ev.Y < arrangementRulerHeight || v.marquee != nil || !actions.Writable() {
			return false
		}
		v.marquee = &arrangementMarquee{EditTarget{p.ID, p.Revision}, ev.X, ev.Y, ev.X, ev.Y}
		actions.Freeze(true)
		return true
	case ui.InputPointerMove:
		if v.marquee != nil {
			v.marquee.endX, v.marquee.endY = ev.X, ev.Y
			return true
		}
	case ui.InputKeyDown:
		if ev.Key == ui.KeyEscape && v.marquee != nil {
			v.cancel(actions)
			return true
		}
	case ui.InputPointerUp:
		m := v.marquee
		if m == nil {
			return false
		}
		v.cancel(actions)
		if ev.Canceled || m.target != (EditTarget{p.ID, p.Revision}) || !v.marqueeHandle.Focused(c) {
			return true
		}
		x1, x2 := min(m.x, ev.X), max(m.x, ev.X)
		y1, y2 := min(m.y, ev.Y), max(m.y, ev.Y)
		v.marked = nil
		var firstTrack project.Track
		var firstClip project.Clip
		y := arrangementRulerHeight + 1
		timeline, timelineErr := playback.NewTimeline(&p.State)
		for _, t := range tracks {
			items, lanes := arrangementClips(t.Clips, start, end, p.PPQ, v.zoom)
			for _, item := range items {
				top := y + 4 + float32(item.Lane)*clipRowHeight
				if item.Left < x2 && item.Left+item.Width > x1 && top < y2 && top+clipRowHeight-4 > y1 && len(v.marked) < 64 {
					if len(v.marked) == 0 {
						firstTrack, firstClip = t, item.Clip
					}
					v.marked = append(v.marked, item.Clip.ID)
				}
			}
			y += arrangementLaneHeight(lanes) + float32(len(trackAutomation(p.State.Mixer, t.ID)))*automationRowHeight + 1
			if timelineErr == nil {
				_, waves := waveClips(t, nil, timeline, start, end, v.zoom)
				y += float32(waves) * waveHeight
			}
		}
		if len(v.marked) > 0 {
			actions.Select(firstTrack, firstClip)
		}
		return true
	}
	return v.marquee != nil
}

func (v *arrangementView) dragSelection(p storage.Project, primary string, clip project.Clip) ([]string, int64) {
	selected := map[string]bool{primary: true}
	for _, id := range v.marked {
		selected[id] = true
	}
	includeMarked := selected[clip.ID] && len(v.marked) > 0
	ids := []string{clip.ID}
	first := clip.Start
	for _, t := range p.State.Tracks {
		for _, other := range t.Clips {
			if other.ID == clip.ID {
				continue
			}
			if includeMarked && selected[other.ID] || clip.GroupID != "" && other.GroupID == clip.GroupID {
				ids = append(ids, other.ID)
				first = min(first, other.Start)
			}
		}
	}
	if len(ids) < 2 {
		return nil, first
	}
	return ids, first
}
