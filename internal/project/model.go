// Package project holds the authoritative music project model: tracks,
// clips, notes, control/raw events, tempo and meter maps, plus the pure
// transaction semantics (validation, application, inverse ops, undo/redo)
// shared by the GUI, CLI and agent. It knows nothing about storage or Wails.
package project

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
)

// Event kinds for ControlEvent. Values match the design contract in
// docs/p1-project-model.md §3.3.
const (
	KindCC                = "cc"
	KindPitchBend         = "pitchBend"
	KindProgram           = "program"
	KindChannelAftertouch = "channelAftertouch"
	KindPolyAftertouch    = "polyAftertouch"
)

// History entry kinds.
const (
	EditKind = "edit"
	UndoKind = "undo"
	RedoKind = "redo"
)

// Note is a pitched note with integer tick positions inside a clip. The
// MIDI channel is a per-note property preserved on import; new notes
// default to their track's channel (the submitter fills it in).
type Note struct {
	ID       string `json:"id"`
	Start    int64  `json:"start"`
	Duration int64  `json:"duration"`
	Pitch    int    `json:"pitch"`
	Velocity int    `json:"velocity"`
	Channel  int    `json:"channel"`
}

// ControlEvent is a structured channel message (CC, bend, program,
// aftertouch) at a local tick inside a clip.
type ControlEvent struct {
	ID         string `json:"id"`
	Tick       int64  `json:"tick"`
	Kind       string `json:"kind"`
	Channel    int    `json:"channel"`
	Controller int    `json:"controller,omitempty"`
	Value      int    `json:"value,omitempty"`
	Program    int    `json:"program,omitempty"`
	Note       int    `json:"note,omitempty"`
}

// RawEvent preserves bytes the model does not structure: SysEx, unmatched
// note-offs, unknown or passthrough meta events. Export writes them back
// verbatim.
type RawEvent struct {
	ID   string `json:"id"`
	Tick int64  `json:"tick"`
	Data []byte `json:"data"`
}

// Clip is a region on a track holding local events. Clips may overlap;
// export orders by absolute tick with a stable tie-break.
type Clip struct {
	ID       string         `json:"id"`
	Name     string         `json:"name,omitempty"`
	Color    string         `json:"color,omitempty"` // 调色板枚举，空 = 未设置（P1 §11）
	Start    int64          `json:"start"`
	Length   int64          `json:"length"`
	Notes    []Note         `json:"notes"`
	Controls []ControlEvent `json:"controls"`
	Raw      []RawEvent     `json:"raw"`
}

// Palette 是 Track/Clip 颜色的限定调色板（P1 §11）；空串合法（未设置，显示层按轨道序回退）。
var Palette = []string{"ice", "mint", "violet", "amber", "coral", "rose", "slate", "moss"}

func ValidColor(c string) bool {
	if c == "" {
		return true
	}
	return slices.Contains(Palette, c)
}

// Track owns clips. The MIDI channel is routing metadata, not identity.
type Track struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Color   string `json:"color,omitempty"` // 调色板枚举，空 = 未设置（P1 §11）
	Channel int    `json:"channel"`
	Program int    `json:"program"`
	Muted   bool   `json:"muted,omitempty"`
	Solo    bool   `json:"solo,omitempty"`
	Clips   []Clip `json:"clips"`
}

// TempoPoint stores the exact SMF value (microseconds per quarter note).
type TempoPoint struct {
	Tick         int64 `json:"tick"`
	USPerQuarter int   `json:"usPerQuarter"`
}

// MeterPoint is a time signature change.
type MeterPoint struct {
	Tick        int64 `json:"tick"`
	Numerator   int   `json:"numerator"`
	Denominator int   `json:"denominator"`
}

// State is the serializable project content. The revision counter lives in
// the storage row, not here.
type State struct {
	Title  string       `json:"title"`
	PPQ    int          `json:"ppq"`
	Tempo  []TempoPoint `json:"tempo"`
	Meter  []MeterPoint `json:"meter"`
	Tracks []Track      `json:"tracks"`
}

// Resource limits from the P1 design (§3.4, §1).
const (
	MaxPPQ            = 32768
	MaxTracks         = 64
	MaxNotes          = 200000
	MaxStateJSONBytes = 64 << 20
	MaxRawBytes       = 8 << 20
	maxTick           = int64(1)<<31 - 1
)

// NewID returns a fresh stable object id: prefix + 12 hex chars.
func NewID(prefix string) string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// ValidID enforces the charset the design allows for client-supplied ids.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// ConvertTick rescales a tick between PPQ resolutions with round-half-up
// on the positive domain (design §2).
func ConvertTick(src int64, srcPPQ, dstPPQ int) (int64, error) {
	if srcPPQ < 1 || srcPPQ > MaxPPQ || dstPPQ < 1 || dstPPQ > MaxPPQ {
		return 0, fmt.Errorf("PPQ 超出支持范围 1–%d", MaxPPQ)
	}
	if src < 0 {
		return 0, fmt.Errorf("tick 不能为负")
	}
	return (src*int64(dstPPQ) + int64(srcPPQ)/2) / int64(srcPPQ), nil
}

func blankState(title string, ppq int) State {
	return State{
		Title: title,
		PPQ:   ppq,
		Tempo: []TempoPoint{{Tick: 0, USPerQuarter: 500000}},
		Meter: []MeterPoint{{Tick: 0, Numerator: 4, Denominator: 4}},
	}
}

// Blank returns an empty project state with the default maps.
func Blank(title string, ppq int) (State, error) {
	if ppq < 1 || ppq > MaxPPQ {
		return State{}, fmt.Errorf("PPQ 超出支持范围 1–%d", MaxPPQ)
	}
	return blankState(title, ppq), nil
}

func findTrack(st *State, id string) (int, bool) {
	for i := range st.Tracks {
		if st.Tracks[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

func findClip(t *Track, id string) (int, bool) {
	for i := range t.Clips {
		if t.Clips[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

func findNote(c *Clip, id string) (int, bool) {
	for i := range c.Notes {
		if c.Notes[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

func findControl(c *Clip, id string) (int, bool) {
	for i := range c.Controls {
		if c.Controls[i].ID == id {
			return i, true
		}
	}
	return 0, false
}
