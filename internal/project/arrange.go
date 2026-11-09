package project

import (
	"crypto/sha256"
	"fmt"
	"slices"
)

// ArrangementEdit addresses existing clips by stable ID. Tick and End are
// absolute. Move uses Tick as the new earliest selected start. Copy repeats
// the entire selected time span starting at Tick.
type ArrangementEdit struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
	Track  string   `json:"track,omitempty"`
	Tick   int64    `json:"tick"`
	End    int64    `json:"end,omitempty"`
	Repeat int      `json:"repeat,omitempty"`
	Seed   string   `json:"seed,omitempty"`
}

type ArrangementTrack struct {
	ID    string `json:"id"`
	Clips []Clip `json:"clips"`
}

type locatedClip struct {
	track int
	clip  Clip
}

func applyArrangement(st *State, ids map[string]bool, op Op) (Op, []string, error) {
	bad := func(s string) (Op, []string, error) { return Op{}, nil, opError(op.Kind, "%s", s) }
	p := op.Arrangement
	if p == nil || len(p.IDs) == 0 || len(p.IDs) > 64 || p.Tick < 0 || p.Tick > 2*MaxTick || p.End < 0 || p.End > 2*MaxTick || !ValidID(p.Seed) {
		return bad("需要 1–64 个片段、有效的时间与编辑标识")
	}
	sources := make([]locatedClip, 0, len(p.IDs))
	seen := map[string]bool{}
	touched := map[int]bool{}
	first, last := int64(2*MaxTick), int64(0)
	for _, id := range p.IDs {
		ti, ci, ok := locateClip(st, id)
		if !ok || seen[id] {
			return bad("片段不存在或重复：" + id)
		}
		c := st.Tracks[ti].Clips[ci]
		if c.Start < 0 || c.Start > MaxTick || c.Length < 1 || c.Length > MaxTick {
			return bad("源片段超出支持的时间范围")
		}
		seen[id], touched[ti] = true, true
		if c.PatternID != "" && (p.Action == "crop" || p.Action == "split" || p.Action == "merge") {
			return bad("请先解除共享 Pattern 关联")
		}
		sources = append(sources, locatedClip{ti, c})
		first, last = min(first, c.Start), max(last, c.Start+c.Length)
	}
	dest := -1
	if p.Track != "" {
		var ok bool
		dest, ok = findTrack(st, p.Track)
		if !ok || (p.Action != "move" && p.Action != "copy" && p.Action != "link") {
			return bad("目标轨无效")
		}
		touched[dest] = true
	}
	var created []string
	allocate := func() string {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", p.Seed, len(created))))
		id := fmt.Sprintf("arr_%x", sum[:12])
		created = append(created, id)
		return id
	}
	var out []locatedClip
	switch p.Action {
	case "move", "copy", "link":
		count := 1
		if p.Action != "move" {
			count = p.Repeat
			if count < 1 || count > 64 || count*len(sources) > 256 {
				return bad("铺排需要 1–64 次，最多产生 256 个片段")
			}
			notes, rawBytes, events := 0, 0, 0
			for _, src := range sources {
				notes += len(src.clip.Notes)
				events += len(src.clip.Controls) + len(src.clip.Raw)
				for _, r := range src.clip.Raw {
					rawBytes += len(r.Data)
				}
			}
			if err := noteCount(op.Kind, st, notes*count); err != nil {
				return Op{}, nil, err
			}
			if events*count > MaxNotes || rawByteCount(st)+rawBytes*count > MaxRawBytes {
				return bad("铺排事件或原始数据超出上限")
			}
		}
		if p.Action == "link" {
			if len(sources) != 1 {
				return bad("共享铺排需要一个源片段")
			}
			if max(1, len(PatternMembers(st, sources[0].clip.ID)))+count > 256 {
				return bad("共享 Pattern 最多 256 个实例")
			}
			if sources[0].clip.PatternID == "" {
				sources[0].clip.PatternID = allocate()
			}
		}
		for i := 0; i < count; i++ {
			for _, src := range sources {
				c := src.clip
				c.Start = p.Tick + c.Start - first + int64(i)*(last-first)
				if p.Action != "move" {
					c.ID = allocate()
					c.GroupID = ""
					if p.Action == "copy" {
						c.PatternID = ""
					}
					c.Notes = slices.Clone(c.Notes)
					c.Controls = slices.Clone(c.Controls)
					c.Raw = slices.Clone(c.Raw)
					for j := range c.Notes {
						c.Notes[j].ID = allocate()
					}
					for j := range c.Controls {
						c.Controls[j].ID = allocate()
					}
					for j := range c.Raw {
						c.Raw[j].ID = allocate()
					}
				}
				track := src.track
				if dest >= 0 {
					track = dest
				}
				out = append(out, locatedClip{track, c})
			}
		}
	case "makePattern":
		if len(sources) != 1 || sources[0].clip.PatternID != "" {
			return bad("请选中一个独立片段")
		}
		sources[0].clip.PatternID = allocate()
		out = append(out, sources[0])
	case "group", "ungroup":
		group := ""
		if p.Action == "group" {
			if len(sources) < 2 {
				return bad("分组需要至少两个片段")
			}
			group = allocate()
		}
		for _, src := range sources {
			src.clip.GroupID = group
			out = append(out, src)
		}
	case "detach":
		for _, src := range sources {
			src.clip.PatternID = ""
			out = append(out, src)
		}
	case "split", "crop":
		if len(sources) != 1 {
			return bad("拆分或裁剪需要选中一个片段")
		}
		src := sources[0]
		if p.Action == "split" {
			if p.Tick <= first || p.Tick >= last {
				return bad("拆分点必须位于片段内部")
			}
			left := sliceArrangementClip(src.clip, first, p.Tick)
			right := sliceArrangementClip(src.clip, p.Tick, last)
			right.ID = allocate()
			right.GroupID = ""
			leftIDs := make(map[string]bool, len(left.Notes))
			for _, n := range left.Notes {
				leftIDs[n.ID] = true
			}
			for i := range right.Notes {
				if leftIDs[right.Notes[i].ID] {
					right.Notes[i].ID = allocate()
				}
			}
			out = []locatedClip{{src.track, left}, {src.track, right}}
		} else {
			if p.Tick < first || p.End > last || p.End <= p.Tick {
				return bad("保留范围必须位于片段内")
			}
			out = []locatedClip{{src.track, sliceArrangementClip(src.clip, p.Tick, p.End)}}
		}
	case "merge":
		if len(sources) < 2 {
			return bad("合并需要至少两个片段")
		}
		slices.SortStableFunc(sources, func(a, b locatedClip) int {
			if a.clip.Start < b.clip.Start {
				return -1
			}
			if a.clip.Start > b.clip.Start {
				return 1
			}
			return 0
		})
		track := sources[0].track
		c := sources[0].clip
		c.Start, c.Length = first, last-first
		c.Notes, c.Controls, c.Raw = []Note{}, []ControlEvent{}, []RawEvent{}
		for _, src := range sources {
			if track != src.track {
				return bad("只可合并同一轨道上的片段")
			}
			offset := src.clip.Start - first
			for _, n := range src.clip.Notes {
				n.Start += offset
				c.Notes = append(c.Notes, n)
			}
			for _, e := range src.clip.Controls {
				e.Tick += offset
				c.Controls = append(c.Controls, e)
			}
			for _, r := range src.clip.Raw {
				r.Tick += offset
				c.Raw = append(c.Raw, r)
			}
		}
		out = []locatedClip{{track, c}}
	default:
		return bad("未知片段操作")
	}

	for _, id := range created {
		if ids[id] {
			return bad("编辑标识已使用")
		}
	}
	delta := 0
	for _, c := range out {
		if c.clip.Start > MaxTick || c.clip.Length > MaxTick {
			return bad("输出超出支持的时间范围")
		}
		delta += len(c.clip.Notes)
	}
	if p.Action != "copy" && p.Action != "link" {
		for _, src := range sources {
			delta -= len(src.clip.Notes)
		}
	}
	if err := noteCount(op.Kind, st, delta); err != nil {
		return Op{}, nil, err
	}
	inv := Op{Kind: "clip.restore_arrangement"}
	if (p.Action == "makePattern" || p.Action == "link") && findPattern(st, sources[0].clip.PatternID) < 0 {
		inv.InvPatterns = patternSnapshot(st)
		src := sources[0].clip
		st.Patterns = append(st.Patterns, Pattern{ID: src.PatternID, Name: src.Name, Body: patternBody(src)})
	}
	for i := range st.Tracks {
		if !touched[i] {
			continue
		}
		t := &st.Tracks[i]
		inv.InvArrangement = append(inv.InvArrangement, ArrangementTrack{t.ID, snapshotClips(t.Clips)})
		t.Clips = slices.Clone(t.Clips)
		if p.Action != "copy" && p.Action != "link" {
			t.Clips = slices.DeleteFunc(t.Clips, func(c Clip) bool {
				if !seen[c.ID] {
					return false
				}
				dropClipIDs(ids, c)
				return true
			})
		}
		if p.Action == "link" && sources[0].track == i {
			for ci := range t.Clips {
				if t.Clips[ci].ID == sources[0].clip.ID {
					t.Clips[ci].PatternID = sources[0].clip.PatternID
				}
			}
		}
		for _, c := range out {
			if c.track == i {
				t.Clips = append(t.Clips, c.clip)
				registerClipIDs(ids, c.clip)
			}
		}
		sortClips(t)
	}
	return inv, created, nil
}

