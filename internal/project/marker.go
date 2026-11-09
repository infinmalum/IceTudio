package project

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MarkerName recognizes standard SMF marker meta events, including those
// imported as raw events. Non-marker bytes are left untouched.
func MarkerName(data []byte) (string, bool) {
	if len(data) < 3 || data[0] != 0xff || data[1] != 6 {
		return "", false
	}
	length := 0
	for i := 2; i < len(data) && i < 6; i++ {
		length = length<<7 | int(data[i]&127)
		if data[i]&128 == 0 {
			if length != len(data)-i-1 {
				return "", false
			}
			return strings.ToValidUTF8(string(data[i+1:]), "�"), true
		}
	}
	return "", false
}

func applyMarker(st *State, ids map[string]bool, op Op) (Op, []string, error) {
	ti, ci, ok := locateClip(st, op.ClipID)
	if !ok {
		return Op{}, nil, opError(op.Kind, "片段不存在")
	}
	clip := &st.Tracks[ti].Clips[ci]
	if op.Kind == "marker.remove" {
		if len(op.EventIDs) != 1 {
			return Op{}, nil, opError(op.Kind, "需要一个标记")
		}
		for i, event := range clip.Raw {
			if event.ID != op.EventIDs[0] {
				continue
			}
			if _, valid := MarkerName(event.Data); !valid {
				break
			}
			inv := Op{Kind: "clip.restore_arrangement", InvArrangement: []ArrangementTrack{{st.Tracks[ti].ID, snapshotClips(st.Tracks[ti].Clips)}}}
			clip.Raw = append(append([]RawEvent{}, clip.Raw[:i]...), clip.Raw[i+1:]...)
			delete(ids, event.ID)
			return inv, nil, nil
		}
		return Op{}, nil, opError(op.Kind, "标记不存在")
	}
	m := op.Marker
	if m == nil || m.Tick < 0 || m.Tick >= clip.Length || len(m.Name) > 127 || strings.TrimSpace(m.Name) == "" || !utf8.ValidString(m.Name) {
		return Op{}, nil, fmt.Errorf("标记需位于片段内，名称 1–127 UTF-8 字节")
	}
	id := m.ID
	if id == "" {
		id = NewID("mark")
	}
	if !ValidID(id) || ids[id] {
		return Op{}, nil, opError(op.Kind, "标记 ID 无效或已使用")
	}
	data := append([]byte{0xff, 6, byte(len(m.Name))}, m.Name...)
	clip.Raw = append(clip.Raw, RawEvent{ID: id, Tick: m.Tick, Data: data})
	ids[id] = true
	return Op{Kind: "marker.remove", ClipID: clip.ID, EventIDs: []string{id}}, []string{id}, nil
}

type MarkerSpec struct {
	ID   string `json:"id,omitempty"`
	Tick int64  `json:"tick"`
	Name string `json:"name"`
}

const MaxControlEditPoints = 128

func applyControlUpdate(st *State, op Op) (Op, []string, error) {
	ti, ci, ok := locateClip(st, op.ClipID)
	if !ok || len(op.Events) < 1 || len(op.Events) > MaxControlEditPoints {
		return Op{}, nil, opError(op.Kind, "需要片段和 1–128 个控制事件")
	}
	clip := &st.Tracks[ti].Clips[ci]
	inverse := Op{Kind: op.Kind, ClipID: op.ClipID}
	seen := map[string]bool{}
	for _, event := range op.Events {
		i, ok := findControl(clip, event.ID)
		if !ok || seen[event.ID] || event.Tick >= clip.Length {
			return Op{}, nil, opError(op.Kind, "事件不存在、重复或超出片段")
		}
		if err := validateEventValues(op.Kind, event); err != nil {
			return Op{}, nil, err
		}
		seen[event.ID] = true
		inverse.Events = append(inverse.Events, EventSpec(clip.Controls[i]))
		clip.Controls[i] = ControlEvent(event)
	}
	return inverse, nil, nil
}
