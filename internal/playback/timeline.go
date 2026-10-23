// Package playback compiles immutable music snapshots off the audio thread.
// Native rendering consumes sample frames independently of Go/UI/model clocks.
package playback

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"icetudio/internal/project"
)

const SampleRate = 48000
const MaxFrames = int64(SampleRate) * 60 * 60 * 24
const MaxEvents = 1000000

type Event struct {
	Frame               int64
	Kind, Channel, A, B int32
	Token               uint32
}

const (
	NoteOff = iota + 1
	NoteOn
	CC
	Program
	Bend
	Pressure
	KeyPressure
	Parameter
)

type segment struct {
	tick, frame float64
	us          int
}
type Timeline struct {
	PPQ      int
	segments []segment
}

func NewTimeline(state *project.State) (Timeline, error) {
	t := Timeline{PPQ: state.PPQ}
	if t.PPQ < 1 || t.PPQ > project.MaxPPQ || len(state.Tempo) == 0 || state.Tempo[0].Tick != 0 {
		return t, errors.New("播放需要有效 PPQ 和 tick 0 速度")
	}
	var prev int64 = -1
	for _, p := range state.Tempo {
		if p.Tick <= prev || p.USPerQuarter < 1 || p.USPerQuarter > 0xffffff {
			return t, errors.New("速度图无效")
		}
		frame := float64(0)
		if len(t.segments) > 0 {
			s := t.segments[len(t.segments)-1]
			frame = s.frame + (float64(p.Tick)-s.tick)*float64(s.us)*SampleRate/(1e6*float64(t.PPQ))
		}
		if frame > float64(MaxFrames) {
			return t, errors.New("播放时间超过 24 小时上限")
		}
		t.segments = append(t.segments, segment{float64(p.Tick), frame, p.USPerQuarter})
		prev = p.Tick
	}
	return t, nil
}
func (t Timeline) Frame(tick float64) int64 {
	i := sort.Search(len(t.segments), func(i int) bool { return t.segments[i].tick > tick }) - 1
	if i < 0 {
		i = 0
	}
	s := t.segments[i]
	return int64(math.Round(s.frame + (tick-s.tick)*float64(s.us)*SampleRate/(1e6*float64(t.PPQ))))
}
func (t Timeline) Tick(frame int64) float64 {
	i := sort.Search(len(t.segments), func(i int) bool { return t.segments[i].frame > float64(frame) }) - 1
	if i < 0 {
		i = 0
	}
	s := t.segments[i]
	return s.tick + (float64(frame)-s.frame)*1e6*float64(t.PPQ)/(float64(s.us)*SampleRate)
}

type Snapshot struct {
	ID       string
	Revision int64
	State    project.State
}
type Score struct {
	ID                string
	Revision          int64
	Timeline          Timeline
	Events            []Event
	EndTick, EndFrame int64
	Channels          []int32
	Skipped           int
}

