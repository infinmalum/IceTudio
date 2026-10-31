package project

import (
	"reflect"
	"strings"
	"testing"
)

func TestObjectDiffDeletionReportsNestedMaterialWithoutRawPayload(t *testing.T) {
	before := State{Tracks: []Track{{ID: "t1", Name: "钢琴", Clips: []Clip{{ID: "c1", Name: "主题", Start: 480, Length: 960,
		Notes: []Note{{ID: "n1"}}, Controls: []ControlEvent{{ID: "e1", Kind: KindCC, Controller: 64, Value: 127}},
		Raw: []RawEvent{{ID: "r1", Data: []byte("private-raw-payload")}},
	}}}}}
	after := State{}
	diff := DiffObjects(&before, &after, 10)
	if diff.Total != 3 || len(diff.Changes) != 3 || !strings.Contains(diff.Changes[1].Before, "1 原始事件（19 字节）") || !strings.Contains(diff.Changes[1].Before, "1 音符 · 1 控制事件") {
		t.Fatalf("lost deletion consequences: %+v", diff)
	}
	for _, row := range diff.Changes {
		if row.TrackName != "钢琴" || strings.Contains(row.Before, "private-raw-payload") {
			t.Fatalf("bad target or leaked raw payload: %+v", row)
		}
	}
	if diff.Changes[2].ClipName != "主题" || !strings.Contains(diff.Changes[2].Before, "CC64 = 127") {
		t.Fatal("lost control detail")
	}
}

func TestObjectDiffGlobalAndMetadataCountsSurviveTruncation(t *testing.T) {
	before, _ := Blank("原名", 480)
	before.Tracks = []Track{{ID: "t1", Name: "钢琴", Clips: []Clip{{ID: "c1", Length: 960}}}}
	after := before
	_, _, _, err := ApplyNewTransaction(&after, []Op{
		{Kind: "project.rename", Title: "新名"},
		{Kind: "tempo.set", Tempo: []TempoPoint{{Tick: 0, USPerQuarter: 600000}, {Tick: 480, USPerQuarter: 750000}}},
		{Kind: "meter.set", Meter: []MeterPoint{{Tick: 0, Numerator: 3, Denominator: 4}}},
		{Kind: "track.update", TrackID: "t1", TrackPatch: &TrackPatch{Muted: boolPtr(true)}},
		{Kind: "clip.update", ClipID: "c1", ClipPatch: &ClipPatch{Start: tickPtr(480)}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	full, limited := DiffObjects(&before, &after, 100), DiffObjects(&before, &after, 2)
	if full.Total != 6 || limited.Total != 6 || len(limited.Changes) != 2 || !reflect.DeepEqual(limited.Changes, full.Changes[:2]) {
		t.Fatalf("incorrect total or unstable prefix: %+v %+v", full, limited)
	}
	if !strings.Contains(full.Changes[1].After, "100.000 BPM · 600000") {
		t.Fatal("tempo lost precision")
	}
	if DiffObjects(&before, &before, 100).Total != 0 {
		t.Fatal("unchanged state manufactured differences")
	}
}

func TestObjectDiffControlsKeepEffectiveValues(t *testing.T) {
	before := State{}
	after := State{Tracks: []Track{{ID: "t", Clips: []Clip{{ID: "c", Controls: []ControlEvent{
		{ID: "e1", Kind: KindCC, Tick: 10, Channel: 1, Controller: 0, Value: 0},
		{ID: "e2", Kind: KindProgram, Program: 40},
		{ID: "e3", Kind: KindPitchBend, Value: 8192},
		{ID: "e4", Kind: KindPolyAftertouch, Note: 60, Value: 70},
		{ID: "e5", Kind: KindChannelAftertouch, Value: 80},
	}}}}}}
	diff := DiffObjects(&before, &after, 100)
	for i, want := range []string{"CC0 = 0 · 片段内 tick 10 · 通道 2", "GM 音色 41", "弯音 8192", "音高 60 · 值 70", "通道压力 80"} {
		if !strings.Contains(diff.Changes[i+2].After, want) {
			t.Fatalf("missing effective value %q: %+v", want, diff)
		}
	}
}

func boolPtr(v bool) *bool   { return &v }
func tickPtr(v int64) *int64 { return &v }

func TestTrackRestoreIndexIsInternalAndAtomic(t *testing.T) {
	st, _ := Blank("restore", 480)
	st.Tracks = []Track{{ID: "t_first"}, {ID: "t_last"}}
	before := st
	zero, bad := 0, 3
	op := Op{Kind: "track.add", Track: &TrackSpec{ID: "t_fake"}, InvTrackIndex: &zero}
	if _, _, _, err := ApplyNewTransaction(&st, []Op{op}, nil); err == nil {
		t.Fatal("client injected internal restore index")
	}
	op.InvTrackIndex = &bad
	if _, _, err := ApplyTransaction(&st, []Op{op}); err == nil {
		t.Fatal("out-of-range trusted index accepted")
	}
	if !reflect.DeepEqual(before, st) {
		t.Fatal("rejected restoration changed state")
	}
}