func sliceArrangementClip(source Clip, first, last int64) Clip {
	c := source
	c.Start, c.Length = first, last-first
	c.Notes, c.Controls, c.Raw = []Note{}, []ControlEvent{}, []RawEvent{}
	for _, n := range source.Notes {
		start := max(first, source.Start+n.Start)
		end := min(last, source.Start+n.Start+n.Duration)
		if start < end {
			n.Start, n.Duration = start-first, end-start
			c.Notes = append(c.Notes, n)
		}
	}
	for _, e := range source.Controls {
		if tick := source.Start + e.Tick; first <= tick && tick < last {
			e.Tick = tick - first
			c.Controls = append(c.Controls, e)
		}
	}
	for _, r := range source.Raw {
		if tick := source.Start + r.Tick; first <= tick && tick < last {
			r.Tick = tick - first
			c.Raw = append(c.Raw, r)
		}
	}
	return c
}

func restoreArrangement(st *State, ids map[string]bool, op Op) (Op, []string, error) {
	inv := Op{Kind: op.Kind}
	if op.InvPatterns != nil {
		inv.InvPatterns = patternSnapshot(st)
		st.Patterns = append([]Pattern(nil), (*op.InvPatterns)...)
		for _, p := range st.Patterns {
			ids[p.ID] = true
		}
	}
	for _, saved := range op.InvArrangement {
		i, ok := findTrack(st, saved.ID)
		if !ok {
			return Op{}, nil, opError(op.Kind, "恢复轨道不存在")
		}
		t := &st.Tracks[i]
		inv.InvArrangement = append(inv.InvArrangement, ArrangementTrack{t.ID, snapshotClips(t.Clips)})
		for _, c := range t.Clips {
			dropClipIDs(ids, c)
		}
		t.Clips = snapshotClips(saved.Clips)
	}
	for _, saved := range op.InvArrangement {
		for _, c := range saved.Clips {
			registerClipIDs(ids, c)
		}
	}
	return inv, nil, nil
}

// Clip edits can be followed by note/control edits in the same transaction.
// Keep history independent of the mutable event slices used by later ops.
func snapshotClips(clips []Clip) []Clip {
	out := slices.Clone(clips)
	for i := range out {
		out[i].Notes = slices.Clone(out[i].Notes)
		out[i].Controls = slices.Clone(out[i].Controls)
		out[i].Raw = slices.Clone(out[i].Raw)
	}
	return out
}
