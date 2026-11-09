package project

import (
	"crypto/sha256"
	"fmt"
	"slices"
)

type PatternInstance struct {
	TrackID, TrackName, ClipID, ClipName string
	Start                                int64
}

func PatternMembers(st *State, clipID string) []PatternInstance {
	ti, ci, ok := locateClip(st, clipID)
	if !ok || st.Tracks[ti].Clips[ci].PatternID == "" {
		return nil
	}
	group := st.Tracks[ti].Clips[ci].PatternID
	var out []PatternInstance
	for _, t := range st.Tracks {
		for _, c := range t.Clips {
			if c.PatternID == group {
				out = append(out, PatternInstance{t.ID, t.Name, c.ID, c.Name, c.Start})
			}
		}
	}
	return out
}

func ChangesPatternBody(op Op) bool {
	switch op.Kind {
	case "note.add", "note.update", "note.remove", "control.add", "control.update", "control.remove", "marker.add", "marker.remove":
		return true
	case "clip.update":
		return op.ClipPatch != nil && op.ClipPatch.Length != nil
	}
	return false
}

func applyWithPattern(st *State, ids map[string]bool, op Op) (Op, Op, []string, error) {
	if !ChangesPatternBody(op) {
		inv, created, err := applyOp(st, ids, op)
		return withCanonicalIDs(op, created, err), inv, created, err
	}
	members := PatternMembers(st, op.ClipID)
	if len(members) == 0 {
		inv, created, err := applyOp(st, ids, op)
		return withCanonicalIDs(op, created, err), inv, created, err
	}
	ti, ci, _ := locateClip(st, op.ClipID)
	index := findPattern(st, st.Tracks[ti].Clips[ci].PatternID)
	if index < 0 {
		return Op{}, Op{}, nil, opError(op.Kind, "共享 Pattern 资源不存在")
	}
	original := snapshotClips(st.Tracks[ti].Clips[ci : ci+1])[0]
	inverse := Op{Kind: "clip.restore_arrangement", InvPatterns: patternSnapshot(st)}
	touched := map[string]bool{}
	for _, instance := range members {
		if !touched[instance.TrackID] {
			t, _ := findTrack(st, instance.TrackID)
			inverse.InvArrangement = append(inverse.InvArrangement, ArrangementTrack{instance.TrackID, snapshotClips(st.Tracks[t].Clips)})
			touched[instance.TrackID] = true
		}
	}
	_, created, err := applyOp(st, ids, op)
	if err != nil {
		return Op{}, Op{}, nil, err
	}
	canonical := withCreatedIDs(op, created)
	after := st.Tracks[ti].Clips[ci]
	st.Patterns[index].Body = patternBody(after)
	for _, instance := range members {
		if instance.ClipID == op.ClipID {
			continue
		}
		t, c, _ := locateClip(st, instance.ClipID)
		target := &st.Tracks[t].Clips[c]
		if len(original.Notes) != len(target.Notes) || len(original.Controls) != len(target.Controls) || len(original.Raw) != len(target.Raw) || original.Length != target.Length {
			return Op{}, Op{}, nil, opError(op.Kind, "共享 Pattern 内容不一致")
		}
		if err = noteCount(op.Kind, st, len(after.Notes)-len(original.Notes)); err != nil {
			return Op{}, Op{}, nil, err
		}
		replica := snapshotClips([]Clip{after})[0]
		replica.ID, replica.Name, replica.Color, replica.Start = target.ID, target.Name, target.Color, target.Start
		mapped := make(map[string]string, len(original.Notes)+len(original.Controls)+len(original.Raw))
		for i, n := range original.Notes {
			mapped[n.ID] = target.Notes[i].ID
		}
		for i, e := range original.Controls {
			mapped[e.ID] = target.Controls[i].ID
		}
		for i, r := range original.Raw {
			mapped[r.ID] = target.Raw[i].ID
		}
		assign := func(id string) (string, error) {
			if existing := mapped[id]; existing != "" {
				return existing, nil
			}
			sum := sha256.Sum256([]byte(target.ID + "/" + id))
			next := fmt.Sprintf("pat_%x", sum[:12])
			if ids[next] {
				return "", opError(op.Kind, "共享事件 ID 已存在")
			}
			created = append(created, next)
			ids[next] = true
			return next, nil
		}
		for i, n := range replica.Notes {
			if replica.Notes[i].ID, err = assign(n.ID); err != nil {
				return Op{}, Op{}, nil, err
			}
		}
		for i, e := range replica.Controls {
			if replica.Controls[i].ID, err = assign(e.ID); err != nil {
				return Op{}, Op{}, nil, err
			}
		}
		for i, r := range replica.Raw {
			if replica.Raw[i].ID, err = assign(r.ID); err != nil {
				return Op{}, Op{}, nil, err
			}
		}
		dropClipIDs(ids, *target)
		*target = replica
		registerClipIDs(ids, replica)
	}
	return canonical, inverse, created, nil
}

