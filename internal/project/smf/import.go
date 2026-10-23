package smf

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gitlab.com/gomidi/midi/v2/smf"

	"icetudio/internal/project"
)

type pairKey struct {
	ch    int
	pitch int
}

type openNote struct {
	start int64
	vel   int
}

// importer carries per-file decode state.
type importer struct {
	st        project.State
	total     int
	noteTotal int
	rawLeft   int
	format    uint16
}

// Import parses a strict SMF 0/1 PPQ file into a fresh project state. The
// file must pass framing validation before the library decoder runs; any
// decoder panic is converted to an error, never a crash.
func Import(data []byte) (project.State, error) {
	f, err := validateFraming(data)
	if err != nil {
		return project.State{}, err
	}
	parsed, err := decodeSafely(data)
	if err != nil {
		return project.State{}, fmt.Errorf("MIDI 解码失败：%w", err)
	}
	if len(parsed.Tracks) != int(f.ntrks) {
		return project.State{}, fmt.Errorf("解码轨数 %d 与头声明 %d 不符", len(parsed.Tracks), f.ntrks)
	}
	imp := &importer{rawLeft: project.MaxRawBytes, format: f.format}
	imp.st, err = project.Blank("", int(f.division))
	if err != nil {
		return project.State{}, err
	}
	for i, track := range parsed.Tracks {
		if len(track) > MaxTrackEvents {
			return project.State{}, fmt.Errorf("轨 %d 事件数 %d 超过上限 %d", i+1, len(track), MaxTrackEvents)
		}
		imp.total += len(track)
		if imp.total > MaxTotalEvents {
			return project.State{}, fmt.Errorf("文件事件总数超过上限 %d", MaxTotalEvents)
		}
		if err := imp.importTrack(i, track); err != nil {
			return project.State{}, err
		}
	}
	if imp.st.Title == "" {
		imp.st.Title = "导入的工程"
	}
	if err := normalizeMaps(&imp.st); err != nil {
		return project.State{}, err
	}
	return imp.st, nil
}

// decodeSafely runs the library decoder behind a panic guard.
func decodeSafely(data []byte) (out *smf.SMF, err error) {
	defer func() {
		if p := recover(); p != nil {
			out, err = nil, fmt.Errorf("MIDI 解码失败（内容不受支持）：%v", p)
		}
	}()
	return smf.ReadFrom(bytes.NewReader(data))
}

