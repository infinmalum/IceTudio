package project

import (
	"encoding/json"
	"testing"
)

func TestDependentBatchInverseRoundTrip(t *testing.T) {
	for name, ops := range map[string][]Op{
		"rename twice": {{Kind: "project.rename", Title: "中间名"}, {Kind: "project.rename", Title: "最终名"}},
		"grow then add": {
			{Kind: "clip.update", ClipID: "c_base", ClipPatch: &ClipPatch{Length: ptrI64(960)}},
			{Kind: "note.add", ClipID: "c_base", Notes: []NewNote{{ID: "n_added", Start: 480, Duration: 480, Pitch: 67, Velocity: 90}}},
		},
		"grow then move": {
			{Kind: "clip.update", ClipID: "c_base", ClipPatch: &ClipPatch{Length: ptrI64(960)}},
			{Kind: "note.update", ClipID: "c_base", NoteDeltas: []NoteDelta{{ID: "n_base", Start: ptrI64(480)}}},
		},
		"remove then shrink": {
			{Kind: "note.remove", ClipID: "c_base", NoteIDs: []string{"n_base"}},
			{Kind: "clip.update", ClipID: "c_base", ClipPatch: &ClipPatch{Length: ptrI64(120)}},
		},
		"create hierarchy": {
			{Kind: "track.add", Track: &TrackSpec{ID: "t_new", Name: "新轨"}},
			{Kind: "clip.add", Clip: &ClipSpec{ID: "c_new", TrackID: "t_new", Length: 480}},
			{Kind: "note.add", ClipID: "c_new", Notes: []NewNote{{ID: "n_new", Duration: 240, Pitch: 60, Velocity: 100}}},
		},
		"repeated note delta": {{Kind: "note.update", ClipID: "c_base", NoteDeltas: []NoteDelta{
			{ID: "n_base", Pitch: ptr(64), Velocity: ptr(80)},
			{ID: "n_base", Pitch: ptr(67)},
		}}},
		"tempo twice": {
			{Kind: "tempo.set", Tempo: []TempoPoint{{Tick: 0, USPerQuarter: 600000}}},
			{Kind: "tempo.set", Tempo: []TempoPoint{{Tick: 0, USPerQuarter: 750000}, {Tick: 240, USPerQuarter: 500001}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			st, err := Blank("原名", 480)
			if err != nil {
				t.Fatal(err)
			}
			mustApply(t, &st, Op{Kind: "track.add", Track: &TrackSpec{ID: "t_base", Name: "原轨"}})
			mustApply(t, &st, Op{Kind: "clip.add", Clip: &ClipSpec{ID: "c_base", TrackID: "t_base", Length: 480}})
			mustApply(t, &st, Op{Kind: "note.add", ClipID: "c_base", Notes: []NewNote{{ID: "n_base", Duration: 240, Pitch: 60, Velocity: 100}}})
			mustApply(t, &st, Op{Kind: "control.add", ClipID: "c_base", Events: []EventSpec{{ID: "e_base", Tick: 0, Kind: KindCC, Controller: 64, Value: 127}}})
			st.Tracks[0].Clips[0].Raw = []RawEvent{{ID: "e_raw", Tick: 0, Data: []byte{0xff, 0x06, 1, 'A'}}}
			before, _ := json.Marshal(st)
			inverse, _ := mustApply(t, &st, ops...)
			edited, _ := json.Marshal(st)
			mustApply(t, &st, inverse...)
			restored, _ := json.Marshal(st)
			if string(before) != string(restored) {
				t.Fatalf("inverse did not restore original state:\nbefore %s\nafter  %s", before, restored)
			}
			mustApply(t, &st, ops...)
			redone, _ := json.Marshal(st)
			if string(edited) != string(redone) {
				t.Fatalf("redo changed the original edit:\nedit %s\nredo %s", edited, redone)
			}
		})
	}
}

func mustApply(t *testing.T, st *State, ops ...Op) (inverse []Op, result Result) {
	t.Helper()
	inv, res, err := ApplyTransaction(st, ops)
	if err != nil {
		t.Fatalf("ApplyTransaction: %v", err)
	}
	return inv, res
}

func TestConvertTick(t *testing.T) {
	// same PPQ is identity
	if got, _ := ConvertTick(480, 480, 480); got != 480 {
		t.Fatalf("identity: %d", got)
	}
	// 480 -> 960 doubles
	if got, _ := ConvertTick(479, 480, 960); got != 958 {
		t.Fatalf("479@480->960: %d", got)
	}
	// 960 -> 480: 961 halves to 480.5 -> rounds up to 481
	if got, _ := ConvertTick(961, 960, 480); got != 481 {
		t.Fatalf("961@960->480: %d", got)
	}
	if _, err := ConvertTick(1, 0, 480); err == nil {
		t.Fatal("srcPPQ=0 must fail")
	}
	if _, err := ConvertTick(1, MaxPPQ+1, 480); err == nil {
		t.Fatal("oversize PPQ must fail")
	}
	if _, err := ConvertTick(-1, 480, 480); err == nil {
		t.Fatal("negative tick must fail")
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"t_abc123", "n_0", "a"} {
		if !ValidID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "T_UP", "with space", "中文", string(make([]byte, 65))} {
		if ValidID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestNoteAddUpdateRemoveAndUndo(t *testing.T) {
	st, _ := Blank("测试", 480)
	inv1, _ := mustApply(t, &st, Op{Kind: "track.add", Track: &TrackSpec{Name: "主旋律", Channel: 0, Program: 0}})
	mustApply(t, &st, Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: track0(t, &st), Name: "A", Start: 0, Length: 1920}})

	inv3, res3 := mustApply(t, &st, Op{Kind: "note.add", ClipID: clip0(t, &st), Notes: []NewNote{
		{Start: 0, Duration: 480, Pitch: 60, Velocity: 100},
		{Start: 480, Duration: 240, Pitch: 64, Velocity: 90},
	}})
	if len(res3.CreatedIDs) != 2 || res3.Summary["note.add"] != 1 {
		t.Fatalf("unexpected result %+v", res3)
	}
	if len(st.Tracks[0].Clips[0].Notes) != 2 {
		t.Fatalf("notes not added")
	}
	// idempotent replay of the same inverse must be a valid transaction too
	if len(inv1) != 1 || inv1[0].Kind != "track.remove" {
		t.Fatalf("track.add inverse wrong: %+v", inv1)
	}
	if len(inv3) != 1 || inv3[0].Kind != "note.remove" || len(inv3[0].NoteIDs) != 2 {
		t.Fatalf("note.add inverse wrong: %+v", inv3)
	}

	// update captures old values (round-trip restores exactly)
	inv4, _ := mustApply(t, &st, Op{Kind: "note.update", ClipID: clip0(t, &st), NoteDeltas: []NoteDelta{
		{ID: st.Tracks[0].Clips[0].Notes[0].ID, Pitch: ptr(72), Start: ptrI64(240)},
	}})
	if st.Tracks[0].Clips[0].Notes[0].Pitch != 72 || st.Tracks[0].Clips[0].Notes[0].Start != 240 {
		t.Fatal("update not applied")
	}
	if _, _, err := ApplyTransaction(&st, inv4); err != nil {
		t.Fatalf("inverse update failed: %v", err)
	}
	if st.Tracks[0].Clips[0].Notes[0].Pitch != 60 || st.Tracks[0].Clips[0].Notes[0].Start != 0 {
		t.Fatalf("inverse update did not restore: %+v", st.Tracks[0].Clips[0].Notes[0])
	}
}

func TestBatchIsAtomic(t *testing.T) {
	st, _ := Blank("批", 480)
	mustApply(t, &st, Op{Kind: "track.add", Track: &TrackSpec{}})
	track := track0(t, &st)
	mustApply(t, &st, Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: track, Start: 0, Length: 480}})
	before, _ := json.Marshal(st)

	// second op is invalid: whole batch must fail leaving state untouched
	_, _, err := ApplyTransaction(&st, []Op{
		{Kind: "project.rename", Title: "改名"},
		{Kind: "note.add", ClipID: clip0(t, &st), Notes: []NewNote{{Start: 0, Duration: 100, Pitch: 200, Velocity: 1}}},
	})
	if err == nil {
		t.Fatal("batch with invalid pitch must fail")
	}
	after, _ := json.Marshal(st)
	if string(before) != string(after) {
		t.Fatal("failed batch mutated state")
	}
	if st.Title == "改名" {
		t.Fatal("rename from failed batch leaked")
	}
}

