package desktop

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (a *Workspace) verifyMixerE4(dir string) error {
	original := a.preview.Context()
	p, err := a.service.CreateProject("E4 混音验证", 480)
	if err != nil {
		return err
	}
	const track, clip, bus = "e4_track", "e4_clip", "e4_bus"
	tx, err := a.service.SubmitProjectOps(p.ID, "e4-seed", "gui", p.Revision, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{ID: track, Name: "混音测试"}},
		{Kind: "clip.add", Clip: &project.ClipSpec{ID: clip, TrackID: track, Length: 480}},
		{Kind: "note.add", ClipID: clip, Notes: []project.NewNote{{Pitch: 60, Duration: 470, Velocity: 96}}},
	})
	if err != nil {
		return err
	}
	seed, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	var originalView string
	if err = a.onMain(func() {
		originalView = a.preferences.View
		a.install(seed)
		a.preferences.View = "mixer"
		a.openMixer(track, "自动化")
		if f := a.form; f != nil && f.kind == "mixer" {
			f.mixer.channel, f.mixer.target, f.mixer.parameter = track, track, "pan"
			m := f.values.Mixer
			m.SetChannel(project.MixChannel{ID: bus, BusName: "混音返回", Gain: 0.8, Effects: []project.MixEffect{
				{ID: "e4_limiter", Kind: "limiter", Wet: 1},
			}})
			m.Routes = []project.MixRoute{{From: track, To: bus, Kind: "output", Gain: 1}}
			putAutomation(m, track, "pan", project.AutomationPoint{Tick: 0, Value: -1, Step: true})
			a.changedForm(f)
		}
	}); err != nil {
		return err
	}
	ready := func(condition func() bool) error {
		return waitCondition(func() bool {
			ok := false
			_ = a.onMain(func() { ok = condition() })
			return ok
		})
	}
	if err = ready(func() bool { return a.form != nil && a.form.writes == 0 && !a.form.saveFailed }); err != nil {
		return fmt.Errorf("混音草稿未确认：%w", err)
	}
	if err = a.onMain(func() { a.applyForm(a.form) }); err != nil {
		return err
	}
	if err = ready(func() bool { return a.form == nil && a.active.Revision == tx.Revision+1 }); err != nil {
		return fmt.Errorf("混音提交未确认：%w", err)
	}
	if err = a.onMain(func() { a.mutate("undo", nil) }); err != nil {
		return err
	}
	if err = ready(func() bool { return a.active.Revision == tx.Revision+2 && a.active.State.Mixer == nil }); err != nil {
		return fmt.Errorf("混音撤销未确认：%w", err)
	}
	if err = a.onMain(func() { a.mutate("redo", nil) }); err != nil {
		return err
	}
	if err = ready(func() bool { return a.active.Revision == tx.Revision+3 && a.active.State.Mixer != nil }); err != nil {
		return fmt.Errorf("混音重做未确认：%w", err)
	}
	path := filepath.Join(dir, "e4-mix.wav")
	if err = a.service.ExportProjectAudio(context.Background(), playback.ExportRequest{ProjectID: p.ID, Revision: tx.Revision + 3, Bits: 16}, path); err != nil {
		return err
	}
	wav, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(wav) != 44+24000*4 {
		return fmt.Errorf("补偿后导出时长错误：%d 字节", len(wav))
	}
	audible := false
	for i := 44; i < len(wav); i += 4 {
		audible = audible || binary.LittleEndian.Uint16(wav[i:]) != 0
		if binary.LittleEndian.Uint16(wav[i+2:]) != 0 {
			return fmt.Errorf("声像自动化右声道泄漏")
		}
	}
	if !audible {
		return fmt.Errorf("混音导出静音")
	}
	if err = a.service.DeleteProject(p.ID); err != nil {
		return err
	}
	old, err := a.service.GetProject(original.ProjectID)
	if err != nil {
		return err
	}
	return a.onMain(func() {
		a.preferences.View = originalView
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
