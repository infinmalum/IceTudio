package playback

import (
	"math"
	"reflect"
	"testing"
)

func windowFrequency(data []float32, lo, hi int) float64 {
	if lo < 1 {
		lo = 1
	}
	count := 0
	for i := lo; i < hi && i < len(data); i++ {
		if data[i-1] <= 0 && data[i] > 0 {
			count++
		}
	}
	return float64(count) * SampleRate / float64(hi-lo)
}

func TestResolvedParametersMatchNativeContinuousSeekAndFutureData(t *testing.T) {
	tests := []struct {
		name                 string
		events               []Event
		kind, register, want int
	}{
		{"RPN bend range survives null selection", []Event{cc(1, 0, 101, 0), cc(2, 0, 100, 0), cc(3, 0, 38, 0), cc(4, 0, 6, 12), cc(5, 0, 101, 127), cc(6, 0, 100, 127)}, 3, 0, 12},
		{"RPN fine and coarse tuning", []Event{cc(1, 0, 101, 0), cc(2, 0, 100, 2), cc(3, 0, 6, 69)}, 4, 51, 5000},
		{"SF2 NRPN generator selection", []Event{cc(1, 0, 99, 120), cc(2, 0, 98, 51), cc(3, 0, 38, 0), cc(4, 0, 6, 65)}, 4, 51, 128000},
		{"GS NRPN maps to ordinary controller", []Event{cc(1, 0, 99, 1), cc(2, 0, 98, 9), cc(3, 0, 6, 100)}, 0, 77, 100},
		{"RPN range survives CC121", []Event{cc(1, 0, 101, 0), cc(2, 0, 100, 0), cc(3, 0, 6, 7), cc(4, 0, 121, 0)}, 3, 0, 7},
		{"RPN generator cleared by CC121", []Event{cc(1, 0, 101, 0), cc(2, 0, 100, 2), cc(3, 0, 6, 70), cc(4, 0, 121, 0)}, 4, 51, 0},
		{"RPN overwrites same SF2 generator", []Event{cc(1, 0, 99, 120), cc(2, 0, 98, 51), cc(3, 0, 6, 65), cc(4, 0, 101, 0), cc(5, 0, 100, 2), cc(6, 0, 6, 67)}, 4, 51, 3000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fixture(t)
			raw.Events = append(tt.events, on(100, 0, 69, 1))
			raw.Events = append(raw.Events, off(40000, 0, 69, 1))
			reference := testNative(t, false)
			if err := reference.install(raw, 0, 0, 0, false, true); err != nil {
				t.Fatal(err)
			}
			reference.render(5000)
			want := reference.channelValue(0, tt.kind, tt.register)
			if want != tt.want {
				t.Fatalf("native reference %d want %d", want, tt.want)
			}
			resolved := *raw
			resolved.Events = resolveParameters(raw.Events)
			n := testNative(t, false)
			if err := n.install(&resolved, 100, 100, 200, true, true); err != nil {
				t.Fatal(err)
			}
			pcm, _ := n.render(6000)
			if got := n.channelValue(0, tt.kind, tt.register); got != want {
				t.Fatalf("seek/loop %d reference %d", got, want)
			}
			if energy(pcm) == 0 || n.status().Loops < 1 {
				t.Fatal("no PCM or loop")
			}
			n.stop()
			if n.channelValue(0, 3, 0) != 2 || n.channelValue(0, 4, 51) != 0 {
				t.Fatal("old parameters leaked after stop")
			}
		})
	}
	// The selector lives only in the compiler. A later partial data entry is
	// already resolved using the original stream, not the chase's last selector.
	raw := fixture(t)
	raw.Events = []Event{cc(1, 0, 101, 0), cc(2, 0, 100, 0), cc(3, 0, 6, 5), cc(4, 0, 100, 2), cc(5, 0, 6, 65), on(100, 0, 69, 1), cc(7000, 0, 6, 68), off(40000, 0, 69, 1)}
	raw.Events = resolveParameters(raw.Events)
	n := testNative(t, false)
	if err := n.install(raw, 100, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	n.render(4000)
	if n.channelValue(0, 3, 0) != 5 || n.channelValue(0, 4, 51) != 4000 {
		t.Fatal("future data entry used another parameter")
	}
}

func TestParameterChaseBoundedAndChannelIsolated(t *testing.T) {
	events := []Event{cc(0, 0, 101, 0), cc(0, 0, 100, 0), cc(0, 16, 101, 0), cc(0, 16, 100, 0), cc(0, 16, 6, 9)}
	for i := int64(1); i <= 100000; i++ {
		events = append(events, cc(i, 0, 6, int32(i%128)))
	}
	score := Score{Events: resolveParameters(events)}
	got := score.Chase(100001)
	if len(got) != 2 {
		t.Fatalf("parameter history leaked into audio callback: %d events", len(got))
	}
	if !reflect.DeepEqual(chasedPitches(got), []int32(nil)) {
		t.Fatal("unexpected notes")
	}
	for _, e := range got {
		if e.Channel == 16 && e.B != 9*128 {
			t.Fatal("channel parameter crossed tracks")
		}
	}
}

