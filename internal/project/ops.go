package project

import (
	"fmt"
	"slices"
	"sort"
)

// Op is one edit instruction inside a transaction. Payloads are sparse:
// only the fields relevant to Kind are read; update patches carry pointers
// so "no change" is distinguishable from a zero value.
type Op struct {
	Kind string `json:"kind"`

	Title string       `json:"title,omitempty"` // project.rename
	Tempo []TempoPoint `json:"tempo,omitempty"` // tempo.set
	Meter []MeterPoint `json:"meter,omitempty"` // meter.set

	TrackID    string      `json:"trackId,omitempty"`
	Track      *TrackSpec  `json:"track,omitempty"`      // track.add
	TrackPatch *TrackPatch `json:"trackPatch,omitempty"` // track.update

	ClipID    string     `json:"clipId,omitempty"`
	Clip      *ClipSpec  `json:"clip,omitempty"`      // clip.add
	ClipPatch *ClipPatch `json:"clipPatch,omitempty"` // clip.update

	Notes      []NewNote   `json:"notes,omitempty"`      // note.add
	NoteDeltas []NoteDelta `json:"noteDeltas,omitempty"` // note.update
	NoteIDs    []string    `json:"noteIds,omitempty"`    // note.remove
	Events     []EventSpec `json:"events,omitempty"`     // control.add
	EventIDs   []string    `json:"eventIds,omitempty"`   // control.remove

	// Inverse-only payloads, filled when applyOp builds the inverse of a
	// remove. ApplyNewTransaction rejects nonempty client payloads; trusted
	// history execution passes them to applyOp to restore nested objects.
	InvClips    []Clip         `json:"invClips,omitempty"`
	InvNotes    []Note         `json:"invNotes,omitempty"`
	InvControls []ControlEvent `json:"invControls,omitempty"`
	InvRaw      []RawEvent     `json:"invRaw,omitempty"`
}

type TrackSpec struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Color   string `json:"color,omitempty"` // 调色板枚举（P1 §11）
	Channel int    `json:"channel"`
	Program int    `json:"program"`
	Muted   bool   `json:"muted,omitempty"`
	Solo    bool   `json:"solo,omitempty"`
}

type TrackPatch struct {
	Name    *string `json:"name,omitempty"`
	Color   *string `json:"color,omitempty"`
	Channel *int    `json:"channel,omitempty"`
	Program *int    `json:"program,omitempty"`
	Muted   *bool   `json:"muted,omitempty"`
	Solo    *bool   `json:"solo,omitempty"`
}

type ClipSpec struct {
	ID      string `json:"id,omitempty"`
	TrackID string `json:"trackId,omitempty"`
	Name    string `json:"name,omitempty"`
	Color   string `json:"color,omitempty"` // 调色板枚举（P1 §11）
	Start   int64  `json:"start"`
	Length  int64  `json:"length"`
}

type ClipPatch struct {
	Name   *string `json:"name,omitempty"`
	Color  *string `json:"color,omitempty"`
	Start  *int64  `json:"start,omitempty"`
	Length *int64  `json:"length,omitempty"`
}

type NewNote struct {
	ID       string `json:"id,omitempty"`
	Start    int64  `json:"start"`
	Duration int64  `json:"duration"`
	Pitch    int    `json:"pitch"`
	Velocity int    `json:"velocity"`
	Channel  int    `json:"channel"`
}

type NoteDelta struct {
	ID       string `json:"id"`
	Start    *int64 `json:"start,omitempty"`
	Duration *int64 `json:"duration,omitempty"`
	Pitch    *int   `json:"pitch,omitempty"`
	Velocity *int   `json:"velocity,omitempty"`
}

type EventSpec struct {
	ID         string `json:"id,omitempty"`
	Tick       int64  `json:"tick"`
	Kind       string `json:"kind"`
	Channel    int    `json:"channel"`
	Controller int    `json:"controller,omitempty"`
	Value      int    `json:"value,omitempty"`
	Program    int    `json:"program,omitempty"`
	Note       int    `json:"note,omitempty"`
}

func opError(kind, detail string, args ...any) error {
	return fmt.Errorf("操作 %s：%s", kind, fmt.Sprintf(detail, args...))
}

