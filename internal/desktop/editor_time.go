package desktop

import (
	"fmt"
	"icetudio/internal/project"
	"math"
	"strconv"
	"strings"
)

var snapLabels = []string{"关闭", "1/4", "1/8", "1/16", "1/32", "1/8T", "1/16T", "自适应"}

func editorSnap(label string, zoom float64, ppq int) float64 {
	if ppq < 1 {
		ppq = 480
	}
	if label == "关闭" {
		return 1 / float64(ppq)
	}
	if label == "自适应" {
		for _, step := range []float64{.125, .25, .5, 1, 2, 4, 8, 16} {
			if step*zoom >= 18 {
				return step
			}
		}
		return 16
	}
	value := map[string]float64{"1/4": 1, "1/8": .5, "1/16": .25, "1/32": .125, "1/8T": 1.0 / 3, "1/16T": 1.0 / 6}[label]
	if value == 0 {
		value = 1
	}
	return math.Max(1/float64(ppq), value)
}
func musicTime(m meterProjection, tick int64) string {
	if len(m) == 0 {
		return strconv.FormatInt(tick, 10)
	}
	p := m.position(max(0, tick))
	return fmt.Sprintf("%d:%d:%s", p.Bar, p.Beat, strconv.FormatFloat(p.Remainder, 'f', -1, 64))
}

// Plain integers retain the advanced tick entry. Colon positions use the
// actual meter map, including shortened bars and fractional-tick beats.
func parseMusicTime(m meterProjection, raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	invalid := fmt.Errorf("位置须为 小节:拍:拍内tick 或整数 tick，且位于当前拍号图中")
	if !strings.Contains(raw, ":") {
		t, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || t < 0 || t > project.MaxTick*2 {
			return 0, invalid
		}
		return t, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, invalid
	}
	bar, e1 := strconv.ParseInt(parts[0], 10, 64)
	beat, e2 := strconv.ParseInt(parts[1], 10, 64)
	rem, e3 := strconv.ParseFloat(parts[2], 64)
	if e1 != nil || e2 != nil || e3 != nil || bar < 1 || bar > project.MaxTick*256 || beat < 1 || rem < 0 || math.IsNaN(rem) || math.IsInf(rem, 0) {
		return 0, invalid
	}
	for _, s := range m {
		if bar < s.number || beat > int64(s.point.Numerator) || rem*32 >= float64(s.beat) {
			continue
		}
		scaled := float64(s.start) + float64(bar-s.number)*float64(s.bar) + float64(beat-1)*float64(s.beat) + rem*32
		if scaled < float64(s.start) || scaled >= float64(s.end) || scaled > float64(project.MaxTick*64) || math.Mod(scaled, 32) != 0 {
			continue
		}
		tick := int64(scaled / 32)
		p := m.position(tick)
		if p.Bar == bar && p.Beat == beat && p.Remainder == rem {
			return tick, nil
		}
	}
	return 0, invalid
}
