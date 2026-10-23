package smf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"icetudio/internal/project"
)

// Export encodes the project as a deterministic SMF format 1 file: track 0
// carries the project name, tempo and meter maps; every project track
// becomes one MIDI track. At the same tick, note-offs sort before note-ons
// for the same channel+pitch so rearticulation round-trips (design §3.2).
func Export(st *project.State) ([]byte, error) {
	if st.PPQ < 1 || st.PPQ > project.MaxPPQ {
		return nil, fmt.Errorf("PPQ %d 超出支持范围", st.PPQ)
	}
	if len(st.Tracks) > project.MaxTracks {
		return nil, fmt.Errorf("轨数超过上限 %d", project.MaxTracks)
	}
	if len(st.Tracks)+1 > 0xFFFF {
		return nil, fmt.Errorf("轨数超过 SMF 上限")
	}

	out := new(bytes.Buffer)
	out.WriteString("MThd")
	binary.Write(out, binary.BigEndian, uint32(6))
	binary.Write(out, binary.BigEndian, uint16(1))
	binary.Write(out, binary.BigEndian, uint16(len(st.Tracks)+1))
	binary.Write(out, binary.BigEndian, uint16(st.PPQ))

	// conductor track
	events := []smfEvent{}
	if st.Title != "" {
		events = append(events, smfEvent{bytes: metaBytes(0x03, []byte(st.Title))})
	}
	for _, p := range st.Tempo {
		tick, err := checkAbs(p.Tick)
		if err != nil {
			return nil, err
		}
		events = append(events, smfEvent{tick: tick, bytes: metaBytes(0x51, []byte{
			byte(p.USPerQuarter >> 16 & 0xFF), byte(p.USPerQuarter >> 8 & 0xFF), byte(p.USPerQuarter & 0xFF),
		})})
	}
	for _, m := range st.Meter {
		tick, err := checkAbs(m.Tick)
		if err != nil {
			return nil, err
		}
		den := 0
		for v := m.Denominator; v > 1; v >>= 1 {
			den++
		}
		events = append(events, smfEvent{tick: tick, bytes: metaBytes(0x58, []byte{byte(m.Numerator), byte(den), 24, 8})})
	}
	sortEvents(events)
	events = append(events, smfEvent{tick: lastTick(events), bytes: metaBytes(0x2F, nil)})
	if err := writeTrack(out, events); err != nil {
		return nil, err
	}

	for ti, t := range st.Tracks {
		events := events[:0]
		if t.Name != "" {
			events = append(events, smfEvent{bytes: metaBytes(0x03, []byte(t.Name))})
		}
		for _, c := range t.Clips {
			base := c.Start
			for i := range c.Notes {
				n := &c.Notes[i]
				on, err := checkAbs(base + n.Start)
				if err != nil {
					return nil, err
				}
				off, err := checkAbs(base + n.Start + n.Duration)
				if err != nil {
					return nil, err
				}
				events = append(events,
					smfEvent{tick: off, bytes: []byte{0x80 | byte(n.Channel)&0x0F, byte(n.Pitch), 0}},
					smfEvent{tick: on, bytes: []byte{0x90 | byte(n.Channel)&0x0F, byte(n.Pitch), byte(n.Velocity)}},
				)
			}
			for _, e := range c.Controls {
				abs, err := checkAbs(base + e.Tick)
				if err != nil {
					return nil, err
				}
				ch := byte(e.Channel) & 0x0F
				var body []byte
				switch e.Kind {
				case project.KindCC:
					body = []byte{0xB0 | ch, byte(e.Controller), byte(e.Value)}
				case project.KindPitchBend:
					body = []byte{0xE0 | ch, byte(e.Value & 0x7F), byte(e.Value >> 7 & 0x7F)}
				case project.KindProgram:
					body = []byte{0xC0 | ch, byte(e.Program)}
				case project.KindChannelAftertouch:
					body = []byte{0xD0 | ch, byte(e.Value)}
				case project.KindPolyAftertouch:
					body = []byte{0xA0 | ch, byte(e.Note), byte(e.Value)}
				default:
					return nil, fmt.Errorf("轨 %d 存在未知事件类型 %q", ti+1, e.Kind)
				}
				events = append(events, smfEvent{tick: abs, bytes: body})
			}
			for _, r := range c.Raw {
				abs, err := checkAbs(base + r.Tick)
				if err != nil {
					return nil, err
				}
				body, err := encodeRaw(r.Data)
				if err != nil {
					return nil, fmt.Errorf("轨 %d：%w", ti+1, err)
				}
				events = append(events, smfEvent{tick: abs, bytes: body})
			}
		}
		// note-offs before note-ons at the same tick; everything else keeps
		// insertion order via the stable sort
		sort.SliceStable(events, func(i, j int) bool {
			if events[i].tick != events[j].tick {
				return events[i].tick < events[j].tick
			}
			offI := events[i].bytes[0]&0xF0 == 0x80
			offJ := events[j].bytes[0]&0xF0 == 0x80
			if offI != offJ {
				return offI
			}
			return false
		})
		events = append(events, smfEvent{tick: lastTick(events), bytes: metaBytes(0x2F, nil)})
		if err := writeTrack(out, events); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

type smfEvent struct {
	tick  int64
	bytes []byte
}

func sortEvents(events []smfEvent) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].tick < events[j].tick })
}

func lastTick(events []smfEvent) int64 {
	max := int64(0)
	for _, e := range events {
		if e.tick > max {
			max = e.tick
		}
	}
	return max
}

func checkAbs(t int64) (int64, error) {
	if t < 0 {
		return 0, fmt.Errorf("绝对 tick 为负：%d", t)
	}
	if err := absTickCheck(t); err != nil {
		return 0, err
	}
	return t, nil
}

func metaBytes(mtype byte, payload []byte) []byte {
	out := []byte{0xFF, mtype}
	out = append(out, varlen(uint32(len(payload)))...)
	out = append(out, payload...)
	return out
}

// encodeRaw re-encodes a preserved event into SMF wire format: channel
// messages and metas round-trip verbatim, SysEx needs its length prefix
// back (gomidi's decode strips it).
func encodeRaw(data []byte) ([]byte, error) {
	if len(data) == 0 || data[0] < 0x80 {
		return nil, fmt.Errorf("原始事件缺少状态字节")
	}
	switch data[0] {
	case 0xF0, 0xF7:
		if len(data) < 2 {
			return nil, fmt.Errorf("SysEx 原始事件过短")
		}
		out := []byte{data[0]}
		out = append(out, varlen(uint32(len(data)-1))...)
		return append(out, data[1:]...), nil
	default:
		return append([]byte{}, data...), nil
	}
}

func writeTrack(out *bytes.Buffer, events []smfEvent) error {
	body := new(bytes.Buffer)
	last := int64(0)
	for _, e := range events {
		delta := e.tick - last
		if delta < 0 {
			return fmt.Errorf("事件顺序出现回退（内部错误）")
		}
		body.Write(varlen(uint32(delta)))
		body.Write(e.bytes)
		last = e.tick
	}
	out.WriteString("MTrk")
	binary.Write(out, binary.BigEndian, uint32(body.Len()))
	out.Write(body.Bytes())
	return nil
}

func varlen(v uint32) []byte {
	buf := make([]byte, 0, 4)
	buf = append(buf, byte(v&0x7F))
	for v >>= 7; v > 0; v >>= 7 {
		buf = append(buf, byte(v&0x7F)|0x80)
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}
