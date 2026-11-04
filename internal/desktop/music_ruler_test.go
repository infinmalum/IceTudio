package desktop

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"icetudio/internal/project"
)

func TestNativeMusicRulerSharesWebViewFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../frontend/tests/fixtures/native-time-map.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string
		PPQ        int
		Meter      []project.MeterPoint
		Tempo      []project.TempoPoint
		Start, End int64
		Zoom       float64
		Thinned    bool
		Positions  []struct {
			Tick                   int64
			Bar, Beat              int64
			Remainder              float64
			Numerator, Denominator int
		}
		Marks []rulerMark
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, f := range cases {
		t.Run(f.Name, func(t *testing.T) {
			m, err := newMeterProjection(f.PPQ, f.Meter)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range f.Positions {
				want := musicPosition{p.Bar, p.Beat, p.Remainder, p.Numerator, p.Denominator}
				if got := m.position(p.Tick); got != want {
					t.Fatalf("position %d: %+v want %+v", p.Tick, got, want)
				}
			}
			got, thinned := m.ruler(f.PPQ, f.Start, f.End, f.Zoom, f.Tempo)
			if !reflect.DeepEqual(got, f.Marks) || thinned != f.Thinned {
				t.Fatalf("Native ruler differs from retained TS rules: got %+v; want %+v", got, f.Marks)
			}
		})
	}
}
func TestNativeRulerBoundsDenseChanges(t *testing.T) {
	meter := make([]project.MeterPoint, 7000)
	tempo := make([]project.TempoPoint, 7000)
	for i := range meter {
		meter[i] = project.MeterPoint{Tick: int64(i), Numerator: 1 + i%7, Denominator: 128}
		tempo[i] = project.TempoPoint{Tick: int64(i), USPerQuarter: 400000 + i}
	}
	m, err := newMeterProjection(480, meter)
	if err != nil {
		t.Fatal(err)
	}
	marks, thinned := m.ruler(480, 0, 7680, 200, tempo)
	counts := map[string]int{}
	for _, p := range marks {
		counts[p.Kind] += p.Count
	}
	if !thinned || len(marks) >= 1024 || counts["meter"] != 7000 || counts["tempo"] != 7000 {
		t.Fatalf("dropped dense changes: %d %+v", len(marks), counts)
	}
	for _, invalid := range [][]project.MeterPoint{nil, {{Tick: 0, Numerator: 4, Denominator: 3}}, {{Tick: 1, Numerator: 4, Denominator: 4}}, {{Tick: 0, Numerator: 4, Denominator: 4}, {Tick: 0, Numerator: 3, Denominator: 4}}} {
		if _, err := newMeterProjection(480, invalid); err == nil {
			t.Fatal("invalid map accepted")
		}
	}
}
