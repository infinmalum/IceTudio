package desktop

import (
	"fmt"
	"path/filepath"
	"reflect"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

// Exercise the production form callbacks, draft queue and edit journal on a
// disposable eight-bar project. A compiled score is not a sound-device test.
func (a *Workspace) verifyMIDICreation(dir string) error {
	original := a.preview.Context()
	p, err := a.service.CreateProject("E2 八小节", 480)
	if err != nil {
		return err
	}
	ops := []project.Op{}
	for i, name := range []string{"鼓", "贝斯", "和弦"} {
		tid, cid := fmt.Sprintf("e2_track_%d", i), fmt.Sprintf("e2_clip_%d", i)
		channel := i
		if i == 0 {
			channel = 9
		}
		ops = append(ops, project.Op{Kind: "track.add", Track: &project.TrackSpec{ID: tid, Name: name, Channel: channel}},
			project.Op{Kind: "clip.add", Clip: &project.ClipSpec{ID: cid, TrackID: tid, Name: name, Length: 480 * 32}})
	}
	if _, err = a.service.SubmitProjectOps(p.ID, "setup-e2", "gui", p.Revision, ops); err != nil {
		return err
	}
	p, err = a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	if err = a.onMain(func() { a.install(p) }); err != nil {
		return err
	}
	apply := func(index int, kind string, fill func(*midiInputs)) error {
		old, e := a.service.GetProject(p.ID)
		if e != nil {
			return e
		}
		if e = a.onMain(func() {
			t := a.active.State.Tracks[index]
			a.selectClip(t, t.Clips[0])
			a.openMIDIEditor(kind, t, t.Clips[0])
			if a.form != nil && a.form.kind == kind {
				fill(a.form.values.MIDI)
				a.changedForm(a.form)
			}
		}); e != nil {
			return e
		}
		ready := false
		if e = waitCondition(func() bool {
			_ = a.onMain(func() { ready = a.form != nil && a.form.writes == 0 && !a.form.saveFailed })
			return ready
		}); e != nil {
			return fmt.Errorf("%s 草稿: %w", kind, e)
		}
		if e = a.onMain(func() { a.applyForm(a.form) }); e != nil {
			return e
		}
		if e = waitCondition(func() bool {
			_ = a.onMain(func() { ready = a.form == nil && a.active.Revision == old.Revision+1 && !a.editBusy })
			return ready
		}); e != nil {
			msg := ""
			_ = a.onMain(func() {
				if a.form != nil {
					msg = a.form.err
				}
				msg += " " + a.message
			})
			return fmt.Errorf("%s 提交: %w %s", kind, e, msg)
		}
		return nil
	}
	if err = apply(0, "midi_steps", func(v *midiInputs) {
		v.DrumLanes = []drumLane{{Pitch: "36", Velocity: "92", Steps: 0x1111}, {Pitch: "38", Velocity: "78", Steps: 0x1010}}
		v.Bars = "8"
	}); err != nil {
		return err
	}
	if err = apply(1, "midi_chords", func(v *midiInputs) {
		v.Pitch = "36"
		v.Voicing = "贝斯根音"
		v.Progression = "I I I I IV IV IV IV V V V V I I I I I I I I IV IV IV IV V V V V I I I I"
	}); err != nil {
		return err
	}
	if err = apply(2, "midi_chords", func(v *midiInputs) {
		v.Progression = "I I I I IV IV IV IV V V V V I I I I I I I I IV IV IV IV V V V V I I I I"
	}); err != nil {
		return err
	}
	if err = apply(2, "midi_control", func(v *midiInputs) { v.Controller = "11"; v.Value = "93" }); err != nil {
		return err
	}
	if err = apply(2, "midi_marker", func(v *midiInputs) { v.Value = "主歌" }); err != nil {
		return err
	}
	before, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	if err = apply(2, "midi_arrange", func(v *midiInputs) { v.Action = "拆分"; v.First = "5:1:0" }); err != nil {
		return err
	}
	split, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	if len(split.State.Tracks[2].Clips) != 2 || len(split.State.Tracks[0].Clips[0].Notes) != 48 {
		return fmt.Errorf("E2 拆分或鼓点数不符")
	}
	score, err := playback.Compile(playback.Snapshot{ID: p.ID, Revision: split.Revision, State: split.State})
	if err != nil || score.EndTick != 480*32 {
		return fmt.Errorf("E2 播放编译: %v", err)
	}
	path := filepath.Join(dir, "e2-eight-bars.mid")
	if err = a.service.ExportProjectMIDI(p.ID, path); err != nil {
		return err
	}
	round, err := a.service.ImportProjectMIDI(path)
	if err != nil {
		return err
	}
	markers, cc, notes := 0, 0, 0
	for _, t := range round.State.Tracks {
		for _, c := range t.Clips {
			notes += len(c.Notes)
			for _, e := range c.Controls {
				if e.Kind == project.KindCC && e.Controller == 11 && e.Value == 93 {
					cc++
				}
			}
			for _, r := range c.Raw {
				if name, ok := project.MarkerName(r.Data); ok && name == "主歌" {
					markers++
				}
			}
		}
	}
	if notes != 176 || cc != 1 || markers != 1 {
		return fmt.Errorf("E2 MIDI 往返: %d 音符、%d CC、%d 标记", notes, cc, markers)
	}
	if _, err = a.service.UndoProject(p.ID, "e2-undo", "gui", split.Revision); err != nil {
		return err
	}
	restored, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before.State, restored.State) {
		return fmt.Errorf("E2 撤销未恢复拆分前状态")
	}
	if err = a.onMain(func() { a.install(restored) }); err != nil {
		return err
	}
	if err = apply(0, "midi_arrange", func(v *midiInputs) { v.Action = "共享铺排"; v.First = "9:1:0" }); err != nil {
		return err
	}
	if err = apply(0, "midi_control", func(v *midiInputs) { v.Controller = "11"; v.Value = "82" }); err != nil {
		return err
	}
	linked, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	clips := linked.State.Tracks[0].Clips
	if len(clips) != 2 || len(linked.State.Patterns) != 1 || clips[0].PatternID == "" ||
		clips[0].PatternID != clips[1].PatternID || len(clips[1].Controls) != 1 || clips[1].Controls[0].Value != 82 {
		return fmt.Errorf("E2 Native 共享 Pattern 未同步")
	}
	if err = waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
		return err
	}
	activated, err := a.piano.Page().Eval(`(()=>{const b=[...document.querySelectorAll("button")].find(b=>b.textContent==="控制通道");if(!b)return false;b.click();return true})()`)
	if err != nil || activated != true {
		return fmt.Errorf("E2 卷帘控制通道未挂载: %v", err)
	}
	if err = waitCondition(func() bool {
		v, e := a.piano.Page().Eval(`(()=>{const b=[...document.querySelectorAll("button")].find(b=>b.textContent==="在视口起点添加");if(!b||b.disabled)return false;b.click();return true})()`)
		return e == nil && v == true
	}); err != nil {
		return fmt.Errorf("E2 控制点按钮不可用: %w", err)
	}
	if err = waitCondition(func() bool {
		q, e := a.service.GetProject(p.ID)
		return e == nil && q.Revision == linked.Revision+1 && a.edits.State().Pending == nil
	}); err != nil {
		return fmt.Errorf("E2 控制点提交未确认: %w", err)
	}
	afterLane, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	clips = afterLane.State.Tracks[0].Clips
	if len(clips[0].Controls) != 2 || len(clips[1].Controls) != 2 ||
		clips[1].Controls[1].Controller != 64 || clips[1].Controls[1].Value != 64 {
		return fmt.Errorf("E2 WebView 控制点未同步到共享实例")
	}
	_, _ = a.piano.Page().Eval(`(()=>{[...document.querySelectorAll("button")].find(b=>b.textContent==="控制通道")?.click();return true})()`)

	if err = a.service.DeleteProject(round.ID); err != nil {
		return err
	}
	if err = a.service.DeleteProject(p.ID); err != nil {
		return err
	}
	old, err := a.service.GetProject(original.ProjectID)
	if err != nil {
		return err
	}
	return a.onMain(func() {
		a.install(old)
		for _, t := range old.State.Tracks {
			for _, c := range t.Clips {
				if c.ID == original.ClipID {
					a.selectClip(t, c)
					return
				}
			}
		}
	})
}
