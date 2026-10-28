package project

import (
	"encoding/json"
	"strings"
	"testing"
)

func transformFixture() *State {
	st := &State{Title: "测试", PPQ: 480}
	c1 := Clip{ID: "clip_a", Start: 0, Length: 2880}
	c1.Notes = []Note{
		{ID: "n1", Start: 10, Duration: 120, Pitch: 60, Velocity: 40, Channel: 0},
		{ID: "n2", Start: 250, Duration: 100, Pitch: 64, Velocity: 100, Channel: 0},
		{ID: "n3", Start: 500, Duration: 240, Pitch: 67, Velocity: 64, Channel: 1},
		{ID: "n4", Start: 980, Duration: 480, Pitch: 72, Velocity: 20, Channel: 0},
		{ID: "n5", Start: 1920, Duration: 480, Pitch: 0, Velocity: 90, Channel: 0},
		{ID: "n6", Start: 1930, Duration: 480, Pitch: 127, Velocity: 90, Channel: 0},
	}
	t1 := Track{ID: "track_x", Channel: 0, Program: 0, Clips: []Clip{c1}}
	c2 := Clip{ID: "clip_b", Start: 1920, Length: 960}
	c2.Notes = []Note{
		{ID: "n7", Start: 30, Duration: 200, Pitch: 55, Velocity: 70, Channel: 0},
	}
	t2 := Track{ID: "track_y", Channel: 1, Program: 40, Clips: []Clip{c2}}
	st.Tracks = []Track{t1, t2}
	return st
}

func transformOpsAccept(t *testing.T, st *State, ops []Op) {
	t.Helper()
	_, _, err := ApplyTransaction(st, ops)
	if err != nil {
		t.Fatalf("编译产物未被事务层接受：%v", err)
	}
}

func TestSelectNotesFiltersAndOnsetWindow(t *testing.T) {
	st := transformFixture()
	// Onset window [0, 200) keeps n1 (start 10) only — n2 starts at 250.
	groups, stats, err := SelectNotes(st, Selection{ClipIDs: []string{"clip_a"}, StartTick: ptrInt64(0), EndTick: ptrInt64(200)})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Matched != 1 || len(groups) != 1 || groups[0].Notes[0].ID != "n1" {
		t.Fatalf("起音窗口选择错误：%+v", stats)
	}
	// AND across criteria: clip_a + velocity 60–127 → n2, n3, n5, n6.
	_, stats, err = SelectNotes(st, Selection{ClipIDs: []string{"clip_a"}, VelocityMin: ptrInt(60), VelocityMax: ptrInt(127)})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Matched != 4 {
		t.Fatalf("velocity AND 过滤错误：%+v", stats)
	}
	// Track filter reaches notes in its clips.
	_, stats, err = SelectNotes(st, Selection{TrackIDs: []string{"track_y"}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Matched != 1 {
		t.Fatalf("轨选择错误：%+v", stats)
	}
	// Unknown IDs match nothing instead of erroring.
	_, stats, err = SelectNotes(st, Selection{ClipIDs: []string{"clip_zzz"}})
	if err != nil || stats.Matched != 0 {
		t.Fatalf("未知 ID 应零匹配：%v %+v", err, stats)
	}
}

func TestSelectNotesValidation(t *testing.T) {
	st := transformFixture()
	if _, _, err := SelectNotes(st, Selection{PitchMin: ptrInt(-1)}); err == nil {
		t.Fatal("pitchMin 负值应拒绝")
	}
	if _, _, err := SelectNotes(st, Selection{Channel: ptrInt(16)}); err == nil {
		t.Fatal("channel 16 应拒绝")
	}
	if _, _, err := SelectNotes(st, Selection{StartTick: ptrInt64(300), EndTick: ptrInt64(300)}); err == nil {
		t.Fatal("空窗口应拒绝")
	}
	if _, _, err := SelectNotes(st, Selection{ClipIDs: []string{"BAD ID"}}); err == nil {
		t.Fatal("非法 ID 应拒绝")
	}
}

func TestCompileTransposeSkipsOutOfRange(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	tr := TransformSpec{}
	tr.Transpose = &struct {
		Semitones int `json:"semitones"`
	}{Semitones: -12}
	ops, stats, err := CompileTransform(st, sel, tr)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Matched != 6 || stats.Changed != 5 || stats.SkippedPitch != 1 {
		t.Fatalf("移调统计错误：%+v", stats)
	}
	if len(ops) != 1 || ops[0].Kind != "note.update" || ops[0].ClipID != "clip_a" || len(ops[0].NoteDeltas) != 5 {
		t.Fatalf("移调编译产物错误：%+v", ops)
	}
	// n5 pitch 0 skipped; n1 60→48.
	found := false
	for _, d := range ops[0].NoteDeltas {
		if d.ID == "n5" {
			t.Fatal("越界音符不应产出 delta")
		}
		if d.ID == "n1" && d.Pitch != nil && *d.Pitch == 48 {
			found = true
		}
	}
	if !found {
		t.Fatal("n1 移调结果缺失")
	}
	transformOpsAccept(t, st, ops)
}

func TestCompileTransposeAllOutOfRangeRejected(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	tr := TransformSpec{}
	tr.Transpose = &struct {
		Semitones int `json:"semitones"`
	}{Semitones: -200}
	if _, _, err := CompileTransform(st, sel, tr); err == nil {
		t.Fatal("全部越界（-200 半音）应拒绝")
	}
}

func TestCompileQuantizeGridStrengthTriplet(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	tr := TransformSpec{}
	tr.Quantize = &struct {
		Unit     string `json:"unit"`
		Strength *int   `json:"strength,omitempty"`
	}{Unit: "1/16"} // grid = 480*4/16 = 120
	ops, stats, err := CompileTransform(st, sel, tr)
	if err != nil {
		t.Fatal(err)
	}
	check := map[string]int64{"n1": 0, "n2": 240, "n4": 960, "n5": 1920, "n6": 1920}
	_ = check
	// n1 10→0, n2 250→240 (strength 100), n3 500→480, n4 980→960,
	// n5 1920→1920 unchanged, n6 1930→1920.
	if stats.Changed != 5 {
		t.Fatalf("量化变更数错误：%+v", stats)
	}
	got := map[string]int64{}
	for _, d := range ops[0].NoteDeltas {
		got[d.ID] = *d.Start
	}
	want := map[string]int64{"n1": 0, "n2": 240, "n3": 480, "n4": 960, "n6": 1920}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("量化 %s 期望 %d 实得 %d", id, w, got[id])
		}
	}
	if _, ok := got["n5"]; ok {
		t.Fatal("已对齐的 n5 不应产出 delta")
	}
	transformOpsAccept(t, st, ops)

	// Strength 50: n2 250 → snapped 240, delta -10 → round(-10*0.5) = -5 → 245.
	st2 := transformFixture()
	tr2 := TransformSpec{}
	tr2.Quantize = &struct {
		Unit     string `json:"unit"`
		Strength *int   `json:"strength,omitempty"`
	}{Unit: "1/16", Strength: ptrInt(50)}
	ops2, _, err := CompileTransform(st2, sel, tr2)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ops2[0].NoteDeltas {
		if d.ID == "n2" && *d.Start != 245 {
			t.Fatalf("强度 50 量化 n2 期望 245 实得 %d", *d.Start)
		}
	}

	// Triplet 1/8T: grid = 480*4/24 = 80.
	st3 := transformFixture()
	tr3 := TransformSpec{}
	tr3.Quantize = &struct {
		Unit     string `json:"unit"`
		Strength *int   `json:"strength,omitempty"`
	}{Unit: "1/8T"}
	ops3, _, err := CompileTransform(st3, sel, tr3)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ops3[0].NoteDeltas {
		if d.ID == "n1" && *d.Start != 0 {
			t.Fatalf("三连音量化 n1 期望 0 实得 %d", *d.Start)
		}
		if d.ID == "n2" && *d.Start != 240 {
			t.Fatalf("三连音量化 n2 期望 240 实得 %d", *d.Start)
		}
	}
}