func withCanonicalIDs(op Op, ids []string, err error) Op {
	if err != nil {
		return op
	}
	return withCreatedIDs(op, ids)
}

// Pattern is a reusable asset. Its body contains values; stable event identity
// is assigned to each placement and maintained independently across edits.
type Pattern struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Body Clip   `json:"body"`
}

func patternBody(c Clip) Clip {
	body := snapshotClips([]Clip{c})[0]
	body.ID, body.PatternID, body.GroupID, body.Name, body.Color, body.Start = "", "", "", "", "", 0
	for i := range body.Notes {
		body.Notes[i].ID = ""
	}
	for i := range body.Controls {
		body.Controls[i].ID = ""
	}
	for i := range body.Raw {
		body.Raw[i].ID = ""
	}
	return body
}

func patternSnapshot(st *State) *[]Pattern {
	old := append([]Pattern{}, st.Patterns...)
	return &old
}

func findPattern(st *State, id string) int {
	return slices.IndexFunc(st.Patterns, func(p Pattern) bool { return p.ID == id })
}

func applyPatternAsset(st *State, ids map[string]bool, op Op) (Op, []string, error) {
	index := findPattern(st, op.PatternID)
	if index < 0 {
		return Op{}, nil, opError(op.Kind, "Pattern 资源不存在")
	}
	asset := st.Patterns[index]
	if op.Kind == "pattern.remove" {
		for _, t := range st.Tracks {
			for _, c := range t.Clips {
				if c.PatternID == asset.ID {
					return Op{}, nil, opError(op.Kind, "仍有编排实例引用此 Pattern")
				}
			}
		}
		inv := Op{Kind: "clip.restore_arrangement", InvPatterns: patternSnapshot(st)}
		st.Patterns = slices.Delete(st.Patterns, index, index+1)
		return inv, nil, nil
	}
	spec := op.Arrangement
	if spec == nil || spec.Tick < 0 || spec.Tick > MaxTick || !ValidID(spec.Seed) || spec.Repeat < 1 || spec.Repeat > 64 {
		return Op{}, nil, opError(op.Kind, "需要目标轨、位置和 1–64 次铺排")
	}
	ti, ok := findTrack(st, spec.Track)
	if !ok {
		return Op{}, nil, opError(op.Kind, "目标轨不存在")
	}
	members := 0
	for _, t := range st.Tracks {
		for _, c := range t.Clips {
			if c.PatternID == asset.ID {
				members++
			}
		}
	}
	if members+spec.Repeat > 256 || spec.Tick+int64(spec.Repeat-1)*asset.Body.Length > MaxTick {
		return Op{}, nil, opError(op.Kind, "Pattern 实例或位置超出上限")
	}
	if err := noteCount(op.Kind, st, spec.Repeat*len(asset.Body.Notes)); err != nil {
		return Op{}, nil, err
	}
	rawBytes := 0
	for _, r := range asset.Body.Raw {
		rawBytes += len(r.Data)
	}
	if (len(asset.Body.Controls)+len(asset.Body.Raw))*spec.Repeat > MaxNotes || rawByteCount(st)+rawBytes*spec.Repeat > MaxRawBytes {
		return Op{}, nil, opError(op.Kind, "Pattern 事件或原始数据超出上限")
	}
	t := &st.Tracks[ti]
	inverse := Op{Kind: "clip.restore_arrangement", InvArrangement: []ArrangementTrack{{t.ID, snapshotClips(t.Clips)}}}
	var created []string
	newID := func() string {
		sum := sha256.Sum256([]byte(fmt.Sprintf("place/%s/%d", spec.Seed, len(created))))
		id := fmt.Sprintf("pat_%x", sum[:12])
		created = append(created, id)
		return id
	}
	for i := 0; i < spec.Repeat; i++ {
		c := snapshotClips([]Clip{asset.Body})[0]
		c.ID, c.PatternID, c.Name, c.Start = newID(), asset.ID, asset.Name, spec.Tick+int64(i)*asset.Body.Length
		for j := range c.Notes {
			c.Notes[j].ID = newID()
		}
		for j := range c.Controls {
			c.Controls[j].ID = newID()
		}
		for j := range c.Raw {
			c.Raw[j].ID = newID()
		}
		if err := clipFitCheck(op.Kind, &c); err != nil {
			return Op{}, nil, err
		}
		t.Clips = append(t.Clips, c)
	}
	for _, id := range created {
		if ids[id] {
			return Op{}, nil, opError(op.Kind, "放置 ID 已使用")
		}
		ids[id] = true
	}
	sortClips(t)
	return inverse, created, nil
}
