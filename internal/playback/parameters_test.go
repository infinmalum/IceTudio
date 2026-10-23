package playback

import (
	"reflect"
	"testing"
)

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