// importTrack converts one SMF track into a project track with a single
// full-length clip (local tick = absolute tick, clip start 0).
func (imp *importer) importTrack(index int, track smf.Track) error {
	notes := []project.Note{}
	controls := []project.ControlEvent{}
	raws := []project.RawEvent{}
	var tempo, meter []pt
	open := map[pairKey][]openNote{}
	name := ""
	endTick := int64(0)
	hasChannel := false

	abs := int64(0)
	for _, ev := range track {
		abs += int64(ev.Delta)
		if err := absTickCheck(abs); err != nil {
			return fmt.Errorf("轨 %d：%w", index+1, err)
		}
		msg := []byte(ev.Message)
		if len(msg) == 0 {
			return fmt.Errorf("轨 %d 存在空事件", index+1)
		}
		endTick = abs
		status := msg[0]
		switch {
		case status < 0x80:
			return fmt.Errorf("轨 %d 存在无状态字节的事件", index+1)
		case status < 0xF0: // channel voice message
			hasChannel = true
			ch := int(status & 0x0F)
			kind := status & 0xF0
			switch kind {
			case 0x90:
				pitch, vel := int(msg[1]), int(msg[2])
				key := pairKey{ch, pitch}
				if vel == 0 {
					if !closeNote(&notes, open, key, abs) {
						raws = append(raws, rawEvent(abs, msg))
					}
					break
				}
				if len(open[key]) > 0 {
					// same pitch retriggered: close the old note at the new onset
					closeNote(&notes, open, key, abs)
				}
				open[key] = append(open[key], openNote{start: abs, vel: vel})
			case 0x80:
				if !closeNote(&notes, open, pairKey{ch, int(msg[1])}, abs) {
					raws = append(raws, rawEvent(abs, msg))
				}
			case 0xB0:
				controls = append(controls, project.ControlEvent{ID: project.NewID("e"), Tick: abs, Kind: project.KindCC, Channel: ch, Controller: int(msg[1]), Value: int(msg[2])})
			case 0xA0:
				controls = append(controls, project.ControlEvent{ID: project.NewID("e"), Tick: abs, Kind: project.KindPolyAftertouch, Channel: ch, Note: int(msg[1]), Value: int(msg[2])})
			case 0xE0:
				controls = append(controls, project.ControlEvent{ID: project.NewID("e"), Tick: abs, Kind: project.KindPitchBend, Channel: ch, Value: int(msg[2])<<7 | int(msg[1])})
			case 0xC0:
				controls = append(controls, project.ControlEvent{ID: project.NewID("e"), Tick: abs, Kind: project.KindProgram, Channel: ch, Program: int(msg[1])})
			case 0xD0:
				controls = append(controls, project.ControlEvent{ID: project.NewID("e"), Tick: abs, Kind: project.KindChannelAftertouch, Channel: ch, Value: int(msg[1])})
			}
		case status == 0xF0 || status == 0xF7: // sysex, kept verbatim
			if len(msg) > MaxRawEventSize {
				return fmt.Errorf("轨 %d 的 SysEx 超过 %d 字节", index+1, MaxRawEventSize)
			}
			raws = append(raws, project.RawEvent{ID: project.NewID("e"), Tick: abs, Data: append([]byte{}, msg...)})
		case status == 0xFF: // meta
			if len(msg) < 3 {
				return fmt.Errorf("轨 %d 的 meta 事件格式无效", index+1)
			}
			mtype, payload := msg[1], msg[3:]
			switch mtype {
			case 0x51:
				if len(payload) != 3 {
					return fmt.Errorf("轨 %d 的速度 meta 长度无效", index+1)
				}
				tempo = append(tempo, pt{tick: abs, a: int(payload[0])<<16 | int(payload[1])<<8 | int(payload[2])})
			case 0x58:
				if len(payload) != 4 {
					return fmt.Errorf("轨 %d 的拍号 meta 长度无效", index+1)
				}
				meter = append(meter, pt{tick: abs, a: int(payload[0]), b: int(payload[1])})
			case 0x2F: // end of track
				if abs > endTick {
					endTick = abs
				}
			case 0x03:
				if name == "" {
					name = strings.TrimSpace(string(payload))
				}
			default:
				if len(msg) > MaxRawEventSize {
					return fmt.Errorf("轨 %d 的 meta 事件超过 %d 字节", index+1, MaxRawEventSize)
				}
				raws = append(raws, project.RawEvent{ID: project.NewID("e"), Tick: abs, Data: append([]byte{}, msg...)})
			}
		default: // 0xF1..0xFE system real-time/escape leftovers
			if len(msg) > MaxRawEventSize {
				return fmt.Errorf("轨 %d 的系统事件超过 %d 字节", index+1, MaxRawEventSize)
			}
			raws = append(raws, project.RawEvent{ID: project.NewID("e"), Tick: abs, Data: append([]byte{}, msg...)})
		}
	}

	// close still-open notes at end of track (design §3.1.4)
	for key, stack := range open {
		for _, on := range stack {
			dur := endTick - on.start
			if dur < 1 {
				dur = 1
			}
			notes = append(notes, project.Note{ID: project.NewID("n"), Start: on.start, Duration: dur, Pitch: key.pitch, Velocity: on.vel, Channel: key.ch})
		}
		delete(open, key)
	}
	imp.noteTotal += len(notes)
	if imp.noteTotal > project.MaxNotes {
		return fmt.Errorf("音符总数超过上限 %d", project.MaxNotes)
	}

	for _, t := range tempo {
		imp.st.Tempo = append(imp.st.Tempo, project.TempoPoint{Tick: t.tick, USPerQuarter: t.a})
	}
	for _, m := range meter {
		den := m.b
		if den < 0 || den > 7 {
			return fmt.Errorf("轨 %d 的拍号分母指数无效：%d", index+1, den)
		}
		imp.st.Meter = append(imp.st.Meter, project.MeterPoint{Tick: m.tick, Numerator: m.a, Denominator: 1 << uint(den)})
	}

	// format 1 track 0 without channel messages is the tempo-map track: it
	// feeds the maps and the project title. Non-map metas it carries (key
	// signature, markers, sequencer data) are preserved in a metadata lane.
	if imp.format == 1 && index == 0 && !hasChannel {
		if name != "" {
			imp.st.Title = name
		}
		if len(raws) == 0 {
			return nil
		}
		for _, r := range raws {
			imp.rawLeft -= len(r.Data)
			if imp.rawLeft < 0 {
				return fmt.Errorf("原始事件字节总量超过上限 %d", project.MaxRawBytes)
			}
		}
		lane := name
		if lane == "" {
			lane = "元数据"
		}
		length := endTick
		if length < 1 {
			length = 1
		}
		imp.st.Tracks = append(imp.st.Tracks, project.Track{
			ID: project.NewID("t"), Name: lane,
			Clips: []project.Clip{{
				ID: project.NewID("c"), Name: lane, Start: 0, Length: length,
				Notes: []project.Note{}, Controls: []project.ControlEvent{}, Raw: raws,
			}},
		})
		return nil
	}

	length := endTick
	if length < 1 {
		length = 1
	}
	for _, r := range raws {
		imp.rawLeft -= len(r.Data)
		if imp.rawLeft < 0 {
			return fmt.Errorf("原始事件字节总量超过上限 %d", project.MaxRawBytes)
		}
	}
	ch := 0
	if len(notes) > 0 {
		ch = notes[0].Channel
	}
	imp.st.Tracks = append(imp.st.Tracks, project.Track{
		ID: project.NewID("t"), Name: name, Channel: ch,
		Clips: []project.Clip{{
			ID: project.NewID("c"), Name: name, Start: 0, Length: length,
			Notes: notes, Controls: controls, Raw: raws,
		}},
	})
	return nil
}

