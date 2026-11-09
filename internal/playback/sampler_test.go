//go:build linux && cgo

package playback

import (
	"math"
	"testing"

	"icetudio/internal/project"
)

func TestSampleVoiceRoutesKeyAndVelocity(t *testing.T) {
	n := mixerTestNative(t)
	a := project.AudioAsset{ID: "sample", Frames: 24000, Digest: "abcdef"}
	z := project.SampleZone{AssetID: a.ID, Frames: a.Frames, Root: 60, Low: 60, High: 72, MinVelocity: 64, MaxVelocity: 127, Attack: 1, Sustain: 100, Release: 1, Gain: 100}
	st := project.State{AudioAssets: []project.AudioAsset{a}, Tracks: []project.Track{{ID: "track", Sampler: &project.Sampler{Zones: []project.SampleZone{z}}, Clips: []project.Clip{
		{Length: 200, Notes: []project.Note{{Pitch: 60, Velocity: 50, Start: 0, Duration: 20}, {Pitch: 72, Velocity: 127, Start: 40, Duration: 40}}},
	}}}, Mixer: &project.Mixer{Channels: []project.MixChannel{{ID: "track", Gain: .5, Pan: -1}}}}
	s := mixerTestScore(t, st)
	if len(s.Events) != 0 || len(s.Audio) != 1 || s.Audio[0].Voice.rate != 2 {
		t.Fatalf("voice projection %+v", s.Audio)
	}
	pcm := make([]int16, a.Frames*2)
	for i := int64(0); i < a.Frames; i++ {
		pcm[i*2] = int16(i % 5000)
		pcm[i*2+1] = pcm[i*2]
	}
	s.Audio[0].Samples = pcm
	if err := n.install(s, 0, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	l, r := n.render(4500)
	start := int(s.Audio[0].Frame)
	for i, v := range r {
		if v != 0 {
			t.Fatalf("panned voice leaked at %d", i)
		}
	}
	want := float64(pcm[100*2*2]) * .5 / 32768
	if l[0] != 0 || l[start] != 0 || math.Abs(float64(l[start+100])-want) > 1e-6 || l[start+2100] != 0 {
		t.Fatalf("bad attack, rate or gate: %v %v %v", l[start], l[start+100], l[start+2100])
	}
}
