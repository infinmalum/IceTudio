package project

import (
	"fmt"
	"strconv"
)

// ObjectChange is a bounded, human-readable projection, never an edit payload.
// Raw MIDI bytes are deliberately represented by counts, not transported here.
type ObjectChange struct {
	Kind      string `json:"kind"`
	ObjectID  string `json:"objectId"`
	TrackName string `json:"trackName"`
	ClipName  string `json:"clipName"`
	Field     string `json:"field"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

type ObjectDiff struct {
	Total   int            `json:"total"`
	Changes []ObjectChange `json:"changes"`
}

// DiffObjects compares the final scoped states, including nested deletions.
// Notes have their own bounded DiffNotes table. Rows follow state order; maps
// are used only for lookups so truncation has a stable, deterministic prefix.
func DiffObjects(before, after *State, limit int) ObjectDiff {
	out := ObjectDiff{Changes: []ObjectChange{}}
	add := func(kind, id, track, clip, field, b, a string) {
		if b == a {
			return
		}
		out.Total++
		if len(out.Changes) < limit {
			out.Changes = append(out.Changes, ObjectChange{kind, id, track, clip, field, b, a})
		}
	}
	add("project", "", "", "", "工程名称", before.Title, after.Title)
	oldPatterns := map[string]Pattern{}
	for _, p := range before.Patterns {
		oldPatterns[p.ID] = p
	}
	for _, p := range after.Patterns {
		if _, ok := oldPatterns[p.ID]; !ok {
			add("pattern", p.ID, "", p.Name, "Pattern 资源", "无", p.Name)
		}
		delete(oldPatterns, p.ID)
	}
	for _, p := range before.Patterns {
		if _, ok := oldPatterns[p.ID]; ok {
			add("pattern", p.ID, "", p.Name, "Pattern 资源", p.Name, "无")
		}
	}

	oldTempo := map[int64]TempoPoint{}
	newTempo := map[int64]bool{}
	for _, p := range before.Tempo {
		oldTempo[p.Tick] = p
	}
	for _, p := range after.Tempo {
		newTempo[p.Tick] = true
		b := "无"
		if old, ok := oldTempo[p.Tick]; ok {
			b = tempoValue(old)
		}
		add("tempo", "", "", "", fmt.Sprintf("速度 · tick %d", p.Tick), b, tempoValue(p))
	}
	for _, p := range before.Tempo {
		if !newTempo[p.Tick] {
			add("tempo", "", "", "", fmt.Sprintf("速度 · tick %d", p.Tick), tempoValue(p), "无")
		}
	}
	oldMeter := map[int64]MeterPoint{}
	newMeter := map[int64]bool{}
	for _, p := range before.Meter {
		oldMeter[p.Tick] = p
	}
	for _, p := range after.Meter {
		newMeter[p.Tick] = true
		b := "无"
		if old, ok := oldMeter[p.Tick]; ok {
			b = meterValue(old)
		}
		add("meter", "", "", "", fmt.Sprintf("拍号 · tick %d", p.Tick), b, meterValue(p))
	}
	for _, p := range before.Meter {
		if !newMeter[p.Tick] {
			add("meter", "", "", "", fmt.Sprintf("拍号 · tick %d", p.Tick), meterValue(p), "无")
		}
	}
	oldTracks := map[string]Track{}
	newTracks := map[string]bool{}
	for _, t := range before.Tracks {
		oldTracks[t.ID] = t
	}
	clips := func(b, a Track) {
		oldAudio := map[string]AudioClip{}
		describe := func(c AudioClip) string {
			return fmt.Sprintf("%s · 资产 %s · %d tick · 源 %d+%d 帧 · 淡变 %d/%d · 增益 %d%% · 反向 %t",
				c.Name, c.AssetID, c.Start, c.Source, c.Frames, c.FadeIn, c.FadeOut, c.Gain, c.Reverse)
		}
		for _, c := range b.Audio {
			oldAudio[c.ID] = c
		}
		for _, c := range a.Audio {
			before := "无"
			if previous, ok := oldAudio[c.ID]; ok {
				before = describe(previous)
			}
			add("audio", c.ID, a.Name, c.Name, "音频片段", before, describe(c))
			delete(oldAudio, c.ID)
		}
		for _, c := range b.Audio {
			if _, ok := oldAudio[c.ID]; ok {
				add("audio", c.ID, b.Name, c.Name, "音频片段", describe(c), "无")
			}
		}

		oldClips := map[string]Clip{}
		newClips := map[string]bool{}
		for _, c := range b.Clips {
			oldClips[c.ID] = c
		}
		controls := func(bc, ac Clip) {
			oldEvents := map[string]ControlEvent{}
			newEvents := map[string]bool{}
			trackName, clipName := a.Name, ac.Name
			if a.ID == "" {
				trackName = b.Name
			}
			if ac.ID == "" {
				clipName = bc.Name
			}
			for _, e := range bc.Controls {
				oldEvents[e.ID] = e
			}
			for _, e := range ac.Controls {
				newEvents[e.ID] = true
				old := "无"
				if v, ok := oldEvents[e.ID]; ok {
					old = controlValue(v)
				}
				add("control", e.ID, trackName, clipName, "控制事件", old, controlValue(e))
			}
			for _, e := range bc.Controls {
				if !newEvents[e.ID] {
					add("control", e.ID, trackName, clipName, "控制事件", controlValue(e), "无")
				}
			}
			beforeMarkers := make(map[string]string)
			afterMarkers := make(map[string]bool)
			for _, e := range bc.Raw {
				if name, ok := MarkerName(e.Data); ok {
					beforeMarkers[e.ID] = fmt.Sprintf("%s · %d tick", name, bc.Start+e.Tick)
				}
			}
			for _, e := range ac.Raw {
				if name, ok := MarkerName(e.Data); ok {
					afterMarkers[e.ID] = true
					add("marker", e.ID, trackName, clipName, "段落标记", beforeMarkers[e.ID], fmt.Sprintf("%s · %d tick", name, ac.Start+e.Tick))
				}
			}
			for _, e := range bc.Raw {
				if value, ok := beforeMarkers[e.ID]; ok && !afterMarkers[e.ID] {
					add("marker", e.ID, trackName, clipName, "段落标记", value, "")
				}
			}

		}
		for _, c := range a.Clips {
			newClips[c.ID] = true
			old, ok := oldClips[c.ID]
			if !ok {
				add("clip", c.ID, a.Name, c.Name, "新增片段", "无", clipValue(c))
			} else {
				for i, f := range []string{"片段名称", "片段颜色", "片段绝对起点", "片段长度", "共享 Pattern", "编排分组"} {
					add("clip", c.ID, a.Name, c.Name, f, clipFields(old)[i], clipFields(c)[i])
				}
			}
			controls(old, c)
		}
		for _, c := range b.Clips {
			if !newClips[c.ID] {
				add("clip", c.ID, b.Name, c.Name, "删除片段（含全部事件）", clipValue(c), "无")
				controls(c, Clip{})
			}
		}
	}
	for _, t := range after.Tracks {
		newTracks[t.ID] = true
		old, ok := oldTracks[t.ID]
		if !ok {
			add("track", t.ID, t.Name, "", "新增音轨", "无", trackValue(t))
		} else {
			for i, f := range []string{"音轨名称", "音轨颜色", "默认 MIDI 通道", "GM 音色", "静音", "独奏"} {
				add("track", t.ID, t.Name, "", f, trackFields(old)[i], trackFields(t)[i])
			}
		}
		clips(old, t)
	}
	for _, t := range before.Tracks {
		if !newTracks[t.ID] {
			add("track", t.ID, t.Name, "", "删除音轨（含全部片段）", trackValue(t), "无")
			clips(t, Track{})
		}
	}
	return out
}

func tempoValue(p TempoPoint) string {
	return fmt.Sprintf("%.3f BPM · %d 微秒/四分音符", 60000000/float64(p.USPerQuarter), p.USPerQuarter)
}
func meterValue(p MeterPoint) string { return fmt.Sprintf("%d/%d", p.Numerator, p.Denominator) }
func tickValue(v int64) string       { return fmt.Sprintf("%d tick", v) }
func switchValue(v bool) string {
	if v {
		return "开"
	}
	return "关"
}
func trackFields(t Track) []string {
	return []string{t.Name, t.Color, strconv.Itoa(t.Channel + 1), fmt.Sprintf("%d（GM 1–128）", t.Program+1), switchValue(t.Muted), switchValue(t.Solo)}
}
func clipFields(c Clip) []string {
	return []string{c.Name, c.Color, tickValue(c.Start), tickValue(c.Length), c.PatternID, c.GroupID}
}
func clipValue(c Clip) string {
	bytes := 0
	for _, e := range c.Raw {
		bytes += len(e.Data)
	}
	return fmt.Sprintf("名称 %q · 颜色 %q · 起点 %d tick · 长度 %d tick · %d 音符 · %d 控制事件 · %d 原始事件（%d 字节）", c.Name, c.Color, c.Start, c.Length, len(c.Notes), len(c.Controls), len(c.Raw), bytes)
}
func trackValue(t Track) string {
	return fmt.Sprintf("名称 %q · 颜色 %q · 通道 %d · GM %d · 静音%s · 独奏%s · %d 片段", t.Name, t.Color, t.Channel+1, t.Program+1, switchValue(t.Muted), switchValue(t.Solo), len(t.Clips))
}
func controlValue(e ControlEvent) string {
	value := ""
	switch e.Kind {
	case KindCC:
		value = fmt.Sprintf("CC%d = %d", e.Controller, e.Value)
	case KindProgram:
		value = fmt.Sprintf("GM 音色 %d（1–128）", e.Program+1)
	case KindPolyAftertouch:
		value = fmt.Sprintf("复音压力 · 音高 %d · 值 %d", e.Note, e.Value)
	case KindChannelAftertouch:
		value = fmt.Sprintf("通道压力 %d", e.Value)
	case KindPitchBend:
		value = fmt.Sprintf("弯音 %d（中心 8192）", e.Value)
	}
	return fmt.Sprintf("%s · 片段内 tick %d · 通道 %d", value, e.Tick, e.Channel+1)
}