func checkID(kind, id string) error {
	if !ValidID(id) {
		return opError(kind, "对象 ID 无效（限 [a-z0-9_]，1–64 字符）：%q", id)
	}
	return nil
}

func validateTrackValues(kind string, channel, program int) error {
	if channel < 0 || channel > 15 {
		return opError(kind, "通道必须在 0–15，收到 %d", channel)
	}
	if program < 0 || program > 127 {
		return opError(kind, "音色编号必须在 0–127，收到 %d", program)
	}
	return nil
}

func validateNoteValues(kind string, n NewNote) error {
	if n.Channel < 0 || n.Channel > 15 {
		return opError(kind, "音符通道必须在 0–15，收到 %d", n.Channel)
	}
	if n.Duration < 1 {
		return opError(kind, "音符时长必须 ≥1 tick，收到 %d", n.Duration)
	}
	if n.Start < 0 {
		return opError(kind, "音符起点不能为负")
	}
	if n.Pitch < 0 || n.Pitch > 127 {
		return opError(kind, "音高必须在 0–127，收到 %d", n.Pitch)
	}
	if n.Velocity < 1 || n.Velocity > 127 {
		return opError(kind, "力度必须在 1–127，收到 %d", n.Velocity)
	}
	if n.ID != "" {
		return checkID(kind, n.ID)
	}
	return nil
}

func validateEventValues(kind string, e EventSpec) error {
	if e.Tick < 0 {
		return opError(kind, "事件 tick 不能为负")
	}
	if e.Channel < 0 || e.Channel > 15 {
		return opError(kind, "通道必须在 0–15，收到 %d", e.Channel)
	}
	switch e.Kind {
	case KindCC:
		if e.Controller < 0 || e.Controller > 127 || e.Value < 0 || e.Value > 127 {
			return opError(kind, "CC 控制器与值必须在 0–127")
		}
	case KindPitchBend:
		if e.Value < 0 || e.Value > 16383 {
			return opError(kind, "弯音值必须在 0–16383，收到 %d", e.Value)
		}
	case KindProgram:
		if e.Program < 0 || e.Program > 127 {
			return opError(kind, "音色编号必须在 0–127，收到 %d", e.Program)
		}
	case KindChannelAftertouch:
		if e.Value < 0 || e.Value > 127 {
			return opError(kind, "通道压力值必须在 0–127，收到 %d", e.Value)
		}
	case KindPolyAftertouch:
		if e.Note < 0 || e.Note > 127 || e.Value < 0 || e.Value > 127 {
			return opError(kind, "复音压力的音高与值必须在 0–127")
		}
	default:
		return opError(kind, "不支持的事件类型 %q", e.Kind)
	}
	if e.ID != "" {
		return checkID(kind, e.ID)
	}
	return nil
}

// idSet collects every object id in the state for uniqueness checks.
func idSet(st *State) map[string]bool {
	ids := map[string]bool{}
	for _, t := range st.Tracks {
		ids[t.ID] = true
		for _, c := range t.Clips {
			ids[c.ID] = true
			for _, n := range c.Notes {
				ids[n.ID] = true
			}
			for _, e := range c.Controls {
				ids[e.ID] = true
			}
			for _, r := range c.Raw {
				ids[r.ID] = true
			}
		}
	}
	return ids
}

func registerClipIDs(ids map[string]bool, c Clip) {
	ids[c.ID] = true
	for _, n := range c.Notes {
		ids[n.ID] = true
	}
	for _, e := range c.Controls {
		ids[e.ID] = true
	}
	for _, r := range c.Raw {
		ids[r.ID] = true
	}
}

func dropClipIDs(ids map[string]bool, c Clip) {
	delete(ids, c.ID)
	for _, n := range c.Notes {
		delete(ids, n.ID)
	}
	for _, e := range c.Controls {
		delete(ids, e.ID)
	}
	for _, r := range c.Raw {
		delete(ids, r.ID)
	}
}

