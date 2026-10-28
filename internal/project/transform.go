package project

import (
	"fmt"
	"math"
	"sort"
)

// Deterministic note transforms for the agent tools (docs/p3-agent-tools.md).
// The model declares a selection and one transform; this file compiles the
// declaration into regular note.update ops so every write still goes through
// the P1 transaction path — no new write path, per-note values are computed
// by the program, never invented by the model.

// Selection filters notes across the project. All given criteria must match
// (AND); the zero value selects every note. Notes enter a tick window by
// local clip onset (startTick ≤ start < endTick, without clip.Start) — unlike
// project__query, whose region
// keeps overlapping notes, selections aim at "the notes in this stretch".
type Selection struct {
	TrackIDs    []string `json:"trackIds,omitempty"`
	ClipIDs     []string `json:"clipIds,omitempty"`
	StartTick   *int64   `json:"startTick,omitempty"`
	EndTick     *int64   `json:"endTick,omitempty"`
	PitchMin    *int     `json:"pitchMin,omitempty"`
	PitchMax    *int     `json:"pitchMax,omitempty"`
	Channel     *int     `json:"channel,omitempty"`
	VelocityMin *int     `json:"velocityMin,omitempty"`
	VelocityMax *int     `json:"velocityMax,omitempty"`
}

// TransformSpec is the agent-facing transform payload: exactly one of the
// four variants. Values outside the documented ranges are rejected here so
// the app layer can trust the compiled ops.
type TransformSpec struct {
	Transpose *struct {
		Semitones int `json:"semitones"`
	} `json:"transpose,omitempty"`
	Quantize *struct {
		Unit     string `json:"unit"`
		Strength *int   `json:"strength,omitempty"` // omitted → 100; explicit 0 does not move notes
	} `json:"quantize,omitempty"`
	Velocity *struct {
		Scale  *int `json:"scale,omitempty"`  // percent, 1–300
		Offset *int `json:"offset,omitempty"` // -126..126
		Set    *int `json:"set,omitempty"`    // 1–127, exclusive of the others
	} `json:"velocity,omitempty"`
	Shift *struct {
		Ticks int64 `json:"ticks"`
	} `json:"shift,omitempty"`
}

// TransformStats reports what a selection matched and what a transform did.
type TransformStats struct {
	Matched      int      `json:"matched"`
	Changed      int      `json:"changed"`
	SkippedPitch int      `json:"skippedPitch"`
	SkippedTime  int      `json:"skippedTime"`
	Clips        []string `json:"clips,omitempty"`
}

// SelectedClip groups matched notes by their clip for tool results.
type SelectedClip struct {
	TrackID string `json:"trackId"`
	ClipID  string `json:"clipId"`
	Notes   []Note `json:"notes"`
}