func Compile(snapshot Snapshot) (*Score, error) {
	st := &snapshot.State
	t, err := NewTimeline(st)
	if err != nil {
		return nil, err
	}
	score := &Score{ID: snapshot.ID, Revision: snapshot.Revision, Timeline: t}
	if len(st.Tracks) > 64 {
		return nil, errors.New("最多播放 64 轨")
	}
	solo := false
	for _, tr := range st.Tracks {
		solo = solo || tr.Solo
	}
	channels := map[int32]bool{}
	var token uint32
	add := func(tick int64, kind int, ch, a, b int, tok uint32) error {
		if tick < 0 || ch < 0 || ch >= 1024 {
			return errors.New("播放事件位置或通道无效")
		}
		frame := t.Frame(float64(tick))
		if frame < 0 || frame > MaxFrames {
			return errors.New("播放时间超过 24 小时上限")
		}
		score.Events = append(score.Events, Event{frame, int32(kind), int32(ch), int32(a), int32(b), tok})
		channels[int32(ch)] = true
		if len(score.Events) > MaxEvents {
			return errors.New("播放事件数超过上限")
		}
		return nil
	}
	for ti, tr := range st.Tracks {
		if tr.Channel < 0 || tr.Channel > 15 {
			return nil, errors.New("轨道通道无效")
		}
		// Muted tracks still contribute length, so mute does not move the loop end.
		audible := !tr.Muted && (!solo || tr.Solo)
		for _, cl := range tr.Clips {
			if cl.Start < 0 || cl.Length < 1 || cl.Start > math.MaxInt64-cl.Length {
				return nil, errors.New("片段时间无效")
			}
			score.EndTick = max(score.EndTick, cl.Start+cl.Length)
			if !audible {
				continue
			}
			used := map[int]bool{tr.Channel: true}
			for _, n := range cl.Notes {
				if n.Channel < 0 || n.Channel > 15 || n.Pitch < 0 || n.Pitch > 127 || n.Velocity < 1 || n.Velocity > 127 || n.Start < 0 || n.Duration < 1 || n.Start > cl.Length || n.Duration > cl.Length-n.Start {
					return nil, errors.New("播放音符无效")
				}
				token++
				ch := ti*16 + n.Channel
				used[n.Channel] = true
				if err = add(cl.Start+n.Start, NoteOn, ch, n.Pitch, n.Velocity, token); err != nil {
					return nil, err
				}
				if err = add(cl.Start+n.Start+n.Duration, NoteOff, ch, n.Pitch, 0, token); err != nil {
					return nil, err
				}
			}
			for _, c := range cl.Controls {
				if c.Channel < 0 || c.Channel > 15 || c.Tick < 0 || c.Tick >= cl.Length {
					return nil, errors.New("播放控制事件无效")
				}
				used[c.Channel] = true
				kind, a, b := 0, 0, 0
				switch c.Kind {
				case project.KindCC:
					kind, a, b = CC, c.Controller, c.Value
				case project.KindProgram:
					kind, a = Program, c.Program
				case project.KindPitchBend:
					kind, a = Bend, c.Value
				case project.KindChannelAftertouch:
					kind, a = Pressure, c.Value
				case project.KindPolyAftertouch:
					kind, a, b = KeyPressure, c.Note, c.Value
				default:
					score.Skipped++
					continue
				}
				if (kind == Bend && (a < 0 || a > 16383)) || (kind != Bend && (a < 0 || a > 127)) || b < 0 || b > 127 {
					return nil, errors.New("播放控制值无效")
				}
				if err = add(cl.Start+c.Tick, kind, ti*16+c.Channel, a, b, 0); err != nil {
					return nil, err
				}
			}
			score.Skipped += len(cl.Raw)
			for ch := range used {
				if tr.Program < 0 || tr.Program > 127 {
					return nil, errors.New("轨道程序号无效")
				}
				// Only one track default per channel, ordered before clip controls.
				channel := int32(ti*16 + ch)
				if !channels[-channel-1] {
					channels[-channel-1] = true
					score.Events = append(score.Events, Event{Kind: Program, Channel: channel, A: int32(tr.Program), B: -1})
					channels[channel] = true
				}
			}
		}
	}
	if score.EndTick == 0 {
		score.EndTick = int64(st.PPQ) * 4
	}
	if len(score.Events) > MaxEvents {
		return nil, errors.New("播放事件数超过上限")
	}
	score.EndFrame = t.Frame(float64(score.EndTick))
	if score.EndFrame <= 0 || score.EndFrame > MaxFrames {
		return nil, errors.New("工程播放时长无效或超过 24 小时")
	}
	// Default programs at tick zero precede controls/notes regardless of clip order.
	sort.SliceStable(score.Events, func(i, j int) bool {
		a, b := score.Events[i], score.Events[j]
		if a.Frame != b.Frame {
			return a.Frame < b.Frame
		}
		rank := func(e Event) int {
			if e.Kind == NoteOff {
				return 2
			}
			if e.Kind == NoteOn {
				return 3
			}
			if e.Kind == Program && e.B == -1 {
				return 0
			}
			return 1
		}
		return rank(a) < rank(b)
	})
	score.Events = resolveParameters(score.Events)
	for ch := range channels {
		if ch >= 0 {
			score.Channels = append(score.Channels, ch)
		}
	}
	sort.Slice(score.Channels, func(i, j int) bool { return score.Channels[i] < score.Channels[j] })
	return score, nil
}

