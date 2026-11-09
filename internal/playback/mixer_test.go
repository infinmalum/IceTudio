//go:build linux && cgo

package playback

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"icetudio/internal/project"
)

func mixerTestNative(t testing.TB) *native {
	t.Helper()
	path := filepath.Join(t.TempDir(), "basic.sf2")
	if err := os.WriteFile(path, BasicFont(), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := openNative(path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.close)
	return n
}

func mixerTestScore(t testing.TB, st project.State) *Score {
	t.Helper()
	st.PPQ = 480
	st.Tempo = []project.TempoPoint{{Tick: 0, USPerQuarter: 500000}}
	score, err := Compile(Snapshot{State: st})
	if err != nil {
		t.Fatal(err)
	}
	score.EndFrame = max(score.EndFrame, 48000)
	score.EndTick = max(score.EndTick, 960)
	return score
}

func testPCM(track int, id string, samples []int16) audioRegion {
	frames := int64(len(samples) / 2)
	return audioRegion{Track: track, Asset: project.AudioAsset{Digest: id, Frames: frames},
		Clip: project.AudioClip{Frames: frames, Gain: 100}, Samples: samples}
}

func TestMixerExtendedEffects(t *testing.T) {
	n := mixerTestNative(t)
	samples := make([]int16, 2*4800)
	samples[0], samples[1] = 16384, 16384
	for i := 1000; i < 2000; i++ {
		samples[2*i] = 8192
		if i%2 != 0 {
			samples[2*i] = -8192
		}
		samples[2*i+1] = samples[2*i]
	}
	for i := 2000; i < 4800; i++ {
		samples[2*i], samples[2*i+1] = 256, 256
	}
	render := func(t *testing.T, fx project.MixEffect, lanes []project.Automation) ([]float32, []float32) {
		t.Helper()
		st := project.State{Tracks: []project.Track{{ID: "audio"}}, Mixer: &project.Mixer{
			Channels: []project.MixChannel{{ID: "audio", Gain: 1, Effects: []project.MixEffect{fx}}},
			Presets:  []project.MixPreset{{Name: "Test", Effect: project.MixEffect{Kind: fx.Kind, Amount: fx.Amount, Wet: fx.Wet}}},
			Lanes:    lanes,
		}}
		s := mixerTestScore(t, st)
		if s.mix.latency != 0 {
			t.Fatalf("effect reported unintended compensation latency %d", s.mix.latency)
		}
		s.Audio = []audioRegion{testPCM(0, "test", samples)}
		if err := n.install(s, 0, 0, 0, false, true); err != nil {
			t.Fatal(err)
		}
		return n.render(4800)
	}
	for _, kind := range []string{"chorus", "lowpass", "drive", "gate"} {
		t.Run(kind, func(t *testing.T) {
			fx := project.MixEffect{ID: "fx", Kind: kind, Amount: 1, Wet: 1}
			l, r := render(t, fx, nil)
			switch kind {
			case "chorus":
				// The moving fractional tap spreads the impulse across adjacent
				// samples and shifts it from the nominal LFO endpoint.
				left, right := l[48]+l[49], float32(0)
				for _, sample := range r[710:721] {
					right += sample
				}
				if l[0] != 0 || left < .48 || right < .48 || l[713] != 0 || r[48] != 0 {
					t.Fatalf("chorus stereo taps: %g %g %g %g %g", l[0], left, right, l[713], r[48])
				}
			case "lowpass":
				fx.Amount = 0
				closed, _ := render(t, fx, nil)
				if l[1400] < .12 || math.Abs(float64(closed[1400])) > .003 || closed[0] < .005 {
					t.Fatalf("lowpass cutoff: %g %g %g", l[1400], closed[1400], closed[0])
				}
			case "drive":
				lane := project.Automation{ID: "curve", Target: fx.ID, Parameter: "amount",
					Points: []project.AutomationPoint{{Tick: 0, Value: 0, Step: true}, {Tick: 30, Value: 1}}}
				automated, _ := render(t, fx, []project.Automation{lane})
				if automated[1499] != -.25 || automated[1500] != .75 || l[1200] != .75 {
					t.Fatalf("drive automation: %g %g %g", automated[1499], automated[1500], l[1200])
				}
			case "gate":
				if l[1900] < .24 || l[4700] > .001 || l[4700] != r[4700] {
					t.Fatalf("gate did not close on the quiet section: %g %g %g", l[1900], l[4700], r[4700])
				}
			}
			fx.Bypass = true
			dryL, dryR := render(t, fx, nil)
			fx.Bypass, fx.Wet = false, 0
			zeroWetL, zeroWetR := render(t, fx, nil)
			if dryL[0] != .5 || dryL[1200] != .25 || !slices.Equal(zeroWetL, dryL) || !slices.Equal(zeroWetR, dryR) {
				t.Fatal("bypass or zero wet altered the dry signal")
			}
		})
	}
}

func TestMixerCompensatesParallelImpulseAndAudibleLoopBoundary(t *testing.T) {
	n := mixerTestNative(t)
	state := project.State{Tracks: []project.Track{{ID: "direct"}, {ID: "delayed"}},
		Mixer: &project.Mixer{Channels: []project.MixChannel{{ID: "delayed", Gain: 1, Effects: []project.MixEffect{{ID: "f", Kind: "limiter", Amount: 0, Wet: 1}}}}}}
	s := mixerTestScore(t, state)
	samples := make([]int16, 200)
	samples[0], samples[1] = 8192, 8192
	s.Audio = []audioRegion{testPCM(0, "one", samples), testPCM(1, "two", samples)}
	if err := n.install(s, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	l, r := n.render(200)
	if s.mix.latency != 64 || l[64] != .5 || r[64] != .5 {
		t.Fatalf("parallel pulse split %d %v %v", s.mix.latency, l[64], r[64])
	}
	for i, v := range l {
		if i != 64 && v != 0 {
			t.Fatalf("extra signal frame %d: %v", i, v)
		}
	}
	// The second pulse is exactly 100 frames after the first, even though its
	// delayed output crosses the first scheduler wrap.
	if err := n.install(s, 0, 0, 2, true, true); err != nil {
		t.Fatal(err)
	}
	l, _ = n.render(180)
	if l[64] != .5 || l[164] != .5 {
		t.Fatalf("loop compensation dropped pulse %v %v", l[64], l[164])
	}
	meter := n.meters(s.mix)[project.MasterChannel]
	if meter.Hold != .5 || meter.Clips != 0 {
		t.Fatalf("unexpected meter %+v", meter)
	}
	n.resetMeters()
	if n.meters(s.mix)[project.MasterChannel] != (Meter{}) {
		t.Fatal("meter did not reset")
	}
}

func TestMixerSidechainAndAutomationSeek(t *testing.T) {
	n := mixerTestNative(t)
	state := project.State{Tracks: []project.Track{{ID: "key"}, {ID: "music"}}, Mixer: &project.Mixer{
		Channels: []project.MixChannel{{ID: "quiet_bus", BusName: "Muted key", Gain: 0}, {ID: "music", Gain: 1, Effects: []project.MixEffect{{ID: "comp", Kind: "compressor", Amount: 1, Wet: 1}}}},
		Routes:   []project.MixRoute{{From: "key", To: "quiet_bus", Kind: "output", Gain: 1}, {From: "key", To: "music", Kind: "sidechain", Gain: 1}},
		Lanes:    []project.Automation{{ID: "pan_curve", Target: "music", Parameter: "pan", Points: []project.AutomationPoint{{Tick: 0, Value: -1}, {Tick: 30, Value: 1, Step: true}}}},
	}}
	key, music := make([]int16, 4000), make([]int16, 4000)
	for i := range key {
		key[i], music[i] = 28000, 2000
	}
	s := mixerTestScore(t, state)
	s.Audio = []audioRegion{testPCM(0, "key", key), testPCM(1, "music", music)}
	if err := n.install(s, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	l, r := n.render(1700)
	if l[800] <= 0 || l[800] >= .04 || math.Abs(float64(l[800]-r[700])) > 1e-6 || l[1600] != 0 || r[1600] <= 0 {
		t.Fatalf("sidechain / pan: %v %v %v %v", l[800], r[700], l[1600], r[1600])
	}
	state.Mixer.Routes = state.Mixer.Routes[:1]
	s = mixerTestScore(t, state)
	s.Audio = []audioRegion{testPCM(0, "key", key), testPCM(1, "music", music)}
	if err := n.install(s, 30, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	l, r = n.render(64)
	if l[0] != 0 || r[0] != float32(2000.0/32768) {
		t.Fatalf("seek did not chase pan or key leaked: %v %v", l[0], r[0])
	}
	if err := n.install(s, 0, 0, 30, true, true); err != nil {
		t.Fatal(err)
	}
	l, r = n.render(1600)
	if r[1500] != 0 || l[1500] != float32(2000.0/32768) {
		t.Fatalf("loop did not chase pan: %v %v", l[1500], r[1500])
	}
}

func TestMixerSynthTracksRemainIndependent(t *testing.T) {
	n := mixerTestNative(t)
	state := project.State{Mixer: &project.Mixer{}}
	for i, id := range []string{"piano", "bass"} {
		state.Tracks = append(state.Tracks, project.Track{ID: id, Program: i * 32, Clips: []project.Clip{{Length: 480, Notes: []project.Note{{Pitch: 60 - i*12, Duration: 480, Velocity: 96}}}}})
		state.Mixer.Channels = append(state.Mixer.Channels, project.MixChannel{ID: id, Gain: 1, Pan: float64(i*2 - 1)})
	}
	s := mixerTestScore(t, state)
	if err := n.install(s, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	l, r := n.render(2048)
	left, right := float32(0), float32(0)
	for i := range l {
		left += l[i] * l[i]
		right += r[i] * r[i]
	}
	if left < .001 || right < .001 {
		t.Fatalf("missing independent MIDI stems %v %v", left, right)
	}
	state.Mixer.Channels[1].Gain = 0
	s = mixerTestScore(t, state)
	if err := n.install(s, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	_, r = n.render(2048)
	for i, v := range r {
		if v != 0 {
			t.Fatalf("isolated track leaked at %d: %v", i, v)
		}
	}
}

func BenchmarkMixer64MIDI20PCM(b *testing.B) {
	for _, effects := range []bool{false, true} {
		name := "routes"
		if effects {
			name = "effects"
		}
		b.Run(name, func(b *testing.B) {
			s := mixerStressScore(b, effects)
			n := mixerTestNative(b)
			if err := n.install(s, 0, 0, 1920, true, true); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n.render(256)
			}
		})
	}
}

func mixerStressScore(t testing.TB, effects bool) *Score {
	st := project.State{Mixer: &project.Mixer{}}
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("track_%d", i)
		st.Tracks = append(st.Tracks, project.Track{ID: id, Clips: []project.Clip{{Length: 1920, Notes: []project.Note{
			{Pitch: 48 + i%24, Duration: 1920, Velocity: 85},
			{Pitch: 55 + i%24, Duration: 1920, Velocity: 85},
		}}}})
		if effects {
			fx := project.MixEffect{ID: "fx_" + id, Kind: []string{"eq", "compressor", "limiter", "delay", "reverb"}[i%5], Amount: .5, Wet: .5}
			st.Mixer.Channels = append(st.Mixer.Channels, project.MixChannel{ID: id, Gain: .25, Effects: []project.MixEffect{fx}})
		}
	}
	s := mixerTestScore(t, st)
	pcm := make([]int16, 192000)
	for i := range pcm {
		pcm[i] = int16(i%1024 - 512)
	}
	for i := 0; i < 20; i++ {
		s.Audio = append(s.Audio, testPCM(i, "shared", pcm))
	}
	return s
}
