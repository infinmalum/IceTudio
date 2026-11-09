package playback

import (
	"fmt"
	"sort"

	"icetudio/internal/project"
)

func meterBeat32(ppq int, p project.MeterPoint) (int64, error) {
	d := p.Denominator
	if p.Numerator < 1 || p.Numerator > project.MaxNumerator || d < 1 || d > 128 || d&(d-1) != 0 {
		return 0, fmt.Errorf("节拍器需要有效拍号")
	}
	return int64(ppq * 128 / d), nil
}

func compileWithMetronome(snapshot Snapshot, enabled bool) (*Score, error) {
	score, err := Compile(snapshot)
	if err != nil || !enabled {
		return score, err
	}
	for i, p := range snapshot.State.Meter {
		beat, err := meterBeat32(snapshot.State.PPQ, p)
		if err != nil || p.Tick < 0 || p.Tick > project.MaxTick || i > 0 && p.Tick <= snapshot.State.Meter[i-1].Tick {
			return nil, fmt.Errorf("节拍器拍号图无效")
		}
		end := score.EndTick
		if i+1 < len(snapshot.State.Meter) {
			end = min(end, snapshot.State.Meter[i+1].Tick)
		}
		for tick, beatNumber := p.Tick*32, 0; tick < end*32; tick, beatNumber = tick+beat, beatNumber+1 {
			if len(score.Events) >= MaxEvents {
				return nil, fmt.Errorf("节拍器与工程事件超过播放上限")
			}
			accent := int32(0)
			if beatNumber%p.Numerator == 0 {
				accent = 1
			}
			score.Events = append(score.Events, Event{Frame: score.Timeline.Frame(float64(tick) / 32), Kind: Click, A: accent})
		}
	}
	sort.SliceStable(score.Events, func(i, j int) bool { return score.Events[i].Frame < score.Events[j].Frame })
	return score, nil
}

func countInAt(st *project.State, timeline Timeline, tick int64) (int, int64, error) {
	if len(st.Meter) == 0 || st.Meter[0].Tick != 0 {
		return 0, 0, fmt.Errorf("预备拍需要有效拍号图")
	}
	i := sort.Search(len(st.Meter), func(i int) bool { return st.Meter[i].Tick > tick }) - 1
	p := st.Meter[i]
	beat, err := meterBeat32(st.PPQ, p)
	if err != nil {
		return 0, 0, err
	}
	j := sort.Search(len(timeline.segments), func(i int) bool { return timeline.segments[i].tick > float64(tick) }) - 1
	us := timeline.segments[max(0, j)].us
	frames := max(1, int64(beat)*int64(us)*SampleRate/(32*int64(st.PPQ)*1e6))
	return p.Numerator, frames, nil
}