func (s Selection) validate() error {
	for _, id := range append(append([]string{}, s.TrackIDs...), s.ClipIDs...) {
		if !ValidID(id) {
			return fmt.Errorf("选择器对象 ID 无效：%q", id)
		}
	}
	if s.StartTick != nil && (*s.StartTick < 0 || *s.StartTick > MaxTick) {
		return fmt.Errorf("选择器 startTick 超界（0–%d）：%d", MaxTick, *s.StartTick)
	}
	if s.EndTick != nil && (*s.EndTick < 0 || *s.EndTick > MaxTick) {
		return fmt.Errorf("选择器 endTick 超界（0–%d）：%d", MaxTick, *s.EndTick)
	}
	if s.StartTick != nil && s.EndTick != nil && *s.StartTick >= *s.EndTick {
		return fmt.Errorf("选择器窗口为空：startTick %d ≥ endTick %d", *s.StartTick, *s.EndTick)
	}
	if s.PitchMin != nil && (*s.PitchMin < 0 || *s.PitchMin > MaxMIDI) {
		return fmt.Errorf("选择器 pitchMin 超界（0–%d）：%d", MaxMIDI, *s.PitchMin)
	}
	if s.PitchMax != nil && (*s.PitchMax < 0 || *s.PitchMax > MaxMIDI) {
		return fmt.Errorf("选择器 pitchMax 超界（0–%d）：%d", MaxMIDI, *s.PitchMax)
	}
	if s.PitchMin != nil && s.PitchMax != nil && *s.PitchMin > *s.PitchMax {
		return fmt.Errorf("选择器 pitchMin %d > pitchMax %d", *s.PitchMin, *s.PitchMax)
	}
	if s.VelocityMin != nil && (*s.VelocityMin < MinVelocity || *s.VelocityMin > MaxMIDI) {
		return fmt.Errorf("选择器 velocityMin 超界（%d–%d）：%d", MinVelocity, MaxMIDI, *s.VelocityMin)
	}
	if s.VelocityMax != nil && (*s.VelocityMax < MinVelocity || *s.VelocityMax > MaxMIDI) {
		return fmt.Errorf("选择器 velocityMax 超界（%d–%d）：%d", MinVelocity, MaxMIDI, *s.VelocityMax)
	}
	if s.VelocityMin != nil && s.VelocityMax != nil && *s.VelocityMin > *s.VelocityMax {
		return fmt.Errorf("选择器 velocityMin %d > velocityMax %d", *s.VelocityMin, *s.VelocityMax)
	}
	if s.Channel != nil && (*s.Channel < 0 || *s.Channel > MaxChannel) {
		return fmt.Errorf("选择器 channel 超界（0–%d）：%d", MaxChannel, *s.Channel)
	}
	return nil
}

// bounded requires at least one scoping criterion, so a transform cannot
// sweep the whole project unconditionally.
func (s Selection) bounded() bool {
	return len(s.TrackIDs) > 0 || len(s.ClipIDs) > 0 || s.StartTick != nil || s.EndTick != nil
}

func (tr TransformSpec) validate() error {
	n := 0
	if tr.Transpose != nil {
		n++
	}
	if tr.Quantize != nil {
		n++
	}
	if tr.Velocity != nil {
		n++
	}
	if tr.Shift != nil {
		n++
	}
	if n != 1 {
		return fmt.Errorf("transform 必须恰好指定一种变换（transpose/quantize/velocity/shift 之一），收到 %d 种", n)
	}
	if tr.Transpose != nil && (tr.Transpose.Semitones < -MaxMIDI || tr.Transpose.Semitones > MaxMIDI) {
		return fmt.Errorf("移调半音数超界（±%d）：%d", MaxMIDI, tr.Transpose.Semitones)
	}
	if tr.Quantize != nil {
		if _, err := gridTicks(480, tr.Quantize.Unit); err != nil {
			return err
		}
		if strength := tr.Quantize.Strength; strength != nil && (*strength < 0 || *strength > 100) {
			return fmt.Errorf("量化强度必须在 0–100，收到 %d", *strength)
		}
	}
	if tr.Velocity != nil {
		v := tr.Velocity
		if v.Set != nil && (v.Scale != nil || v.Offset != nil) {
			return fmt.Errorf("velocity.set 不能与 scale/offset 同时使用")
		}
		if v.Set == nil && v.Scale == nil && v.Offset == nil {
			return fmt.Errorf("velocity 变换缺少 scale/offset/set 之一")
		}
		if v.Set != nil && (*v.Set < MinVelocity || *v.Set > MaxMIDI) {
			return fmt.Errorf("力度必须在 %d–%d，收到 %d", MinVelocity, MaxMIDI, *v.Set)
		}
		if v.Scale != nil && (*v.Scale < 1 || *v.Scale > 300) {
			return fmt.Errorf("力度 scale 必须在 1–300（百分比），收到 %d", *v.Scale)
		}
		if v.Offset != nil && (*v.Offset < -(MaxMIDI-1) || *v.Offset > MaxMIDI-1) {
			return fmt.Errorf("力度 offset 必须在 ±%d，收到 %d", MaxMIDI-1, *v.Offset)
		}
	}
	if tr.Shift != nil && (tr.Shift.Ticks < -MaxTick || tr.Shift.Ticks > MaxTick) {
		return fmt.Errorf("平移 tick 数超界（±%d）", MaxTick)
	}
	return nil
}