func clipFitCheck(kind string, c *Clip) error {
	for _, n := range c.Notes {
		if n.Start < 0 || n.Start >= c.Length || n.Start+n.Duration > c.Length {
			return opError(kind, "音符 %s 超出片段范围（长度 %d）", n.ID, c.Length)
		}
	}
	for _, e := range c.Controls {
		if e.Tick < 0 || e.Tick >= c.Length {
			return opError(kind, "事件 %s 超出片段范围（长度 %d）", e.ID, c.Length)
		}
	}
	for _, r := range c.Raw {
		if r.Tick < 0 || r.Tick >= c.Length {
			return opError(kind, "原始事件 %s 超出片段范围（长度 %d）", r.ID, c.Length)
		}
	}
	return nil
}

func trackCount(kind string, st *State, delta int) error {
	if len(st.Tracks)+delta > MaxTracks {
		return opError(kind, "轨数超过上限 %d", MaxTracks)
	}
	return nil
}

func noteCount(kind string, st *State, delta int) error {
	total := 0
	for _, t := range st.Tracks {
		for _, c := range t.Clips {
			total += len(c.Notes)
		}
	}
	if total+delta > MaxNotes {
		return opError(kind, "音符总数超过上限 %d", MaxNotes)
	}
	return nil
}

func rawByteCount(st *State) int {
	total := 0
	for _, t := range st.Tracks {
		for _, c := range t.Clips {
			for _, r := range c.Raw {
				total += len(r.Data)
			}
		}
	}
	return total
}

func locateClip(st *State, clipID string) (ti, ci int, ok bool) {
	for i := range st.Tracks {
		if ci, found := findClip(&st.Tracks[i], clipID); found {
			return i, ci, true
		}
	}
	return 0, 0, false
}

func sortClips(t *Track) {
	sort.SliceStable(t.Clips, func(i, j int) bool { return t.Clips[i].Start < t.Clips[j].Start })
}

func normalizeTempo(points []TempoPoint) ([]TempoPoint, error) {
	for _, p := range points {
		if p.Tick < 0 || p.Tick > maxTick {
			return nil, fmt.Errorf("速度表 tick 超界：%d", p.Tick)
		}
		if p.USPerQuarter < 1 || p.USPerQuarter > 0xFFFFFF {
			return nil, fmt.Errorf("速度值超出支持范围（微秒/四分 1–16777215）：%d", p.USPerQuarter)
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].Tick < points[j].Tick })
	if len(points) == 0 || points[0].Tick != 0 {
		return nil, fmt.Errorf("速度表必须以 tick 0 开始")
	}
	out := points[:0]
	for i, p := range points {
		if i+1 < len(points) && points[i+1].Tick == p.Tick {
			continue // same-tick duplicates keep the last one
		}
		out = append(out, p)
	}
	return out, nil
}