func TestRemoveRestoreRoundTrip(t *testing.T) {
	st, _ := Blank("删恢复", 480)
	mustApply(t, &st, Op{Kind: "track.add", Track: &TrackSpec{Name: "轨", Channel: 3, Program: 40}})
	track := track0(t, &st)
	mustApply(t, &st, Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: track, Start: 0, Length: 960}})
	clip := clip0(t, &st)
	mustApply(t, &st, Op{Kind: "note.add", ClipID: clip, Notes: []NewNote{{ID: "n_fixed", Start: 0, Duration: 480, Pitch: 60, Velocity: 100}}})
	mustApply(t, &st, Op{Kind: "control.add", ClipID: clip, Events: []EventSpec{{Tick: 0, Kind: KindCC, Channel: 3, Controller: 64, Value: 127}}})

	snapshot, _ := json.Marshal(st)
	inv, _ := mustApply(t, &st, Op{Kind: "track.remove", TrackID: track})
	if len(st.Tracks) != 0 {
		t.Fatal("track not removed")
	}
	if _, _, err := ApplyTransaction(&st, inv); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	restored, _ := json.Marshal(st)
	if string(snapshot) != string(restored) {
		t.Fatalf("restore not exact\n%s\n%s", snapshot, restored)
	}
}

func TestUndoRedoLinearHistory(t *testing.T) {
	st, _ := Blank("线性历史", 480)
	var history []HistoryEntry
	rev := int64(0)
	commit := func(ops ...Op) HistoryEntry {
		inv, res, err := ApplyTransaction(&st, ops)
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		res.Revision = rev
		res.OpID = "op"
		h := HistoryEntry{Revision: rev, Kind: EditKind, Ops: ops, Inverse: inv, Result: res}
		history = append(history, h)
		rev++
		return h
	}

	edit1 := commit(Op{Kind: "project.rename", Title: "一"})
	edit2 := commit(Op{Kind: "project.rename", Title: "二"})
	if st.Title != "二" {
		t.Fatalf("title: %q", st.Title)
	}

	// undo #2
	target, ops, err := PlanUndo(history)
	if err != nil || target.Revision != edit2.Revision {
		t.Fatalf("undo target: %v %+v", err, target)
	}
	_, res, err := ApplyTransaction(&st, ops)
	if err != nil || st.Title != "一" {
		t.Fatalf("undo apply: %v title=%q", err, st.Title)
	}
	history = append(history, HistoryEntry{Revision: rev, Kind: UndoKind, TargetRevision: target.Revision, Ops: ops, Result: res})
	rev++
	history[edit2.Revision].Undone = true

	// undo #1 too
	target, ops, err = PlanUndo(history)
	if err != nil || target.Revision != edit1.Revision {
		t.Fatalf("second undo target: %v %+v", err, target)
	}
	_, res, err = ApplyTransaction(&st, ops)
	if err != nil || st.Title != "线性历史" {
		t.Fatalf("second undo: %v title=%q", err, st.Title)
	}
	history = append(history, HistoryEntry{Revision: rev, Kind: UndoKind, TargetRevision: target.Revision, Ops: ops, Result: res})
	rev++

	// redo back to "一"
	target, ops, err = PlanRedo(history)
	if err != nil || target.TargetRevision != edit1.Revision {
		t.Fatalf("redo: %v %+v", err, target)
	}
	if _, _, err = ApplyTransaction(&st, ops); err != nil {
		t.Fatalf("redo apply: %v", err)
	}
	if st.Title != "一" {
		t.Fatalf("after redo: %q", st.Title)
	}

	// a fresh edit invalidates redo
	commit(Op{Kind: "project.rename", Title: "三"})
	if _, _, err := PlanRedo(history); err == nil {
		t.Fatal("redo must be invalid after a new edit")
	}
}