// gridTicks resolves a musical unit against the project's PPQ. Non-divisible
// PPQs floor to whole ticks; grids never collapse below one tick.
func gridTicks(ppq int, unit string) (int64, error) {
	if ppq < 1 {
		return 0, fmt.Errorf("工程 PPQ 无效：%d", ppq)
	}
	den := 0
	triplet := false
	switch unit {
	case "1/4":
		den = 4
	case "1/8":
		den = 8
	case "1/16":
		den = 16
	case "1/32":
		den = 32
	case "1/8T":
		den, triplet = 8, true
	case "1/16T":
		den, triplet = 16, true
	default:
		return 0, fmt.Errorf("量化单位无效（支持 1/4、1/8、1/16、1/32、1/8T、1/16T）：%q", unit)
	}
	if triplet {
		den *= 3
	}
	g := int64(ppq) * 4 / int64(den)
	if g < 1 {
		g = 1
	}
	return g, nil
}

func (s Selection) matches(n Note) bool {
	if s.StartTick != nil && n.Start < *s.StartTick {
		return false
	}
	if s.EndTick != nil && n.Start >= *s.EndTick {
		return false
	}
	if s.PitchMin != nil && n.Pitch < *s.PitchMin {
		return false
	}
	if s.PitchMax != nil && n.Pitch > *s.PitchMax {
		return false
	}
	if s.Channel != nil && n.Channel != *s.Channel {
		return false
	}
	if s.VelocityMin != nil && n.Velocity < *s.VelocityMin {
		return false
	}
	if s.VelocityMax != nil && n.Velocity > *s.VelocityMax {
		return false
	}
	return true
}

func setOf(ids []string) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// SelectNotes returns every note matching the selection, grouped by clip in
// state order with notes sorted by (start, id). Track/clip ID filters only
// pick existing objects; a typo simply matches nothing (the transform path
// turns that into an explicit rejection).
func SelectNotes(st *State, sel Selection) ([]SelectedClip, TransformStats, error) {
	if err := sel.validate(); err != nil {
		return nil, TransformStats{}, err
	}
	trackSet, clipSet := setOf(sel.TrackIDs), setOf(sel.ClipIDs)
	stats := TransformStats{}
	out := []SelectedClip{}
	for _, t := range st.Tracks {
		if len(trackSet) > 0 && !trackSet[t.ID] {
			continue
		}
		for _, c := range t.Clips {
			if len(clipSet) > 0 && !clipSet[c.ID] {
				continue
			}
			group := SelectedClip{TrackID: t.ID, ClipID: c.ID}
			for _, n := range c.Notes {
				if !sel.matches(n) {
					continue
				}
				stats.Matched++
				group.Notes = append(group.Notes, n)
			}
			if len(group.Notes) == 0 {
				continue
			}
			sort.SliceStable(group.Notes, func(i, j int) bool {
				if group.Notes[i].Start != group.Notes[j].Start {
					return group.Notes[i].Start < group.Notes[j].Start
				}
				return group.Notes[i].ID < group.Notes[j].ID
			})
			out = append(out, group)
		}
	}
	return out, stats, nil
}