// closeNote pairs the newest open note for key; false means nothing was
// open (the caller preserves the note-off as a raw event).
func closeNote(notes *[]project.Note, open map[pairKey][]openNote, key pairKey, at int64) bool {
	stack := open[key]
	if len(stack) == 0 {
		return false
	}
	on := stack[len(stack)-1]
	open[key] = stack[:len(stack)-1]
	dur := at - on.start
	if dur < 1 {
		dur = 1
	}
	*notes = append(*notes, project.Note{ID: project.NewID("n"), Start: on.start, Duration: dur, Pitch: key.pitch, Velocity: on.vel, Channel: key.ch})
	return true
}

func rawEvent(tick int64, msg []byte) project.RawEvent {
	return project.RawEvent{ID: project.NewID("e"), Tick: tick, Data: append([]byte{}, msg...)}
}

type pt struct {
	tick int64
	a, b int
}

// normalizeMaps sorts the collected map points, dedupes same-tick entries
// and fills defaults when the file declared none.
func normalizeMaps(st *project.State) error {
	points := append([]project.TempoPoint{}, st.Tempo...)
	if len(points) == 0 || points[0].Tick != 0 {
		points = append([]project.TempoPoint{{Tick: 0, USPerQuarter: 500000}}, points...)
	}
	points, err := mergeTempo(points)
	if err != nil {
		return err
	}
	st.Tempo = points
	mp := append([]project.MeterPoint{}, st.Meter...)
	if len(mp) == 0 || mp[0].Tick != 0 {
		mp = append([]project.MeterPoint{{Tick: 0, Numerator: 4, Denominator: 4}}, mp...)
	}
	mp, err = mergeMeter(mp)
	if err != nil {
		return err
	}
	st.Meter = mp
	return nil
}

func mergeTempo(points []project.TempoPoint) ([]project.TempoPoint, error) {
	sort.SliceStable(points, func(i, j int) bool { return points[i].Tick < points[j].Tick })
	for _, p := range points {
		if p.USPerQuarter < 1 || p.USPerQuarter > 0xFFFFFF {
			return nil, fmt.Errorf("速度值超出支持范围：%d", p.USPerQuarter)
		}
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

func mergeMeter(points []project.MeterPoint) ([]project.MeterPoint, error) {
	sort.SliceStable(points, func(i, j int) bool { return points[i].Tick < points[j].Tick })
	for _, p := range points {
		d := p.Denominator
		if d < 1 || d > 128 || d&(d-1) != 0 {
			return nil, fmt.Errorf("拍号分母无效：%d", d)
		}
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