func TestClipUpdateRejectsShrinkWithContents(t *testing.T) {
	st, _ := Blank("收缩", 480)
	mustApply(t, &st, Op{Kind: "track.add", Track: &TrackSpec{}})
	mustApply(t, &st, Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: track0(t, &st), Start: 0, Length: 960}})
	mustApply(t, &st, Op{Kind: "note.add", ClipID: clip0(t, &st), Notes: []NewNote{{Start: 480, Duration: 480, Pitch: 60, Velocity: 80}}})
	short := int64(480)
	_, _, err := ApplyTransaction(&st, []Op{{Kind: "clip.update", ClipID: clip0(t, &st), ClipPatch: &ClipPatch{Length: &short}}})
	if err == nil {
		t.Fatal("shrinking below note end must fail")
	}
}

func TestTempoMeterNormalization(t *testing.T) {
	st, _ := Blank("图", 480)
	mustApply(t, &st,
		Op{Kind: "tempo.set", Tempo: []TempoPoint{{Tick: 480, USPerQuarter: 600000}, {Tick: 0, USPerQuarter: 500000}, {Tick: 480, USPerQuarter: 400000}}},
		Op{Kind: "meter.set", Meter: []MeterPoint{{Tick: 0, Numerator: 3, Denominator: 4}}},
	)
	if len(st.Tempo) != 2 || st.Tempo[1].USPerQuarter != 400000 {
		t.Fatalf("tempo dedupe/sort wrong: %+v", st.Tempo)
	}
	bad := int64(10)
	if _, _, err := ApplyTransaction(&st, []Op{{Kind: "meter.set", Meter: []MeterPoint{{Tick: 0, Numerator: 3, Denominator: 3}}}}); err == nil {
		t.Fatal("denominator 3 must fail")
	}
	if _, _, err := ApplyTransaction(&st, []Op{{Kind: "tempo.set", Tempo: []TempoPoint{{Tick: bad, USPerQuarter: 500000}}}}); err == nil {
		t.Fatal("tempo without tick 0 must fail")
	}
}

