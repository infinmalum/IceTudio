package desktop

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// Like the retained TS meterIndex, ticks * 32 represent every supported meter
// beat exactly. Mid-bar changes start a new numbered bar, without moving music.
type meterSegment struct {
	point                         project.MeterPoint
	start, end, beat, bar, number int64
}
type meterProjection []meterSegment
type musicPosition struct {
	Bar, Beat              int64
	Remainder              float64
	Numerator, Denominator int
}
type rulerMark struct {
	Tick        float64
	Kind, Label string
	Count       int
}

func newMeterProjection(ppq int, meter []project.MeterPoint) (meterProjection, error) {
	if ppq < 1 || ppq > 32768 || len(meter) == 0 || meter[0].Tick != 0 {
		return nil, fmt.Errorf("无效 PPQ 或拍号图")
	}
	out := make(meterProjection, 0, len(meter))
	bar := int64(1)
	for i, p := range meter {
		d := p.Denominator
		if p.Tick < 0 || p.Tick > project.MaxTick || i > 0 && p.Tick <= meter[i-1].Tick || d < 1 || d > 128 || d&(d-1) != 0 || p.Numerator < 1 || p.Numerator > 255 {
			return nil, fmt.Errorf("无效拍号变化点")
		}
		beat := int64(ppq * 128 / d)
		end := int64(math.MaxInt64)
		if i+1 < len(meter) {
			end = meter[i+1].Tick * 32
		}
		s := meterSegment{p, p.Tick * 32, end, beat, beat * int64(p.Numerator), bar}
		out = append(out, s)
		if end != math.MaxInt64 {
			bar += (end - s.start + s.bar - 1) / s.bar
		}
	}
	return out, nil
}
func (m meterProjection) at(tick float64) int {
	return max(0, sort.Search(len(m), func(i int) bool { return float64(m[i].point.Tick) > tick })-1)
}
func (m meterProjection) position(tick int64) musicPosition {
	s := m[m.at(float64(tick))]
	offset := tick*32 - s.start
	inBar := offset % s.bar
	return musicPosition{s.number + offset/s.bar, inBar/s.beat + 1, float64(inBar%s.beat) / 32, s.point.Numerator, s.point.Denominator}
}
func (m meterProjection) ruler(ppq int, start, end int64, zoom float64, tempo []project.TempoPoint) ([]rulerMark, bool) {
	ordinary := map[int64]rulerMark{}
	groups := map[string]rulerMark{}
	thinned := false
	px := func(t float64) float64 { return (t - float64(start)) / float64(ppq) * zoom }
	add := func(mark rulerMark) {
		key := int64(math.Floor(px(mark.Tick) / 12))
		old, ok := ordinary[key]
		if !ok || old.Kind == "beat" && mark.Kind == "bar" {
			ordinary[key] = mark
		}
		thinned = thinned || ok
	}
	change := func(mark rulerMark) {
		key := fmt.Sprintf("%s:%d", mark.Kind, int64(math.Floor(px(mark.Tick)/16)))
		old, ok := groups[key]
		if ok {
			old.Count++
			groups[key] = old
			thinned = true
		} else {
			groups[key] = mark
		}
	}
	for i := m.at(float64(start)); i < len(m) && m[i].point.Tick < end; i++ {
		s := m[i]
		a := max(start*32, s.start)
		b := min(end*32, s.end)
		bs := max(int64(1), int64(math.Ceil(52/(float64(s.bar)/32/float64(ppq)*zoom))))
		beatStride := max(int64(1), int64(math.Ceil(12/(float64(s.beat)/32/float64(ppq)*zoom))))
		thinned = thinned || bs > 1 || beatStride > 1
		for n := (a - s.start + s.bar*bs - 1) / (s.bar * bs) * bs; s.start+n*s.bar < b; n += bs {
			add(rulerMark{float64(s.start+n*s.bar) / 32, "bar", fmt.Sprint(s.number + n), 1})
		}
		for n := (a - s.start + s.beat*beatStride - 1) / (s.beat * beatStride) * beatStride; s.start+n*s.beat < b; n += beatStride {
			if n%int64(s.point.Numerator) != 0 {
				add(rulerMark{float64(s.start+n*s.beat) / 32, "beat", "", 1})
			}
		}
		if s.point.Tick >= start {
			change(rulerMark{float64(s.point.Tick), "meter", fmt.Sprintf("%d/%d", s.point.Numerator, s.point.Denominator), 1})
		}
	}
	for _, p := range tempo {
		if p.Tick >= start && p.Tick < end {
			change(rulerMark{float64(p.Tick), "tempo", strconv.FormatFloat(math.Round(60000000/float64(p.USPerQuarter)*100)/100, 'f', -1, 64), 1})
		}
	}
	marks := make([]rulerMark, 0, len(ordinary)+len(groups))
	for _, v := range ordinary {
		marks = append(marks, v)
	}
	for _, v := range groups {
		marks = append(marks, v)
	}
	sort.Slice(marks, func(i, j int) bool {
		if marks[i].Tick != marks[j].Tick {
			return marks[i].Tick < marks[j].Tick
		}
		return marks[i].Kind < marks[j].Kind
	})
	labels := map[string]float64{}
	for i := range marks {
		mark := &marks[i]
		if mark.Label != "" {
			gap := float64(52)
			if mark.Kind == "tempo" {
				gap = 66
			} else if mark.Kind == "meter" {
				gap = 48
			}
			if prev, ok := labels[mark.Kind]; ok && px(mark.Tick)-prev < gap {
				mark.Label = ""
				thinned = true
			} else {
				labels[mark.Kind] = px(mark.Tick)
			}
		}
	}
	return marks, thinned
}
func drawArrangementRuler(c *ui.Context, p storage.Project, start, end int64, zoom float64) {
	index, err := newMeterProjection(p.PPQ, p.State.Meter)
	if err != nil {
		ui.Text(c, err.Error()).Height(52)
		return
	}
	marks, thinned := index.ruler(p.PPQ, start, end, zoom, p.State.Tempo)
	pos := index.position(start)
	label := fmt.Sprintf("工程小节尺：第 %d 小节第 %d 拍，tick %d", pos.Bar, pos.Beat, start)
	if thinned {
		label += "；密集变化已分组合并"
	}
	theme := c.Theme()
	ui.Box(c).Width(float32(16 * zoom)).Height(52).Background(c.Theme().Surface).Label(label).Tooltip(label).Draw(func(paint *ui.Painter, r ui.Rect) {
		for _, m := range marks {
			x := r.X + float32((m.Tick-float64(start))/float64(p.PPQ)*zoom)
			y := float32(0)
			ty := float32(4)
			switch m.Kind {
			case "beat":
				y = 46
			case "bar":
				y = 22
				ty = 24
			case "meter":
				ty = 39
			}
			paint.Line(x, r.Y+y, x, r.Y+52, 1, theme.Border)
			if m.Label != "" {
				text := m.Label
				if m.Kind == "tempo" {
					text = "♩ " + text
				}
				if m.Count > 1 {
					text += fmt.Sprintf(" ×%d", m.Count)
				}
				paint.Text(x+3, r.Y+ty, text, 10, theme.TextMuted)
			}
		}
	})
}