func TestNativeMonoChaseUsesNoteOrderAndDoesNotLeakAcrossTracks(t *testing.T) {
	n := testNative(t, false)
	s := fixture(t)
	s.Events = []Event{cc(0, 0, 126, 0), on(10, 0, 76, 1), on(20, 0, 60, 2), off(20000, 0, 60, 2), off(30000, 0, 76, 1)}
	s.Events = resolveParameters(s.Events)
	if err := n.install(s, 100, 100, 200, true, true); err != nil {
		t.Fatal(err)
	}
	data, _ := n.render(2500)
	freq := crossingFrequency(data[500:])
	if freq < 245 || freq > 280 {
		t.Fatalf("mono note order lost: %g Hz", freq)
	}
	n.stop()
	if n.channelValue(0, 0, 68) != 0 || n.channelValue(0, 0, 65) != 0 {
		t.Fatal("old legato/portamento")
	}
}
func crossingFrequency(data []float32) float64 {
	count := 0
	for i := 1; i < len(data); i++ {
		if data[i-1] <= 0 && data[i] > 0 {
			count++
		}
	}
	return float64(count) * SampleRate / float64(len(data))
}

// Portamento chase must restore CC5/65/84 before the glide target sounds, so
// seeking exactly onto the target onset reproduces the continuous glide.
// FluidSynth 2.6.1 portamento time scales with CC5 (≈ value/127 s, verified
// against the fixed local build); CC5=64 glides 60→67 over roughly 0.4 s.
func TestNativePortamentoChaseRestoresGlideFromSource(t *testing.T) {
	score := func() *Score {
		s := fixture(t)
		s.Events = resolveParameters([]Event{
			cc(0, 0, 65, 127), cc(0, 0, 5, 64),
			on(100, 0, 60, 1), cc(3000, 0, 84, 60), on(3200, 0, 67, 2),
			off(200000, 0, 60, 1), off(200000, 0, 67, 2),
		})
		s.EndFrame = 210000
		return s
	}
	ref := testNative(t, false)
	if err := ref.install(score(), 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	data, _ := ref.render(48000)
	refEarly, refLate := windowFrequency(data, 5200, 10200), windowFrequency(data, 40000, 45000)
	if refEarly > 345 || refLate < 378 || refLate > 405 {
		t.Fatalf("reference glide missing: early=%.1f late=%.1f", refEarly, refLate)
	}
	n := testNative(t, false)
	if err := n.install(score(), 64, 0, 0, false, true); err != nil { // tick 64 = frame 3200
		t.Fatal(err)
	}
	data, _ = n.render(48000)
	early, late := windowFrequency(data, 5200, 10200), windowFrequency(data, 40000, 45000)
	if math.Abs(early-refEarly) > 30 || math.Abs(late-refLate) > 12 {
		t.Fatalf("seek glide diverged from continuous play: early=%.1f (ref %.1f) late=%.1f (ref %.1f)", early, refEarly, late, refLate)
	}
	n.stop()
	if n.channelValue(0, 0, 65) != 0 || n.channelValue(0, 0, 5) != 0 {
		t.Fatal("portamento state survived stop")
	}
}

// Mono mode and legato footprint survive loop-boundary chases; a later install
// must restore polyphony (basic channel reset), and stop must clear CC68/65.
func TestNativeMonoLegatoLoopAndPolyRestore(t *testing.T) {
	n := testNative(t, false)
	s := fixture(t)
	s.Events = resolveParameters([]Event{
		cc(0, 0, 126, 0), cc(0, 0, 68, 1),
		on(1000, 0, 60, 1), on(2000, 0, 67, 2),
		off(150000, 0, 67, 2), off(150000, 0, 60, 1),
	})
	s.EndFrame = 160000
	if err := n.install(s, 60, 60, 120, true, true); err != nil { // loop frames 3000–6000, after both note-ons
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		data, _ := n.render(24000)
		if n.status().Loops < 1 {
			t.Fatal("loop not advancing")
		}
		if freq := crossingFrequency(data[1000:]); freq < 375 || freq > 410 {
			t.Fatalf("mono mode lost after loop chase: %.1f Hz", freq)
		}
	}
	if n.channelValue(0, 0, 68) != 1 {
		t.Fatal("legato footprint not active during playback")
	}
	n.stop()
	if n.channelValue(0, 0, 68) != 0 || n.channelValue(0, 0, 65) != 0 {
		t.Fatal("legato/portamento survived stop")
	}
	poly := fixture(t)
	poly.Events = []Event{on(0, 0, 60, 1), on(0, 0, 67, 2), off(60000, 0, 60, 1), off(60000, 0, 67, 2)}
	poly.EndFrame = 70000
	if err := n.install(poly, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	two, _ := n.render(24000)
	single := fixture(t)
	single.Events = []Event{on(0, 0, 60, 1), off(60000, 0, 60, 1)}
	single.EndFrame = 70000
	if err := n.install(single, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	one, _ := n.render(24000)
	if e1, e2 := energy(one[2000:]), energy(two[2000:]); e2 < 1.5*e1 {
		t.Fatalf("polyphony not restored after mono: one=%g two=%g", e1, e2)
	}
}
