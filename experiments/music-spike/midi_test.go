package spike

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"gitlab.com/gomidi/midi/v2/smf"
)

// Independently encoded SMF fixture: do not use the library under test to
// construct the input. Includes running status and overlapping same-pitch notes.
func fixture(format uint16, division uint16) []byte {
	tracks := [][]byte{
		{0, 0xff, 0x51, 3, 7, 0xa1, 0x20, 0, 0xff, 0x58, 4, 4, 2, 24, 8,
			0, 0xff, 0x59, 2, 0, 0, 0, 0xff, 6, 3, 'A', 'B', 'C',
			0x83, 0x60, 0xff, 0x51, 3, 9, 0x27, 0xc0,
			0, 0xff, 0x58, 4, 3, 2, 24, 8, 0, 0xff, 0x7f, 3, 1, 2, 3, 0, 0xff, 0x2f, 0},
		{0, 0xc1, 10, 0, 0xb1, 64, 127, 0, 0xe1, 0, 80,
			0, 0xd1, 60, 0, 0xa1, 60, 50,
			0, 0x91, 60, 100, 0x81, 0x20, 60, 80,
			0x81, 0x20, 0x81, 60, 45, 0x81, 0x20, 0x91, 60, 0,
			0, 0xb1, 64, 0, 0, 0xf0, 4, 0x7e, 0x7f, 9, 0xf7,
			0, 0xff, 0x2f, 0},
	}
	if format == 0 {
		tracks = tracks[1:]
	}
	b := new(bytes.Buffer)
	b.WriteString("MThd")
	binary.Write(b, binary.BigEndian, uint32(6))
	for _, v := range []uint16{format, uint16(len(tracks)), division} {
		binary.Write(b, binary.BigEndian, v)
	}
	for _, tr := range tracks {
		b.WriteString("MTrk")
		binary.Write(b, binary.BigEndian, uint32(len(tr)))
		b.Write(tr)
	}
	return b.Bytes()
}

func signature(s *smf.SMF) string {
	out := fmt.Sprintf("%d/%v", s.Format(), s.TimeFormat)
	for i, tr := range s.Tracks {
		var tick uint32
		for _, e := range tr {
			tick += e.Delta
			out += fmt.Sprintf("|%d:%d:%x", i, tick, []byte(e.Message))
		}
	}
	return out
}

func TestEventRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name             string
		format, division uint16
	}{
		{"format1_ppq", 1, 480}, {"format0_ppq", 0, 480}, {"format1_smpte25", 1, 0xe728},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("upstream panic: %v", p)
				}
			}()
			s, err := smf.ReadFrom(bytes.NewReader(fixture(tc.format, tc.division)))
			if err != nil {
				t.Fatal(err)
			}
			if s.Format() != tc.format {
				t.Fatal("format lost")
			}
			notes := s.Tracks[len(s.Tracks)-1]
			if len(notes) != 12 {
				t.Fatalf("expected 12 events, got %d", len(notes))
			}
			for _, v := range []struct {
				i     int
				delta uint32
				raw   []byte
			}{
				{5, 0, []byte{0x91, 60, 100}}, {6, 160, []byte{0x91, 60, 80}},
				{7, 160, []byte{0x81, 60, 45}}, {8, 160, []byte{0x91, 60, 0}},
				{10, 0, []byte{0xf0, 0x7e, 0x7f, 9, 0xf7}},
			} {
				if notes[v.i].Delta != v.delta || !bytes.Equal(notes[v.i].Message, v.raw) {
					t.Fatalf("input event %d decoded incorrectly", v.i)
				}
			}
			if tc.format == 1 && tc.division == 480 && s.TimeAt(960) != 1100000 {
				t.Fatalf("tempo conversion: %d", s.TimeAt(960))
			}
			for _, noRunning := range []bool{false, true} {
				s.NoRunningStatus = noRunning
				data, err := s.Bytes()
				if err != nil {
					t.Fatal(err)
				}
				r, err := smf.ReadFrom(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				if signature(s) != signature(r) {
					t.Fatalf("events changed\n%s\n%s", signature(s), signature(r))
				}
			}
			t.Logf("preserved %d tracks; events=%s", len(s.Tracks), signature(s))
		})
	}
}

func TestTruncation(t *testing.T) {
	b := fixture(1, 480)
	for _, n := range []int{0, 4, 13, len(b) - 2} {
		if _, err := smf.ReadFrom(bytes.NewReader(b[:n])); err == nil {
			t.Errorf("accepted truncated input length %d", n)
		}
	}
}
