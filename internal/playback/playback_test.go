package playback

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"icetudio/internal/project"
)

func fixture(t *testing.T) *Score {
	t.Helper()
	state, _ := project.Blank("audio", 480)
	state.Tracks = []project.Track{{ID: "t_a", Clips: []project.Clip{{ID: "c_a", Length: 960, Notes: []project.Note{{ID: "n_a", Start: 0, Duration: 480, Pitch: 69, Velocity: 100}}}}}}
	score, err := Compile(Snapshot{ID: "p_a", Revision: 1, State: state})
	if err != nil {
		t.Fatal(err)
	}
	return score
}
func testNative(t *testing.T, live bool) *native {
	t.Helper()
	if !nativeAvailable() {
		t.Skip("Linux/cgo FluidSynth required")
	}
	font := filepath.Join(t.TempDir(), "basic.sf2")
	if err := os.WriteFile(font, BasicFont(), 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := openNative(font, "pulseaudio", !live)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.close)
	return engine
}
func energy(data []float32) float64 {
	v := 0.0
	for _, s := range data {
		v += float64(s * s)
	}
	return v
}
func TestNativeBasicFontProducesAudio(t *testing.T) {
	n := testNative(t, false)
	s := fixture(t)
	if err := n.install(s, 0, 0, 960, false, true); err != nil {
		t.Fatal(err)
	}
	data, _ := n.render(12000)
	if energy(data) < .01 {
		t.Fatalf("expected real rendered PCM, energy=%g", energy(data))
	}
	// Root sample is A4. Count crossings after the attack to check tuning.
	count := 0
	for i := 2001; i < 10000; i++ {
		if data[i-1] <= 0 && data[i] > 0 {
			count++
		}
	}
	frequency := float64(count) * SampleRate / 8000
	if math.Abs(frequency-440) > 12 {
		t.Fatalf("A4 tuning: %g Hz", frequency)
	}
	n.stop()
	silence, _ := n.render(2048)
	if energy(silence) != 0 || n.status().Playing {
		t.Fatal("stop left sound active")
	}
	for program := 0; program < 128; program += 32 {
		s.Events[0].A = int32(program)
		if err := n.install(s, 0, 0, 960, false, true); err != nil {
			t.Fatal(err)
		}
		data, _ = n.render(4000)
		if energy(data) == 0 {
			t.Fatalf("program %d silent", program)
		}
	}
}
func TestTimelineTempoAndBounds(t *testing.T) {
	state, _ := project.Blank("tempo", 480)
	state.Tempo = append(state.Tempo, project.TempoPoint{Tick: 480, USPerQuarter: 1000000})
	tm, err := NewTimeline(&state)
	if err != nil {
		t.Fatal(err)
	}
	if tm.Frame(960) != 72000 || tm.Tick(72000) != 960 {
		t.Fatalf("tempo integration: %d %g", tm.Frame(960), tm.Tick(72000))
	}
	for _, tick := range []float64{0, 120, 480, 601, 960} {
		if math.Abs(tm.Tick(tm.Frame(tick))-tick) > .02 {
			t.Fatal("inverse mismatch")
		}
	}
	state.Tempo[1].Tick = 0
	if _, err = NewTimeline(&state); err == nil {
		t.Fatal("duplicate tempo accepted")
	}
	s := fixture(t)
	if s.ValidateRange(-1, 0, 480, true) == nil || s.ValidateRange(0, 480, 480, true) == nil {
		t.Fatal("invalid ranges accepted")
	}
}
func TestChaseSustainAndIsolation(t *testing.T) {
	s := fixture(t)
	s.Events = append(s.Events, Event{Frame: 100, Kind: CC, Channel: 0, A: 64, B: 127}, Event{Frame: 30000, Kind: CC, Channel: 0, A: 64, B: 0})
	// Recompile from model to get the stable control ordering.
	st, _ := project.Blank("pedal", 480)
	st.Tracks = []project.Track{{ID: "a", Clips: []project.Clip{{ID: "ca", Length: 960, Notes: []project.Note{{ID: "na", Duration: 240, Pitch: 60, Velocity: 100}}, Controls: []project.ControlEvent{{Kind: project.KindCC, Tick: 0, Controller: 64, Value: 127}, {Kind: project.KindCC, Tick: 720, Controller: 64, Value: 0}}}}}, {ID: "b", Program: 80, Clips: []project.Clip{{ID: "cb", Length: 960, Notes: []project.Note{{ID: "nb", Duration: 480, Pitch: 64, Velocity: 100}}}}}}
	score, err := Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	if len(score.Channels) != 2 || score.Channels[1] != 16 {
		t.Fatalf("track channels: %v", score.Channels)
	}
	chased := score.Chase(score.Timeline.Frame(600))
	var on, off int
	for _, e := range chased {
		if e.Kind == NoteOn && e.Channel == 0 {
			on++
		}
		if e.Kind == NoteOff && e.Channel == 0 {
			off++
		}
	}
	if on != 1 || off != 1 {
		t.Fatalf("released but sustained note not reconstructed: %+v", chased)
	}
	for _, e := range score.Chase(score.Timeline.Frame(800)) {
		if e.Kind == NoteOn {
			t.Fatal("released pedal left a held note")
		}
	}
	st.Tracks[1].Solo = true
	score, err = Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	if len(score.Channels) != 1 || score.Channels[0] != 16 {
		t.Fatal("solo not respected")
	}
	st.Tracks[1].Muted = true
	score, err = Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	if len(score.Events) != 0 {
		t.Fatal("muted solo track audible")
	}
}
func TestNativeSeekLoopAndEnd(t *testing.T) {
	n := testNative(t, false)
	s := fixture(t)
	if err := n.install(s, 240, 240, 480, true, true); err != nil {
		t.Fatal(err)
	}
	data, _ := n.render(48000)
	if energy(data) == 0 || n.status().Loops < 3 {
		t.Fatalf("loop didn't repeat: %+v", n.status())
	}
	if err := n.install(s, 960, 0, 960, false, false); err != nil {
		t.Fatal(err)
	}
	data, _ = n.render(5000)
	if energy(data) != 0 {
		t.Fatal("stopped seek emitted audio")
	}
	if err := n.install(s, 0, 0, 960, false, true); err != nil {
		t.Fatal(err)
	}
	n.render(80000)
	if n.status().Playing {
		t.Fatal("end didn't stop")
	}
}
func TestLiveAudioDriver(t *testing.T) {
	if os.Getenv("ICETUDIO_LIVE_AUDIO") != "1" {
		t.Skip("set ICETUDIO_LIVE_AUDIO=1 to exercise the actual PulseAudio device")
	}
	n := testNative(t, true)
	s := fixture(t)
	if err := n.install(s, 0, 0, 480, true, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	status := n.status()
	if status.Callbacks < 2 || status.Frame == 0 {
		t.Fatalf("audio driver not advancing: %+v", status)
	}
	start := time.Now()
	n.stop()
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("stop command exceeded 50ms")
	}
	if n.status().Playing {
		t.Fatal("stop failed")
	}
	t.Logf("real driver callbacks=%d misses=%d position=%d frames; physical latency and listening not measured", status.Callbacks, status.Misses, status.Frame)
}