func normalizeMeter(points []MeterPoint) ([]MeterPoint, error) {
	for _, p := range points {
		if p.Tick < 0 || p.Tick > maxTick {
			return nil, fmt.Errorf("拍号表 tick 超界：%d", p.Tick)
		}
		if p.Numerator < 1 || p.Numerator > 255 {
			return nil, fmt.Errorf("拍号分子超出支持范围 1–255：%d", p.Numerator)
		}
		d := p.Denominator
		if d < 1 || d > 128 || d&(d-1) != 0 {
			return nil, fmt.Errorf("拍号分母必须是 2 的幂（1–128）：%d", d)
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].Tick < points[j].Tick })
	if len(points) == 0 || points[0].Tick != 0 {
		return nil, fmt.Errorf("拍号表必须以 tick 0 开始")
	}
	out := points[:0]
	for i, p := range points {
		if i+1 < len(points) && points[i+1].Tick == p.Tick {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// applyOp applies one operation to the state in place and returns its
// inverse (plus the ids it created). On error the caller discards the whole
// working copy, so partial application never reaches storage.
func applyOp(st *State, ids map[string]bool, op Op) (inv Op, created []string, err error) {
	switch op.Kind {
	case "project.rename":
		if len([]rune(op.Title)) > 200 {
			return inv, nil, opError(op.Kind, "工程名过长")
		}
		inv = Op{Kind: op.Kind, Title: st.Title}
		st.Title = op.Title
		return inv, nil, nil

	case "tempo.set":
		points, err := normalizeTempo(append([]TempoPoint{}, op.Tempo...))
		if err != nil {
			return inv, nil, opError(op.Kind, "%s", err)
		}
		inv = Op{Kind: op.Kind, Tempo: append([]TempoPoint{}, st.Tempo...)}
		st.Tempo = points
		return inv, nil, nil

	case "meter.set":
		points, err := normalizeMeter(append([]MeterPoint{}, op.Meter...))
		if err != nil {
			return inv, nil, opError(op.Kind, "%s", err)
		}
		inv = Op{Kind: op.Kind, Meter: append([]MeterPoint{}, st.Meter...)}
		st.Meter = points
		return inv, nil, nil

	case "track.add":
		if op.Track == nil {
			return inv, nil, opError(op.Kind, "缺少 track")
		}
		if err := trackCount(op.Kind, st, 1); err != nil {
			return inv, nil, err
		}
		id := op.Track.ID
		if id == "" {
			id = NewID("t")
		} else if err := checkID(op.Kind, id); err != nil {
			return inv, nil, err
		}
		if ids[id] {
			return inv, nil, opError(op.Kind, "对象 ID 已存在：%s", id)
		}
		if err := validateTrackValues(op.Kind, op.Track.Channel, op.Track.Program); err != nil {
			return inv, nil, err
		}
		if !ValidColor(op.Track.Color) {
			return inv, nil, opError(op.Kind, "颜色必须是调色板值或空：%q", op.Track.Color)
		}
		ids[id] = true
		track := Track{
			ID: id, Name: op.Track.Name, Color: op.Track.Color, Channel: op.Track.Channel,
			Program: op.Track.Program, Muted: op.Track.Muted, Solo: op.Track.Solo,
			Clips: op.InvClips,
		}
		if track.Clips == nil {
			track.Clips = []Clip{}
		}
		for _, c := range track.Clips {
			registerClipIDs(ids, c)
		}
		st.Tracks = append(st.Tracks, track)
		return Op{Kind: "track.remove", TrackID: id}, []string{id}, nil

	case "track.update":
		i, ok := findTrack(st, op.TrackID)
		if !ok {
			return inv, nil, opError(op.Kind, "音轨不存在：%s", op.TrackID)
		}
		p := op.TrackPatch
		if p == nil || (p.Name == nil && p.Color == nil && p.Channel == nil && p.Program == nil && p.Muted == nil && p.Solo == nil) {
			return inv, nil, opError(op.Kind, "缺少要更新的字段")
		}
		if p.Color != nil && !ValidColor(*p.Color) {
			return inv, nil, opError(op.Kind, "颜色必须是调色板值或空：%q", *p.Color)
		}
		if p.Channel != nil || p.Program != nil {
			ch, pr := st.Tracks[i].Channel, st.Tracks[i].Program
			if p.Channel != nil {
				ch = *p.Channel
			}
			if p.Program != nil {
				pr = *p.Program
			}
			if err := validateTrackValues(op.Kind, ch, pr); err != nil {
				return inv, nil, err
			}
		}
		inv = Op{Kind: op.Kind, TrackID: op.TrackID, TrackPatch: &TrackPatch{}}
		t := &st.Tracks[i]
		if p.Name != nil {
			old := t.Name
			inv.TrackPatch.Name = &old
			t.Name = *p.Name
		}
		if p.Color != nil {
			old := t.Color
			inv.TrackPatch.Color = &old
			t.Color = *p.Color
		}
		if p.Channel != nil {
			old := t.Channel
			inv.TrackPatch.Channel = &old
			t.Channel = *p.Channel
		}
		if p.Program != nil {
			old := t.Program
			inv.TrackPatch.Program = &old
			t.Program = *p.Program
		}
		if p.Muted != nil {
			old := t.Muted
			inv.TrackPatch.Muted = &old
			t.Muted = *p.Muted
		}
		if p.Solo != nil {
			old := t.Solo
			inv.TrackPatch.Solo = &old
			t.Solo = *p.Solo
		}
		return inv, nil, nil

	case "track.remove":
		i, ok := findTrack(st, op.TrackID)
		if !ok {
			return inv, nil, opError(op.Kind, "音轨不存在：%s", op.TrackID)
		}
		removed := st.Tracks[i]
		for _, c := range removed.Clips {
			dropClipIDs(ids, c)
		}
		delete(ids, removed.ID)
		st.Tracks = append(st.Tracks[:i], st.Tracks[i+1:]...)
		inv = Op{Kind: "track.add", Track: &TrackSpec{
			ID: removed.ID, Name: removed.Name, Color: removed.Color, Channel: removed.Channel,
			Program: removed.Program, Muted: removed.Muted, Solo: removed.Solo,
		}, InvClips: removed.Clips}
		return inv, nil, nil

	case "clip.add":
		if op.Clip == nil {
			return inv, nil, opError(op.Kind, "缺少 clip")
		}
		ti, ok := findTrack(st, op.Clip.TrackID)
		if !ok {
			return inv, nil, opError(op.Kind, "音轨不存在：%s", op.Clip.TrackID)
		}
		id := op.Clip.ID
		if id == "" {
			id = NewID("c")
		} else if err := checkID(op.Kind, id); err != nil {
			return inv, nil, err
		}
		if ids[id] {
			return inv, nil, opError(op.Kind, "对象 ID 已存在：%s", id)
		}
		if op.Clip.Length < 1 {
			return inv, nil, opError(op.Kind, "片段长度必须 ≥1 tick，收到 %d", op.Clip.Length)
		}
		if op.Clip.Start < 0 {
			return inv, nil, opError(op.Kind, "片段起点不能为负")
		}
		if !ValidColor(op.Clip.Color) {
			return inv, nil, opError(op.Kind, "颜色必须是调色板值或空：%q", op.Clip.Color)
		}
		ids[id] = true
		clip := Clip{
			ID: id, Name: op.Clip.Name, Color: op.Clip.Color, Start: op.Clip.Start, Length: op.Clip.Length,
			Notes: op.InvNotes, Controls: op.InvControls, Raw: op.InvRaw,
		}
		if clip.Notes == nil {
			clip.Notes = []Note{}
		}
		if clip.Controls == nil {
			clip.Controls = []ControlEvent{}
		}
		if clip.Raw == nil {
			clip.Raw = []RawEvent{}
		}
		registerClipIDs(ids, clip)
		t := &st.Tracks[ti]
		t.Clips = append(t.Clips, clip)
		sortClips(t)
		return Op{Kind: "clip.remove", ClipID: id}, []string{id}, nil

	case "clip.update":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		p := op.ClipPatch
		if p == nil || (p.Name == nil && p.Color == nil && p.Start == nil && p.Length == nil) {
			return inv, nil, opError(op.Kind, "缺少要更新的字段")
		}
		if p.Color != nil && !ValidColor(*p.Color) {
			return inv, nil, opError(op.Kind, "颜色必须是调色板值或空：%q", *p.Color)
		}
		c := &st.Tracks[ti].Clips[ci]
		newLen := c.Length
		if p.Length != nil {
			if *p.Length < 1 {
				return inv, nil, opError(op.Kind, "片段长度必须 ≥1 tick，收到 %d", *p.Length)
			}
			newLen = *p.Length
		}
		if p.Start != nil && *p.Start < 0 {
			return inv, nil, opError(op.Kind, "片段起点不能为负")
		}
		if p.Length != nil {
			probe := *c
			probe.Length = newLen
			if err := clipFitCheck(op.Kind, &probe); err != nil {
				return inv, nil, err
			}
		}
		inv = Op{Kind: op.Kind, ClipID: op.ClipID, ClipPatch: &ClipPatch{}}
		if p.Name != nil {
			old := c.Name
			inv.ClipPatch.Name = &old
			c.Name = *p.Name
		}
		if p.Color != nil {
			old := c.Color
			inv.ClipPatch.Color = &old
			c.Color = *p.Color
		}
		if p.Start != nil {
			old := c.Start
			inv.ClipPatch.Start = &old
			c.Start = *p.Start
		}
		if p.Length != nil {
			old := c.Length
			inv.ClipPatch.Length = &old
			c.Length = newLen
		}
		return inv, nil, nil

	case "clip.remove":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		t := &st.Tracks[ti]
		removed := t.Clips[ci]
		dropClipIDs(ids, removed)
		t.Clips = append(t.Clips[:ci], t.Clips[ci+1:]...)
		inv = Op{Kind: "clip.add", Clip: &ClipSpec{
			ID: removed.ID, TrackID: t.ID, Name: removed.Name, Color: removed.Color,
			Start: removed.Start, Length: removed.Length,
		}, InvNotes: removed.Notes, InvControls: removed.Controls, InvRaw: removed.Raw}
		return inv, nil, nil

	case "note.add":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		if len(op.Notes) == 0 {
			return inv, nil, opError(op.Kind, "缺少要添加的音符")
		}
		if err := noteCount(op.Kind, st, len(op.Notes)); err != nil {
			return inv, nil, err
		}
		c := &st.Tracks[ti].Clips[ci]
		created = make([]string, 0, len(op.Notes))
		invNotes := make([]Note, 0, len(op.Notes))
		for _, n := range op.Notes {
			if err := validateNoteValues(op.Kind, n); err != nil {
				return inv, nil, err
			}
			if n.Start+n.Duration > c.Length {
				return inv, nil, opError(op.Kind, "音符超出片段长度 %d", c.Length)
			}
			id := n.ID
			if id == "" {
				id = NewID("n")
			}
			if ids[id] {
				return inv, nil, opError(op.Kind, "对象 ID 已存在：%s", id)
			}
			ids[id] = true
			created = append(created, id)
			invNotes = append(invNotes, Note{ID: id, Start: n.Start, Duration: n.Duration, Pitch: n.Pitch, Velocity: n.Velocity, Channel: n.Channel})
		}
		c.Notes = append(c.Notes, invNotes...)
		return Op{Kind: "note.remove", ClipID: op.ClipID, NoteIDs: created}, created, nil

	case "note.update":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		c := &st.Tracks[ti].Clips[ci]
		if len(op.NoteDeltas) == 0 {
			return inv, nil, opError(op.Kind, "缺少要更新的音符")
		}
		for _, d := range op.NoteDeltas {
			if _, ok := findNote(c, d.ID); !ok {
				return inv, nil, opError(op.Kind, "音符不存在：%s", d.ID)
			}
			if d.Start != nil && *d.Start < 0 {
				return inv, nil, opError(op.Kind, "音符起点不能为负")
			}
			if d.Duration != nil && *d.Duration < 1 {
				return inv, nil, opError(op.Kind, "音符时长必须 ≥1 tick，收到 %d", *d.Duration)
			}
			if d.Pitch != nil && (*d.Pitch < 0 || *d.Pitch > 127) {
				return inv, nil, opError(op.Kind, "音高必须在 0–127，收到 %d", *d.Pitch)
			}
			if d.Velocity != nil && (*d.Velocity < 1 || *d.Velocity > 127) {
				return inv, nil, opError(op.Kind, "力度必须在 1–127，收到 %d", *d.Velocity)
			}
		}
		inv = Op{Kind: op.Kind, ClipID: op.ClipID, NoteDeltas: make([]NoteDelta, 0, len(op.NoteDeltas))}
		for _, d := range op.NoteDeltas {
			ni, _ := findNote(c, d.ID)
			n := &c.Notes[ni]
			idelta := NoteDelta{ID: d.ID}
			if d.Start != nil {
				old := n.Start
				idelta.Start = &old
				n.Start = *d.Start
			}
			if d.Duration != nil {
				old := n.Duration
				idelta.Duration = &old
				n.Duration = *d.Duration
			}
			if d.Pitch != nil {
				old := n.Pitch
				idelta.Pitch = &old
				n.Pitch = *d.Pitch
			}
			if d.Velocity != nil {
				old := n.Velocity
				idelta.Velocity = &old
				n.Velocity = *d.Velocity
			}
			inv.NoteDeltas = append(inv.NoteDeltas, idelta)
		}
		// Deltas can update the same note repeatedly; restore their prior
		// values in the opposite order, just like the enclosing batch.
		slices.Reverse(inv.NoteDeltas)
		if err := clipFitCheck(op.Kind, c); err != nil {
			return inv, nil, err
		}
		return inv, nil, nil

	case "note.remove":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		c := &st.Tracks[ti].Clips[ci]
		if len(op.NoteIDs) == 0 {
			return inv, nil, opError(op.Kind, "缺少要删除的音符")
		}
		removed := make([]Note, 0, len(op.NoteIDs))
		for _, id := range op.NoteIDs {
			ni, ok := findNote(c, id)
			if !ok {
				return inv, nil, opError(op.Kind, "音符不存在：%s", id)
			}
			removed = append(removed, c.Notes[ni])
		}
		inv = Op{Kind: "note.add", ClipID: op.ClipID, Notes: make([]NewNote, 0, len(removed))}
		for _, n := range removed {
			inv.Notes = append(inv.Notes, NewNote{ID: n.ID, Start: n.Start, Duration: n.Duration, Pitch: n.Pitch, Velocity: n.Velocity, Channel: n.Channel})
		}
		keep := c.Notes[:0]
		for _, n := range c.Notes {
			drop := false
			for _, id := range op.NoteIDs {
				if n.ID == id {
					drop = true
					break
				}
			}
			if drop {
				delete(ids, n.ID)
				continue
			}
			keep = append(keep, n)
		}
		c.Notes = keep
		return inv, nil, nil

	case "control.add":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		if len(op.Events) == 0 {
			return inv, nil, opError(op.Kind, "缺少要添加的事件")
		}
		c := &st.Tracks[ti].Clips[ci]
		created = make([]string, 0, len(op.Events))
		invEvents := make([]ControlEvent, 0, len(op.Events))
		for _, e := range op.Events {
			if err := validateEventValues(op.Kind, e); err != nil {
				return inv, nil, err
			}
			if e.Tick >= c.Length {
				return inv, nil, opError(op.Kind, "事件超出片段长度 %d", c.Length)
			}
			id := e.ID
			if id == "" {
				id = NewID("e")
			}
			if ids[id] {
				return inv, nil, opError(op.Kind, "对象 ID 已存在：%s", id)
			}
			ids[id] = true
			created = append(created, id)
			invEvents = append(invEvents, ControlEvent{ID: id, Tick: e.Tick, Kind: e.Kind, Channel: e.Channel, Controller: e.Controller, Value: e.Value, Program: e.Program, Note: e.Note})
		}
		c.Controls = append(c.Controls, invEvents...)
		return Op{Kind: "control.remove", ClipID: op.ClipID, EventIDs: created}, created, nil

	case "control.remove":
		ti, ci, ok := locateClip(st, op.ClipID)
		if !ok {
			return inv, nil, opError(op.Kind, "片段不存在：%s", op.ClipID)
		}
		c := &st.Tracks[ti].Clips[ci]
		if len(op.EventIDs) == 0 {
			return inv, nil, opError(op.Kind, "缺少要删除的事件")
		}
		removed := make([]ControlEvent, 0, len(op.EventIDs))
		for _, id := range op.EventIDs {
			ei, ok := findControl(c, id)
			if !ok {
				return inv, nil, opError(op.Kind, "事件不存在：%s", id)
			}
			removed = append(removed, c.Controls[ei])
		}
		inv = Op{Kind: "control.add", ClipID: op.ClipID, Events: make([]EventSpec, 0, len(removed))}
		for _, e := range removed {
			inv.Events = append(inv.Events, EventSpec{ID: e.ID, Tick: e.Tick, Kind: e.Kind, Channel: e.Channel, Controller: e.Controller, Value: e.Value, Program: e.Program, Note: e.Note})
		}
		keep := c.Controls[:0]
		for _, e := range c.Controls {
			drop := false
			for _, id := range op.EventIDs {
				if e.ID == id {
					drop = true
					break
				}
			}
			if drop {
				delete(ids, e.ID)
				continue
			}
			keep = append(keep, e)
		}
		c.Controls = keep
		return inv, nil, nil

	default:
		return inv, nil, opError(op.Kind, "不支持的操作类型")
	}
}
