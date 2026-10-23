package playback

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"icetudio/internal/project"
)

func cc(frame int64, ch, number, value int32) Event {
	return Event{Frame: frame, Kind: CC, Channel: ch, A: number, B: value}
}
func on(frame int64, ch, pitch int32, token uint32) Event {
	return Event{Frame: frame, Kind: NoteOn, Channel: ch, A: pitch, B: 100, Token: token}
}
func off(frame int64, ch, pitch int32, token uint32) Event {
	return Event{Frame: frame, Kind: NoteOff, Channel: ch, A: pitch, Token: token}
}
func chasedPitches(events []Event) []int32 {
	var pitches []int32
	for _, e := range events {
		if e.Kind == NoteOn {
			pitches = append(pitches, e.A)
		}
	}
	return pitches
}

func TestChaseSostenutoBoundariesAndPedalInteractions(t *testing.T) {
	tests := []struct {
		name   string
		events []Event
		want   []int32
	}{
		{"captures earlier notes only", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), on(2, 0, 64, 2), off(3, 0, 60, 1), off(4, 0, 64, 2)}, []int32{60}},
		{"uncaptured held key survives", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), on(2, 0, 64, 2), off(3, 0, 60, 1)}, []int32{60, 64}},
		{"release drops released captured note", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), off(2, 0, 60, 1), cc(3, 0, 66, 0)}, nil},
		{"sustain release respects sostenuto", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), cc(2, 0, 64, 127), off(3, 0, 60, 1), cc(4, 0, 64, 0)}, []int32{60}},
		{"sostenuto release respects sustain", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), cc(2, 0, 64, 127), off(3, 0, 60, 1), cc(4, 0, 66, 0)}, []int32{60}},
		{"second release drops note", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), cc(2, 0, 64, 127), off(3, 0, 60, 1), cc(4, 0, 66, 0), cc(5, 0, 64, 0)}, nil},
		{"captures sustain held note", []Event{cc(0, 0, 64, 127), on(1, 0, 60, 1), off(2, 0, 60, 1), cc(3, 0, 66, 127), cc(4, 0, 64, 0)}, []int32{60}},
		{"repeated down captures new note", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), on(2, 0, 64, 2), cc(3, 0, 66, 64), off(4, 0, 60, 1), off(5, 0, 64, 2)}, []int32{60, 64}},
		{"CC120 clears held sounds", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), cc(2, 0, 120, 0)}, nil},
		{"CC123 respects captured sound", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), on(2, 0, 64, 2), cc(3, 0, 123, 0)}, []int32{60}},
		{"CC121 drops released captured sound", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), off(2, 0, 60, 1), cc(3, 0, 121, 0)}, nil},
		{"CC121 retains held key", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), cc(2, 0, 121, 0)}, []int32{60}},
		{"released pedal does not capture later off", []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), cc(2, 0, 66, 0), off(3, 0, 60, 1)}, nil},
		{"other channel not captured", []Event{on(0, 16, 60, 1), cc(1, 0, 66, 127), off(2, 16, 60, 1)}, nil},
		{"target frame excluded", []Event{on(0, 0, 60, 1), off(10, 0, 60, 1)}, []int32{60}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chase := (&Score{Events: tt.events}).Chase(10)
			if got := chasedPitches(chase); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("notes %v, want %v; chase %+v", got, tt.want, chase)
			}
			for _, e := range chase {
				if e.Frame != 10 {
					t.Fatal("chased event retained old timestamp")
				}
			}
		})
	}
}

func TestChaseSostenutoOrderingAndBoundedHistory(t *testing.T) {
	s := &Score{Events: []Event{on(0, 0, 60, 1), cc(1, 0, 66, 127), on(2, 0, 64, 2), off(3, 0, 60, 1)}}
	want := []Event{on(10, 0, 60, 1), cc(10, 0, 66, 127), on(10, 0, 64, 2), off(10, 0, 60, 1)}
	if got := s.Chase(10); !reflect.DeepEqual(got, want) {
		t.Fatalf("capture order: %+v, want %+v", got, want)
	}
	for i := int64(4); i < 100004; i++ {
		s.Events = append(s.Events, cc(i, 0, 66, 127))
	}
	if got := s.Chase(100005); len(got) != len(want) {
		t.Fatalf("history leaked into callback chase: %d events", len(got))
	}
}

func TestChaseBankProgramAndReset(t *testing.T) {
	s := &Score{Events: []Event{
		cc(0, 0, 0, 1), cc(1, 0, 32, 2),
		{Frame: 2, Kind: Program, A: 32}, cc(3, 0, 0, 3), cc(4, 0, 32, 4),
		cc(5, 0, 39, 9), cc(6, 0, 74, 80), cc(7, 0, 94, 20), cc(8, 0, 11, 50), cc(9, 0, 121, 0),
	}}
	chase := s.Chase(10)
	var bank []Event
	for _, e := range chase {
		if e.Kind == Program || e.Kind == CC && (e.A == 0 || e.A == 32) {
			bank = append(bank, e)
		}
		if e.Kind == CC && e.A == 11 {
			t.Fatal("reset retained expression")
		}
	}
	want := []Event{cc(10, 0, 0, 1), cc(10, 0, 32, 2), {Frame: 10, Kind: Program, A: 32}, cc(10, 0, 0, 3), cc(10, 0, 32, 4)}
	if !reflect.DeepEqual(bank, want) || len(chase) != 8 {
		t.Fatalf("bank/reset state: %+v", chase)
	}
}