func TestGridTicksFloorsAndRejectsUnknown(t *testing.T) {
	if g, _ := gridTicks(480, "1/16"); g != 120 {
		t.Fatalf("480 PPQ 1/16 应为 120，实得 %d", g)
	}
	if g, _ := gridTicks(96, "1/16"); g != 24 {
		t.Fatalf("96 PPQ 1/16 应为 24，实得 %d", g)
	}
	if g, _ := gridTicks(100, "1/32"); g != 12 { // 100*4/32 = 12.5 → floor 12
		t.Fatalf("100 PPQ 1/32 应向下取整为 12，实得 %d", g)
	}
	if g, _ := gridTicks(1, "1/32"); g != 1 {
		t.Fatalf("网格下限应为 1 tick，实得 %d", g)
	}
	if _, err := gridTicks(480, "1/7"); err == nil {
		t.Fatal("未知量化单位应拒绝")
	}
}

func TestCompileVelocityScaleOffsetSetClamp(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	tr := TransformSpec{}
	tr.Velocity = &struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	}{Scale: ptrInt(200), Offset: ptrInt(-30)}
	ops, stats, err := CompileTransform(st, sel, tr)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, d := range ops[0].NoteDeltas {
		got[d.ID] = *d.Velocity
	}
	// n1 40→50, n2 100→170 clamp→127, n3 64→98, n4 20→10, n5 90→150→127, n6 90→150→127.
	if got["n1"] != 50 || got["n2"] != 127 || got["n3"] != 98 || got["n4"] != 10 || got["n5"] != 127 {
		t.Fatalf("力度 scale+offset 错误：%v", got)
	}
	if stats.Changed != 6 {
		t.Fatalf("力度变更数错误：%+v", stats)
	}
	transformOpsAccept(t, st, ops)

	// set alone.
	st2 := transformFixture()
	tr2 := TransformSpec{}
	tr2.Velocity = &struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	}{Set: ptrInt(100)}
	ops2, stats2, err := CompileTransform(st2, sel, tr2)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Changed != 5 { // n2 已是 100 不变；n1/n3/n4/n5/n6 → 100
		t.Fatalf("set 变更数错误：%+v", stats2)
	}
	for _, d := range ops2[0].NoteDeltas {
		if *d.Velocity != 100 {
			t.Fatalf("set 应统一为 100，%s=%d", d.ID, *d.Velocity)
		}
	}
}

