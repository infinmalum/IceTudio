//go:build linux && cgo

package playback

import (
	"testing"

	"icetudio/internal/project"
)

func TestMetronomeMeterChangesAndCountInPCM(t *testing.T) {
	st, _ := project.Blank("click", 480)
	st.Meter = append(st.Meter, project.MeterPoint{Tick: 480, Numerator: 3, Denominator: 8})
	score, err := compileWithMetronome(Snapshot{State: st}, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{0, 24000, 36000, 48000, 60000} {
		if score.Events[i].Frame != want || score.Events[i].Kind != Click {
			t.Fatalf("click %d: %+v", i, score.Events[i])
		}
	}
	for _, e := range score.Chase(37000) {
		if e.Kind == Click {
			t.Fatal("seek chased a click from the past")
		}
	}
	beats, interval, err := countInAt(&st, score.Timeline, 480)
	if err != nil || beats != 3 || interval != 12000 {
		t.Fatalf("count in: %d %d %v", beats, interval, err)
	}
	n := testNative(t, false)
	if err = n.installCount(score, 480, 0, 0, false, true, beats, interval); err != nil {
		t.Fatal(err)
	}
	l, _ := n.render(11999)
	if l[0] == 0 || l[len(l)-1] != 0 || !n.status().CountIn || n.status().Frame != 24000 {
		t.Fatal("count in advanced song time or omitted the pulse")
	}
	n.render(24001)
	if n.status().CountIn || n.status().Frame != 24000 {
		t.Fatal("count in did not end exactly at the start")
	}
	l, _ = n.render(120)
	if l[0] == 0 || n.status().Frame != 24120 {
		t.Fatal("project beat did not start after count in")
	}
	n.stop()
	l, _ = n.render(120)
	for _, sample := range l {
		if sample != 0 {
			t.Fatal("stop left a click sounding")
		}
	}
}