func track0(t *testing.T, st *State) string {
	t.Helper()
	if len(st.Tracks) == 0 {
		t.Fatal("no track")
	}
	return st.Tracks[0].ID
}

func clip0(t *testing.T, st *State) string {
	t.Helper()
	if len(st.Tracks[0].Clips) == 0 {
		t.Fatal("no clip")
	}
	return st.Tracks[0].Clips[0].ID
}

func ptr(v int) *int        { return &v }
func ptrI64(v int64) *int64 { return &v }

func TestRegionFilters(t *testing.T) {
	st, _ := Blank("范围", 480)
	mustApply(t, &st,
		Op{Kind: "track.add", Track: &TrackSpec{Name: "A"}},
		Op{Kind: "track.add", Track: &TrackSpec{Name: "B"}},
	)
	a, b := st.Tracks[0].ID, st.Tracks[1].ID
	mustApply(t, &st,
		Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: a, Name: "a1", Start: 0, Length: 3840}},
		Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: b, Name: "b1", Start: 0, Length: 3840}},
	)
	clipA := st.Tracks[0].Clips[0].ID
	mustApply(t, &st,
		Op{Kind: "note.add", ClipID: clipA, Notes: []NewNote{
			{Start: 0, Duration: 480, Pitch: 60, Velocity: 100},
			{Start: 960, Duration: 480, Pitch: 62, Velocity: 100},
			{Start: 2400, Duration: 480, Pitch: 64, Velocity: 100},
		}},
		Op{Kind: "control.add", ClipID: clipA, Events: []EventSpec{
			{Tick: 480, Kind: KindCC, Controller: 7, Value: 100},
			{Tick: 2400, Kind: KindCC, Controller: 7, Value: 20},
		}},
	)

	full := Region(&st, "", "", nil, nil)
	if len(full.Tracks) != 2 || len(full.Tempo) == 0 {
		t.Fatalf("unscoped region must keep everything: %d tracks", len(full.Tracks))
	}

	onlyB := Region(&st, b, "", nil, nil)
	if len(onlyB.Tracks) != 1 || onlyB.Tracks[0].ID != b || len(onlyB.Tracks[0].Clips) != 1 {
		t.Fatalf("track filter returned wrong shape: %+v", onlyB.Tracks)
	}
	if len(onlyB.Tempo) == 0 {
		t.Fatal("tempo map must pass through in scoped regions")
	}

	mid := Region(&st, a, "", ptrI64(480), ptrI64(1920))
	clip := mid.Tracks[0].Clips[0]
	// note at 0 ends exactly at 480 -> excluded; 960 overlaps; 2400 outside;
	// control at 480 in, control at 2400 out.
	if len(clip.Notes) != 1 || clip.Notes[0].Pitch != 62 {
		t.Fatalf("window notes wrong: %+v", clip.Notes)
	}
	if len(clip.Controls) != 1 || clip.Controls[0].Tick != 480 {
		t.Fatalf("window controls wrong: %+v", clip.Controls)
	}

	onlyClip := Region(&st, a, clipA, nil, nil)
	if len(onlyClip.Tracks[0].Clips) != 1 || len(onlyClip.Tracks[0].Clips[0].Notes) != 3 {
		t.Fatalf("clip filter wrong: %+v", onlyClip.Tracks[0].Clips)
	}
	if (Region(&st, "t_missing", "", nil, nil)).Tracks != nil {
		t.Fatal("unknown track must yield no tracks")
	}
}