func TestCompileVelocityValidation(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	mk := func(f func(*struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	})) TransformSpec {
		tr := TransformSpec{}
		tr.Velocity = &struct {
			Scale  *int `json:"scale,omitempty"`
			Offset *int `json:"offset,omitempty"`
			Set    *int `json:"set,omitempty"`
		}{}
		f(tr.Velocity)
		return tr
	}
	if _, _, err := CompileTransform(st, sel, mk(func(v *struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	}) {
		v.Set = ptrInt(64)
		v.Scale = ptrInt(100)
	})); err == nil {
		t.Fatal("set 与 scale 同给应拒绝")
	}
	if _, _, err := CompileTransform(st, sel, mk(func(v *struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	}) {
	})); err == nil {
		t.Fatal("velocity 全空应拒绝")
	}
	if _, _, err := CompileTransform(st, sel, mk(func(v *struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	}) {
		v.Scale = ptrInt(301)
	})); err == nil {
		t.Fatal("scale 301 应拒绝")
	}
}

func TestCompileShiftBounds(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	tr := TransformSpec{}
	tr.Shift = &struct {
		Ticks int64 `json:"ticks"`
	}{Ticks: -100}
	ops, stats, err := CompileTransform(st, sel, tr)
	if err != nil {
		t.Fatal(err)
	}
	// n1 10-100 < 0 skipped, n2..n6 shift by -100.
	if stats.Matched != 6 || stats.Changed != 5 || stats.SkippedTime != 1 {
		t.Fatalf("平移统计错误：%+v", stats)
	}
	for _, d := range ops[0].NoteDeltas {
		if d.ID == "n1" {
			t.Fatal("负起点音符不应产出 delta")
		}
	}
	transformOpsAccept(t, st, ops)
}

func TestCompileRequiresBoundedSelectionAndMatch(t *testing.T) {
	st := transformFixture()
	tr := TransformSpec{}
	tr.Transpose = &struct {
		Semitones int `json:"semitones"`
	}{Semitones: 1}
	if _, _, err := CompileTransform(st, Selection{}, tr); err == nil {
		t.Fatal("无限定选择应拒绝")
	}
	if _, _, err := CompileTransform(st, Selection{ClipIDs: []string{"clip_zzz"}}, tr); err == nil || !strings.Contains(err.Error(), "未匹配任何音符") {
		t.Fatalf("零匹配应拒绝：%v", err)
	}
	// Multiple transforms rejected.
	tr2 := TransformSpec{}
	tr2.Transpose = &struct {
		Semitones int `json:"semitones"`
	}{Semitones: 1}
	tr2.Shift = &struct {
		Ticks int64 `json:"ticks"`
	}{Ticks: 1}
	if _, _, err := CompileTransform(st, Selection{ClipIDs: []string{"clip_a"}}, tr2); err == nil {
		t.Fatal("多种变换同给应拒绝")
	}
	if _, _, err := CompileTransform(st, Selection{ClipIDs: []string{"clip_a"}}, TransformSpec{}); err == nil {
		t.Fatal("零变换应拒绝")
	}
}

func TestCompileNoChangeRejected(t *testing.T) {
	st := transformFixture()
	sel := Selection{ClipIDs: []string{"clip_a"}}
	tr := TransformSpec{}
	tr.Velocity = &struct {
		Scale  *int `json:"scale,omitempty"`
		Offset *int `json:"offset,omitempty"`
		Set    *int `json:"set,omitempty"`
	}{Scale: ptrInt(100)} // ×100% → no change
	if _, _, err := CompileTransform(st, sel, tr); err == nil {
		t.Fatal("零变更应拒绝，不产生空事务")
	}
}

func TestCompileQuantizeExplicitZeroAndDefault(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		changed   bool
	}{
		{"omitted", `{"quantize":{"unit":"1/16"}}`, true},
		{"zero", `{"quantize":{"unit":"1/16","strength":0}}`, false},
		{"full", `{"quantize":{"unit":"1/16","strength":100}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tr TransformSpec
			if err := json.Unmarshal([]byte(tc.raw), &tr); err != nil {
				t.Fatal(err)
			}
			ops, stats, err := CompileTransform(transformFixture(), Selection{ClipIDs: []string{"clip_a"}}, tr)
			if tc.changed {
				if err != nil || stats.Changed == 0 {
					t.Fatalf("expected quantization: stats=%+v err=%v", stats, err)
				}
			} else if err == nil || stats.Changed != 0 || len(ops) != 0 {
				t.Fatalf("zero strength must remain a rejected no-op: stats=%+v ops=%+v err=%v", stats, ops, err)
			}
		})
	}
}

func ptrInt(v int) *int       { return &v }
func ptrInt64(v int64) *int64 { return &v }