// Chase reconstructs the state strictly before a destination. Notes released
// while CC64 or captured by CC66 remain audible until both release. No SysEx
// is replayed. Analysis is off the audio thread; output is bounded by channel
// state and surviving notes, not the length of the preceding controller history.
func (s *Score) Chase(frame int64) []Event {
	controls := map[[3]int32]Event{}
	// Bank selection changes the pending bank; only Program chooses a preset.
	// Remember the bank at that Program separately from the latest bank values.
	programBanks := map[int32][]Event{}
	notes := map[[2]int32]Event{}
	released := map[[2]int32]bool{}
	pedal := map[int32]bool{}
	sostenuto := map[[2]int32]bool{}
	dropReleased := func(ch int32) {
		for k := range released {
			if k[0] == ch && !pedal[ch] && !sostenuto[k] {
				delete(notes, k)
				delete(released, k)
			}
		}
	}
	for _, e := range s.Events {
		if e.Frame >= frame {
			break
		}
		key := [2]int32{e.Channel, e.A}
		switch e.Kind {
		case NoteOn:
			// Portamento Control (84) is consumed by a note-on, not a
			// persistent source for every later note after a seek.
			delete(controls, [3]int32{e.Channel, CC, 84})
			notes[key] = e
			delete(released, key)
			delete(sostenuto, key)
		case NoteOff:
			if n, ok := notes[key]; ok && n.Token == e.Token {
				if pedal[e.Channel] || sostenuto[key] {
					released[key] = true
				} else {
					delete(notes, key)
				}
			}
		case CC:
			if e.A == 120 || e.A == 123 || e.A >= 124 {
				for k := range notes {
					if k[0] == e.Channel {
						if e.A != 120 && (pedal[e.Channel] || sostenuto[k]) {
							released[k] = true
						} else {
							delete(notes, k)
							delete(released, k)
							delete(sostenuto, k)
						}
					}
				}
				if e.A >= 124 {
					controls[[3]int32{e.Channel, CC, 124 + (e.A-124)/2*2}] = e
				}
				continue
			}
			if e.A == 121 {
				for k := range controls {
					if k[0] == e.Channel && k[1] != Program {
						// FluidSynth 2.6.1 retains bank, volume, pan, balance,
						// sound controllers and effects depth on CC121.
						cc := k[2]
						if k[1] == CC && cc == 37 { v := controls[k]; v.B = 0; controls[k] = v; continue }
						keep := k[1] == CC && (cc == 0 || cc == 32 || cc == 7 || cc == 39 || cc == 10 || cc == 42 || cc == 8 || cc == 40 || cc >= 70 && cc <= 79 || cc >= 91 && cc <= 95 || cc >= 124) || k[1] == Parameter && (cc == 0 || cc == 3 || cc == 4 || cc == 5)
						if !keep {
							delete(controls, k)
						}
					}
				}
				pedal[e.Channel] = false
				for k := range sostenuto {
					if k[0] == e.Channel {
						delete(sostenuto, k)
					}
				}
				for k := range released {
					if k[0] == e.Channel {
						delete(notes, k)
						delete(released, k)
					}
				}
				continue
			}
			if e.A == 64 {
				pedal[e.Channel] = e.B >= 64
				dropReleased(e.Channel)
			}
			if e.A == 66 {
				for k := range notes {
					if k[0] == e.Channel {
						if e.B >= 64 {
							sostenuto[k] = true
						} else {
							delete(sostenuto, k)
						}
					}
				}
				dropReleased(e.Channel)
			}
			controls[[3]int32{e.Channel, CC, e.A}] = e
		default:
			if e.Kind == Parameter {
				controls[[3]int32{e.Channel, Parameter, parameterKey(e)}] = e
				continue
			}
			if e.Kind == Program {
				var bank []Event
				for _, cc := range []int32{0, 32} {
					if b, ok := controls[[3]int32{e.Channel, CC, cc}]; ok {
						bank = append(bank, b)
					}
				}
				programBanks[e.Channel] = bank
			}
			key := int32(0)
			if e.Kind == KeyPressure {
				key = e.A
			}
			controls[[3]int32{e.Channel, e.Kind, key}] = e
		}
	}
	out := make([]Event, 0, len(controls)+len(notes)*2)
	for _, e := range controls {
		if e.Kind == Program || e.Kind == CC && (e.A == 0 || e.A == 32 || e.A == 66) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		if a.Frame != b.Frame {
			return a.Frame < b.Frame
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.A < b.A
	})
	// Program needs its historical bank; a later bank event is pending until
	// the next Program. Do not reorder those three stages by controller number.
	channels := make([]int32, 0, len(programBanks))
	for ch := range programBanks {
		channels = append(channels, ch)
	}
	// Also restore bank-only channels when a Score is constructed without defaults.
	for k := range controls {
		if k[1] == CC && (k[2] == 0 || k[2] == 32) {
			if _, ok := programBanks[k[0]]; !ok {
				programBanks[k[0]] = nil
				channels = append(channels, k[0])
			}
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	appendAt := func(e Event) { e.Frame = frame; out = append(out, e) }
	for _, ch := range channels {
		for _, e := range programBanks[ch] {
			appendAt(e)
		}
		if e, ok := controls[[3]int32{ch, Program, 0}]; ok {
			appendAt(e)
		}
		for _, cc := range []int32{0, 32} {
			if e, ok := controls[[3]int32{ch, CC, cc}]; ok {
				appendAt(e)
			}
		}
	}
	// RPN tuning-program selection uses its historical tuning bank. Restore
	// a later pending tuning-bank value after choosing the active program.
	for _, e := range controls {
		if e.Kind == Parameter && e.A == 4 {
			appendAt(e)
		}
	}
	keys := make([][2]int32, 0, len(notes))
	for k := range notes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		a, b := notes[keys[i]], notes[keys[j]]
		if a.Frame != b.Frame {
			return a.Frame < b.Frame
		}
		if a.Token != b.Token {
			return a.Token < b.Token
		}
		return keys[i][1] < keys[j][1]
	})
	// CC66 captures voices already sounding. Sound captured notes first, then
	// engage CC66, then sound uncaptured notes. CC64 is already restored above.
	for _, k := range keys {
		if sostenuto[k] {
			appendAt(notes[k])
		}
	}
	var pedals []Event
	for k, e := range controls {
		if k[1] == CC && k[2] == 66 {
			pedals = append(pedals, e)
		}
	}
	sort.Slice(pedals, func(i, j int) bool { return pedals[i].Channel < pedals[j].Channel })
	for _, e := range pedals {
		appendAt(e)
	}
	for _, k := range keys {
		if !sostenuto[k] {
			appendAt(notes[k])
		}
	}
	for _, k := range keys {
		if released[k] {
			n := notes[k]
			n.Kind = NoteOff
			n.B = 0
			appendAt(n)
		}
	}
	for i := range out {
		out[i].Frame = frame
	}
	return out
}
func (s *Score) ValidateRange(start, loopA, loopB int64, loop bool) error {
	if start < 0 || start > s.EndTick {
		return fmt.Errorf("播放位置须在 0–%d tick 内", s.EndTick)
	}
	if loop && (loopA < 0 || loopB <= loopA || loopB > s.EndTick) {
		return fmt.Errorf("循环须满足 0 ≤ 起点 < 终点 ≤ %d", s.EndTick)
	}
	return nil
}
