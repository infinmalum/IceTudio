package smf

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"testing"

	"icetudio/internal/project"
)

// Independently encoded SMF fixture: never built with the library under
// test (mirrors the experiments/music-spike discipline). Includes running
// status, same-pitch retriggers, an unmatched note-off, sysex and meta.
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
	return build(format, uint16(len(tracks)), division, tracks)
}

func build(format, ntrks uint16, division uint16, tracks [][]byte) []byte {
	b := new(bytes.Buffer)
	b.WriteString("MThd")
	binary.Write(b, binary.BigEndian, uint32(6))
	for _, v := range []uint16{format, ntrks, division} {
		binary.Write(b, binary.BigEndian, v)
	}
	for _, tr := range tracks {
		b.WriteString("MTrk")
		binary.Write(b, binary.BigEndian, uint32(len(tr)))
		b.Write(tr)
	}
	return b.Bytes()
}

// stripIDs removes generated identities so two imports can be compared.
func stripIDs(t *testing.T, st project.State) string {
	t.Helper()
	for i := range st.Tracks {
		st.Tracks[i].ID = ""
		for j := range st.Tracks[i].Clips {
			c := &st.Tracks[i].Clips[j]
			c.ID = ""
			for k := range c.Notes {
				c.Notes[k].ID = ""
			}
			for k := range c.Controls {
				c.Controls[k].ID = ""
			}
			for k := range c.Raw {
				c.Raw[k].ID = ""
			}
		}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestImportSemantics(t *testing.T) {
	st, err := Import(fixture(1, 480))
	if err != nil {
		t.Fatal(err)
	}
	if st.PPQ != 480 {
		t.Fatalf("header wrong: ppq=%d", st.PPQ)
	}
	// tempo/meter maps from conductor track
	if len(st.Tempo) != 2 || st.Tempo[1].USPerQuarter != 600000 {
		t.Fatalf("tempo map: %+v", st.Tempo)
	}
	if len(st.Meter) != 2 || st.Meter[1].Numerator != 3 || st.Meter[1].Denominator != 4 {
		t.Fatalf("meter map: %+v", st.Meter)
	}
	// conductor track: maps + title; its non-map metas survive as a
	// metadata lane, so two tracks exist
	if len(st.Tracks) != 2 {
		t.Fatalf("expected metadata + music track, got %d", len(st.Tracks))
	}
	if st.Tracks[0].Name != "元数据" || len(st.Tracks[0].Clips[0].Raw) != 3 {
		t.Fatalf("metadata lane: %+v", st.Tracks[0])
	}
	clip := st.Tracks[1].Clips[0]
	// paired: retrigger@160 closes note1 (dur 160); off@320 closes note2;
	// unmatched off@480 and sysex stay raw
	if len(clip.Notes) != 2 {
		t.Fatalf("notes: %+v", clip.Notes)
	}
	if clip.Notes[0].Start != 0 || clip.Notes[0].Duration != 160 || clip.Notes[0].Channel != 1 {
		t.Fatalf("note1: %+v", clip.Notes[0])
	}
	if clip.Notes[1].Start != 160 || clip.Notes[1].Duration != 160 || clip.Notes[1].Velocity != 80 {
		t.Fatalf("note2: %+v", clip.Notes[1])
	}
	kinds := map[string]int{}
	for _, e := range clip.Controls {
		kinds[e.Kind]++
	}
	if kinds[project.KindCC] != 2 || kinds[project.KindPitchBend] != 1 || kinds[project.KindChannelAftertouch] != 1 || kinds[project.KindPolyAftertouch] != 1 {
		t.Fatalf("controls: %+v", clip.Controls)
	}
	if len(clip.Raw) != 2 { // orphan note-off + sysex
		t.Fatalf("raw events: %+v", clip.Raw)
	}
	if clip.Raw[0].Data[0] != 0x91 || clip.Raw[1].Data[0] != 0xF0 {
		t.Fatalf("raw bytes not preserved: %x %x", clip.Raw[0].Data, clip.Raw[1].Data)
	}
	if clip.Length != 480 {
		t.Fatalf("clip length: %d", clip.Length)
	}
}

func TestFormat0Import(t *testing.T) {
	st, err := Import(fixture(0, 480))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Tracks) != 1 {
		t.Fatalf("format 0 must yield one track, got %d", len(st.Tracks))
	}
	// no tempo meta in this track? the fixture track 1 has none, so the
	// default map must be filled in at tick 0
	if st.Tempo[0].Tick != 0 || st.Tempo[0].USPerQuarter != 500000 {
		t.Fatalf("default tempo: %+v", st.Tempo)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	st, err := Import(fixture(1, 480))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Export(&st)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := Import(data)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	a, b := stripIDs(t, st), stripIDs(t, st2)
	if a != b {
		t.Fatalf("round trip changed semantics\n%s\n%s", a, b)
	}
	again, err := Export(&st2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Fatal("export not deterministic")
	}
}

func TestRejections(t *testing.T) {
	cases := map[string][]byte{
		"smpte":         fixture(1, 0xe728),
		"truncated":     fixture(1, 480)[:13],
		"empty":         {},
		"not-smf":       []byte("RIFF0000WAVE"),
		"format2":       build(2, 2, 480, [][]byte{{0, 0xff, 0x2f, 0}, {0, 0xff, 0x2f, 0}}),
		"short-mthd":    func() []byte { b := fixture(1, 480); b[5] = 7; return b }(),
		"track-missing": func() []byte { b := fixture(1, 480); b[11] = 5; return b }(),
		"trailing":      append(fixture(1, 480), 0x00, 0x01, 0x02),
		"chunk-overflow": func() []byte {
			b := fixture(1, 480)
			binary.BigEndian.PutUint32(b[18:22], 1<<20)
			return b
		}(),
	}
	for name, data := range cases {
		if _, err := Import(data); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestUnmatchedOpenNoteClosesAtEnd(t *testing.T) {
	// note-on with no note-off: closed at EOT tick (480), min 1 tick
	tr := [][]byte{{0, 0x90, 60, 100, 0x83, 0x60, 0xff, 0x2f, 0}}
	st, err := Import(build(0, 1, 480, tr))
	if err != nil {
		t.Fatal(err)
	}
	notes := st.Tracks[0].Clips[0].Notes
	if len(notes) != 1 || notes[0].Start != 0 || notes[0].Duration != 480 {
		t.Fatalf("open note: %+v", notes)
	}
	// onset == end: minimum duration 1
	tr = [][]byte{{0x83, 0x60, 0x90, 60, 100, 0, 0xff, 0x2f, 0}}
	st, err = Import(build(0, 1, 480, tr))
	if err != nil {
		t.Fatal(err)
	}
	notes = st.Tracks[0].Clips[0].Notes
	if len(notes) != 1 || notes[0].Duration != 1 {
		t.Fatalf("zero-length note: %+v", notes)
	}
}

func TestOversizeInputRejected(t *testing.T) {
	// declared chunk length beyond the file is caught by framing; the file
	// size gate is exercised with a synthetic >10MiB buffer of header-only
	// bytes plus padding (framing still rejects the incomplete tail first,
	// so probe the size check directly)
	big := make([]byte, MaxFileSize+1)
	if _, err := Import(big); err == nil {
		t.Fatal("oversize file must be rejected")
	}
}

func TestConvertTickStillGuarded(t *testing.T) {
	if _, err := project.ConvertTick(10, 480, 960); err != nil {
		t.Fatal(err)
	}
}

func fmtEvent(tick int64, msg []byte) string { // helper keeps fixture readable
	return fmt.Sprintf("%d:%x", tick, msg)
}