// CompileTransform validates the request and compiles it into note.update
// ops (one per affected clip) plus stats. It never mutates st. On error the
// caller has nothing to clean up and no op is produced.
func CompileTransform(st *State, sel Selection, tr TransformSpec) ([]Op, TransformStats, error) {
	if err := sel.validate(); err != nil {
		return nil, TransformStats{}, err
	}
	if err := tr.validate(); err != nil {
		return nil, TransformStats{}, err
	}
	if !sel.bounded() {
		return nil, TransformStats{}, fmt.Errorf("变换必须限定选择范围（clipIds、trackIds 或 tick 窗口至少一项）；不限定范围的整工程重写被拒绝")
	}
	groups, stats, err := SelectNotes(st, sel)
	if err != nil {
		return nil, TransformStats{}, err
	}
	if stats.Matched == 0 {
		return nil, TransformStats{}, fmt.Errorf("选择未匹配任何音符；请先用 project__select 确认选择器")
	}
	byClip := make(map[string][]NoteDelta)
	for _, group := range groups {
		for _, n := range group.Notes {
			delta, outcome := tr.delta(n, st.PPQ)
			switch outcome {
			case deltaSkipPitch:
				stats.SkippedPitch++
				continue
			case deltaSkipTime:
				stats.SkippedTime++
				continue
			case deltaChanged:
				byClip[group.ClipID] = append(byClip[group.ClipID], delta)
				stats.Changed++
			}
		}
	}
	if len(byClip) == 0 {
		detail := "选择匹配的音符没有任何可应用的变更"
		if stats.SkippedPitch > 0 || stats.SkippedTime > 0 {
			detail = fmt.Sprintf("%s（移调越界跳过 %d、平移越界跳过 %d）", detail, stats.SkippedPitch, stats.SkippedTime)
		}
		return nil, stats, fmt.Errorf("%s；请调整变换参数或选择器", detail)
	}
	ops := []Op{}
	for _, t := range st.Tracks {
		for _, c := range t.Clips {
			deltas, ok := byClip[c.ID]
			if !ok {
				continue
			}
			ops = append(ops, Op{Kind: "note.update", ClipID: c.ID, NoteDeltas: deltas})
			stats.Clips = append(stats.Clips, c.ID)
		}
	}
	return ops, stats, nil
}

// delta computes the per-note effect of the transform against the project's
// PPQ. Outcomes distinguish an intentional skip (out-of-range transpose or
// shift, counted in stats) from "nothing to do" (no actual change).
type deltaOutcome int

const (
	deltaNone deltaOutcome = iota
	deltaChanged
	deltaSkipPitch
	deltaSkipTime
)

func (tr TransformSpec) delta(n Note, ppq int) (NoteDelta, deltaOutcome) {
	switch {
	case tr.Transpose != nil:
		p := n.Pitch + tr.Transpose.Semitones
		if p < 0 || p > MaxMIDI {
			return NoteDelta{}, deltaSkipPitch
		}
		if p == n.Pitch {
			return NoteDelta{}, deltaNone
		}
		return NoteDelta{ID: n.ID, Pitch: &p}, deltaChanged
	case tr.Quantize != nil:
		grid, err := gridTicks(ppq, tr.Quantize.Unit)
		if err != nil {
			return NoteDelta{}, deltaNone
		}
		strength := 100
		if tr.Quantize.Strength != nil {
			strength = *tr.Quantize.Strength
		}
		snapped := (n.Start + grid/2) / grid * grid
		if snapped > MaxTick {
			snapped = MaxTick
		}
		newStart := n.Start + int64(math.Round(float64(snapped-n.Start)*float64(strength)/100))
		if newStart < 0 {
			newStart = 0
		}
		if newStart > MaxTick {
			newStart = MaxTick
		}
		if newStart == n.Start {
			return NoteDelta{}, deltaNone
		}
		return NoteDelta{ID: n.ID, Start: &newStart}, deltaChanged
	case tr.Velocity != nil:
		v := n.Velocity
		if tr.Velocity.Set != nil {
			v = *tr.Velocity.Set
		} else {
			if tr.Velocity.Scale != nil {
				v = int(math.Round(float64(v) * float64(*tr.Velocity.Scale) / 100))
			}
			if tr.Velocity.Offset != nil {
				v += *tr.Velocity.Offset
			}
		}
		if v < MinVelocity {
			v = MinVelocity
		}
		if v > MaxMIDI {
			v = MaxMIDI
		}
		if v == n.Velocity {
			return NoteDelta{}, deltaNone
		}
		return NoteDelta{ID: n.ID, Velocity: &v}, deltaChanged
	case tr.Shift != nil:
		s := n.Start + tr.Shift.Ticks
		if s < 0 || s > MaxTick {
			return NoteDelta{}, deltaSkipTime
		}
		if s == n.Start {
			return NoteDelta{}, deltaNone
		}
		return NoteDelta{ID: n.ID, Start: &s}, deltaChanged
	}
	return NoteDelta{}, deltaNone
}