func TestNativeUpperTrackGroupAndDrumChannel(t *testing.T) {
	n := testNative(t, false)
	st, _ := project.Blank("groups", 480)
	for i := 0; i < 64; i++ {
		st.Tracks = append(st.Tracks, project.Track{ID: project.NewID("t")})
	}
	st.Tracks[63].Clips = []project.Clip{{ID: "c_last", Length: 960, Notes: []project.Note{{ID: "n_last", Duration: 480, Pitch: 69, Velocity: 100, Channel: 9}}}}
	s, err := Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	if err = n.install(s, 0, 0, 960, false, true); err != nil {
		t.Fatal(err)
	}
	l, _ := n.render(6000)
	if energy(l) == 0 {
		t.Fatal("track 64 percussion route is silent")
	}
}
func TestChaseControllerResetAndOverlappingPitch(t *testing.T) {
	s := &Score{Events: []Event{
		{Frame: 0, Kind: CC, Channel: 0, A: 7, B: 40}, {Frame: 0, Kind: CC, Channel: 0, A: 64, B: 127},
		{Frame: 0, Kind: NoteOn, Channel: 0, A: 60, B: 100, Token: 1},
		{Frame: 100, Kind: NoteOn, Channel: 0, A: 60, B: 90, Token: 2},
		{Frame: 200, Kind: NoteOff, Channel: 0, A: 60, Token: 1},
		{Frame: 300, Kind: CC, Channel: 0, A: 121, B: 0},
	}}
	chased := s.Chase(400)
	var volume, notes int
	for _, e := range chased {
		if e.Kind == CC && e.A == 7 && e.B == 40 {
			volume++
		}
		if e.Kind == NoteOn && e.Token == 2 {
			notes++
		}
		if e.Kind == CC && e.A == 64 && e.B >= 64 {
			t.Fatal("CC121 retained sustain")
		}
	}
	if volume != 1 || notes != 1 {
		t.Fatalf("reset/overlap chase: %+v", chased)
	}
}