func TestTrackAndClipColorPaletteTransactions(t *testing.T) {
	st, _ := Blank("颜色", 480)
	str := func(s string) *string { return &s }
	// 合法颜色：track.add / clip.add 携带并保存。
	mustApply(t, &st, Op{Kind: "track.add", Track: &TrackSpec{ID: "t_red", Name: "主旋律", Color: "coral", Channel: 0, Program: 40}})
	mustApply(t, &st, Op{Kind: "clip.add", Clip: &ClipSpec{ID: "c_violet", TrackID: "t_red", Name: "A", Color: "violet", Start: 0, Length: 480}})
	if st.Tracks[0].Color != "coral" || st.Tracks[0].Clips[0].Color != "violet" {
		t.Fatalf("color not stored: %+v", st.Tracks[0])
	}
	// track.update / clip.update 修改颜色，逆操作恢复原值。
	inv, _, _ := ApplyTransaction(&st, []Op{Op{Kind: "track.update", TrackID: "t_red", TrackPatch: &TrackPatch{Color: str("ice")}}})
	if st.Tracks[0].Color != "ice" {
		t.Fatalf("color not updated: %s", st.Tracks[0].Color)
	}
	if _, _, err := ApplyTransaction(&st, []Op{inv[0]}); err != nil {
		t.Fatal(err)
	}
	if st.Tracks[0].Color != "coral" {
		t.Fatalf("inverse did not restore color: %s", st.Tracks[0].Color)
	}
	invC, _, _ := ApplyTransaction(&st, []Op{Op{Kind: "clip.update", ClipID: "c_violet", ClipPatch: &ClipPatch{Color: str("")}}})
	if st.Tracks[0].Clips[0].Color != "" {
		t.Fatal("clearing clip color failed")
	}
	if _, _, err := ApplyTransaction(&st, []Op{invC[0]}); err != nil {
		t.Fatal(err)
	}
	if st.Tracks[0].Clips[0].Color != "violet" {
		t.Fatal("inverse did not restore clip color")
	}
	// 越界调色板值拒绝：add 与 update 都不留部分变更。
	before := st
	if _, _, err := ApplyTransaction(&st, []Op{{Kind: "track.add", Track: &TrackSpec{ID: "t_bad", Color: "hotpink", Channel: 0, Program: 0}}}); err == nil {
		t.Fatal("off-palette track color accepted")
	}
	if _, _, err := ApplyTransaction(&st, []Op{Op{Kind: "track.update", TrackID: "t_red", TrackPatch: &TrackPatch{Color: str("#ff0000")}}}); err == nil {
		t.Fatal("off-palette track patch accepted")
	}
	if _, _, err := ApplyTransaction(&st, []Op{Op{Kind: "clip.add", Clip: &ClipSpec{TrackID: "t_red", Color: "neon", Start: 0, Length: 10}}}); err == nil {
		t.Fatal("off-palette clip color accepted")
	}
	if _, _, err := ApplyTransaction(&st, []Op{Op{Kind: "clip.update", ClipID: "c_violet", ClipPatch: &ClipPatch{Color: str("深蓝")}}}); err == nil {
		t.Fatal("off-palette clip patch accepted")
	}
	if len(before.Tracks) != len(st.Tracks) || st.Tracks[0].Color != "coral" || st.Tracks[0].Clips[0].Color != "violet" {
		t.Fatal("rejected color ops mutated state")
	}
	// remove → 逆操作恢复颜色。
	invR, _, _ := ApplyTransaction(&st, []Op{Op{Kind: "track.remove", TrackID: "t_red"}})
	if len(st.Tracks) != 0 {
		t.Fatal("track not removed")
	}
	if _, _, err := ApplyTransaction(&st, []Op{invR[0]}); err != nil {
		t.Fatal(err)
	}
	if st.Tracks[0].Color != "coral" || st.Tracks[0].Clips[0].Color != "violet" {
		t.Fatalf("undo of remove lost colors: %+v", st.Tracks[0])
	}
}