// The actual project compiler and native installer are used for seek/loop, not
// a simulated synthesizer. Duration/frame conversion remains the normal path.
func sostenutoScore(t *testing.T) *Score {
	t.Helper()
	st, _ := project.Blank("sostenuto", 480)
	st.Tracks = []project.Track{{Clips: []project.Clip{{Length: 960,
		Notes:    []project.Note{{Pitch: 69, Velocity: 100, Duration: 120}, {Pitch: 76, Velocity: 100, Start: 90, Duration: 60}},
		Controls: []project.ControlEvent{{Kind: project.KindCC, Tick: 60, Controller: 66, Value: 127}, {Kind: project.KindCC, Tick: 480, Controller: 66, Value: 0}},
	}}}}
	s, err := Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativeSostenutoSeekLoopReleaseAndStop(t *testing.T) {
	n := testNative(t, false)
	s := sostenutoScore(t)
	if err := n.install(s, 240, 240, 360, true, true); err != nil {
		t.Fatal(err)
	}
	data, _ := n.render(24000)
	if energy(data) < .01 || n.status().Loops < 3 {
		t.Fatal("CC66 held note missing after seek/loops")
	}
	// Only A4 was captured; the later E5 was released before the destination.
	count := 0
	for i := 2001; i < 5000; i++ {
		if data[i-1] <= 0 && data[i] > 0 {
			count++
		}
	}
	freq := float64(count) * SampleRate / 3000
	if freq < 425 || freq > 455 {
		t.Fatalf("uncaptured note leaked into chase: %g Hz", freq)
	}
	if err := n.install(s, 240, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	n.render(20000) // passes tick 480 and the basic font release envelope
	data, _ = n.render(2048)
	if energy(data) > 1e-8 || n.channelValue(0, 0, 66) != 0 {
		t.Fatalf("CC66 release left sound: %g", energy(data))
	}
	if err := n.install(s, 240, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	n.stop()
	data, _ = n.render(2048)
	if energy(data) != 0 || n.channelValue(0, 0, 66) != 0 {
		t.Fatal("stop retained sostenuto state")
	}
	if err := n.install(s, 600, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	data, _ = n.render(2048)
	if energy(data) > 1e-8 {
		t.Fatal("seek beyond released CC66 retriggered old note")
	}
}

func TestNativeChaseSelectsPresetBeforePendingBank(t *testing.T) {
	if !nativeAvailable() {
		t.Skip("Linux/cgo FluidSynth required")
	}
	// Make one additional legal test bank from our original generated data:
	// move program 32 to bank 1. No third-party assets are introduced.
	font := BasicFont()
	phdr := bytes.Index(font, []byte("phdr")) + 8
	if phdr < 8 || phdr+32*38+24 > len(font) {
		t.Fatal("missing generated preset table")
	}
	binary.LittleEndian.PutUint16(font[phdr+32*38+22:], 1)
	path := filepath.Join(t.TempDir(), "banks.sf2")
	if err := os.WriteFile(path, font, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := openNative(path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer n.close()
	st, _ := project.Blank("bank order", 480)
	st.Tracks = []project.Track{{Clips: []project.Clip{{Length: 960,
		Notes:    []project.Note{{Pitch: 69, Velocity: 100, Start: 4, Duration: 480}},
		Controls: []project.ControlEvent{{Kind: project.KindCC, Tick: 1, Controller: 0, Value: 1}, {Kind: project.KindProgram, Tick: 2, Program: 32}, {Kind: project.KindCC, Tick: 3, Controller: 0, Value: 0}},
	}}}}
	s, err := Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.install(s, 240, 240, 360, true, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		data, _ := n.render(6000)
		if energy(data) == 0 || n.channelValue(0, 1, 0) != 1 || n.channelValue(0, 2, 0) != 32 || n.channelValue(0, 0, 0) != 0 {
			t.Fatal("pending bank replaced the selected preset during seek/loop")
		}
	}
}

func TestNativeChaseDualPedalsAndChannelModeMessages(t *testing.T) {
	n := testNative(t, false)
	for _, tt := range []struct {
		name      string
		events    []Event
		wantSound bool
	}{
		{"capture sustain then release sustain", []Event{cc(0, 0, 64, 127), on(1, 0, 69, 1), off(2, 0, 69, 1), cc(3, 0, 66, 127), cc(4, 0, 64, 0), cc(16000, 0, 66, 0)}, true},
		{"release sostenuto while sustain remains", []Event{on(0, 0, 69, 1), cc(1, 0, 66, 127), cc(2, 0, 64, 127), off(3, 0, 69, 1), cc(4, 0, 66, 0), cc(16000, 0, 64, 0)}, true},
		{"CC123 releases keys into sostenuto", []Event{on(0, 0, 69, 1), cc(1, 0, 66, 127), cc(2, 0, 123, 0), cc(16000, 0, 66, 0)}, true},
		{"CC121 releases captured sound", []Event{on(0, 0, 69, 1), cc(1, 0, 66, 127), off(2, 0, 69, 1), cc(3, 0, 121, 0)}, false},
		{"CC120 silences captured sound", []Event{on(0, 0, 69, 1), cc(1, 0, 66, 127), cc(2, 0, 120, 0)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture(t)
			s.Events = tt.events
			if err := n.install(s, 240, 0, 0, false, true); err != nil {
				t.Fatal(err)
			}
			data, _ := n.render(3000)
			if (energy(data) > .01) != tt.wantSound {
				t.Fatalf("sound=%g, want audible=%v", energy(data), tt.wantSound)
			}
			n.render(6000)
			data, _ = n.render(2048)
			if energy(data) > 1e-8 {
				t.Fatal("final pedal release retained sound")
			}
		})
	}
}

func TestNativeSeekDoesNotCaptureLaterHeldKey(t *testing.T) {
	n := testNative(t, false)
	s := fixture(t)
	s.Events = []Event{on(0, 0, 69, 1), cc(10, 0, 66, 127), on(20, 0, 76, 2), off(30, 0, 69, 1), off(13000, 0, 76, 2), cc(30000, 0, 66, 0)}
	if err := n.install(s, 240, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	n.render(5000) // uncaptured key has been released; captured A4 remains
	data, _ := n.render(6000)
	count := 0
	for i := 1; i < len(data); i++ {
		if data[i-1] <= 0 && data[i] > 0 {
			count++
		}
	}
	freq := float64(count) * SampleRate / float64(len(data))
	if energy(data) < .01 || freq < 425 || freq > 455 {
		t.Fatalf("later key was captured during seek: energy=%g, frequency=%g", energy(data), freq)
	}
}

func TestNativeSostenutoAtSeekDestination(t *testing.T) {
	n := testNative(t, false)
	s := fixture(t)
	s.Events = []Event{on(0, 0, 69, 1), cc(12000, 0, 66, 127), on(12000, 0, 76, 2), off(13000, 0, 69, 1), off(13000, 0, 76, 2)}
	if err := n.install(s, 240, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	n.render(5000)
	data, _ := n.render(6000)
	count := 0
	for i := 1; i < len(data); i++ {
		if data[i-1] <= 0 && data[i] > 0 {
			count++
		}
	}
	freq := float64(count) * SampleRate / float64(len(data))
	if energy(data) < .01 || freq < 425 || freq > 455 {
		t.Fatalf("destination CC66/notes reordered: energy=%g frequency=%g", energy(data), freq)
	}
}

func TestManagerRefreshDropsRemovedSostenuto(t *testing.T) {
	makeSnapshot := func(revision int64) Snapshot {
		s := managerSnapshot()
		s.Revision = revision
		s.State.Tracks[0].Clips[0].Notes[0].Duration = 120
		if revision == 1 {
			s.State.Tracks[0].Clips[0].Controls = []project.ControlEvent{{Kind: project.KindCC, Tick: 60, Controller: 66, Value: 127}}
		}
		return s
	}
	var mu sync.Mutex
	revision := int64(1)
	m := offlineManager(t, func(string) (Snapshot, error) {
		mu.Lock()
		defer mu.Unlock()
		return makeSnapshot(revision), nil
	})
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1, Tick: 240}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	data, _ := m.engine.render(2000)
	m.mu.Unlock()
	if energy(data) < .01 {
		t.Fatal("initial CC66-held note missing")
	}
	tick := m.Status().Tick
	mu.Lock()
	revision = 2
	mu.Unlock()
	m.Changed("p_a")
	status := waitStatus(t, m, func(s Status) bool { return s.Revision == 2 })
	if status.Tick != tick || !status.Playing {
		t.Fatalf("refresh moved or stopped transport: %+v", status)
	}
	m.mu.Lock()
	data, _ = m.engine.render(2048)
	pedal := m.engine.channelValue(0, 0, 66)
	m.mu.Unlock()
	if energy(data[64:]) > 1e-8 || pedal != 0 {
		t.Fatalf("removed CC66 survived revision replacement: first64=%g 64-256=%g 256-1024=%g last1024=%g CC66=%d", energy(data[:64]), energy(data[64:256]), energy(data[256:1024]), energy(data[1024:]), pedal)
	}
	t.Logf("revision swap PCM energy: first64=%g, remaining1984=%g, CC66=%d", energy(data[:64]), energy(data[64:]), pedal)
}
