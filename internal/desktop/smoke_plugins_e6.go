package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"icetudio/internal/playback"
	"icetudio/internal/pluginhost"
	"icetudio/internal/project"
)

func (a *Workspace) verifyPluginCatalogE6(folder, evidence string) error {
	before := a.preview.Context()
	if err := a.onMain(a.openPluginCatalog); err != nil {
		return err
	}
	ready := func(check func() bool) error {
		return waitCondition(func() bool {
			var ok bool
			_ = a.onMain(func() { ok = a.pluginShelf.open && !a.pluginShelf.busy && check() })
			return ok
		})
	}
	if err := ready(func() bool { return true }); err != nil {
		return err
	}
	if err := a.onMain(func() { a.runPluginCatalog(folder, false) }); err != nil {
		return err
	}
	if err := ready(func() bool {
		return slices.ContainsFunc(a.pluginShelf.modules, func(m pluginhost.Module) bool {
			return m.Status == pluginhost.Ready && m.Format == "clap"
		}) && slices.ContainsFunc(a.pluginShelf.modules, func(m pluginhost.Module) bool {
			return m.Status == pluginhost.Ready && m.Format == "vst3"
		})
	}); err != nil {
		return fmt.Errorf("未在 Native 插件库看到两种格式：%w", err)
	}
	after := a.preview.Context()
	if before.ProjectID != after.ProjectID || before.Revision != after.Revision {
		return fmt.Errorf("扫描改变了工程修订")
	}
	var module pluginhost.Module
	var class pluginhost.Class
	if err := a.onMain(func() {
		a.pluginShelf.open = false
		for _, item := range a.pluginShelf.modules {
			if item.Status != pluginhost.Ready || item.Format != "clap" {
				continue
			}
			for _, candidate := range item.Classes {
				if candidate.Kind == "instrument" {
					module, class = item, candidate
					return
				}
			}
		}
	}); err != nil {
		return err
	}
	if class.ID == "" {
		return fmt.Errorf("没有已登记的 CLAP 乐器")
	}
	original, err := a.service.GetProject(before.ProjectID)
	if err != nil {
		return err
	}
	seed, err := a.service.CreateProject("E6 插件验收", 480)
	if err != nil {
		return err
	}
	const track, clip = "e6_track", "e6_clip"
	_, err = a.service.SubmitProjectOps(seed.ID, "e6_seed", "gui", seed.Revision, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{ID: track, Name: "插件测试"}},
		{Kind: "clip.add", Clip: &project.ClipSpec{ID: clip, TrackID: track, Length: 480}},
		{Kind: "note.add", ClipID: clip, Notes: []project.NewNote{{Pitch: 60, Velocity: 100, Duration: 420}}},
	})
	if err != nil {
		return err
	}
	seed, err = a.service.GetProject(seed.ID)
	if err != nil {
		return err
	}
	if err := a.onMain(func() {
		a.install(seed)
		a.openMixer(track, "通道")
		if f := a.form; f != nil {
			m := f.values.Mixer
			identity := pluginhost.Identity{Format: module.Format, Class: class.ID, SHA256: module.SHA256}
			a.preparePluginDraft(f, identity, "instrument", class.Name, nil, func(p *project.PluginState) {
				ch := m.Channel(track)
				ch.Instrument = &project.MixEffect{ID: "e6_instrument", Kind: "plugin", Wet: 1, Plugin: p}
				m.SetChannel(ch)
				a.changedForm(f)
			})
		}
	}); err != nil {
		return err
	}
	if err := waitCondition(func() bool {
		var ok bool
		_ = a.onMain(func() {
			ok = a.form != nil && !a.form.preparing && a.form.writes == 0 && a.form.values.Mixer.Channel(track).Instrument != nil
		})
		return ok
	}); err != nil {
		return fmt.Errorf("插件草稿未持久保存：%w", err)
	}
	if err := a.onMain(func() { a.applyForm(a.form) }); err != nil {
		return err
	}
	if err := waitCondition(func() bool {
		var ok bool
		_ = a.onMain(func() { ok = a.form == nil && a.active.ID == seed.ID && a.active.Revision == seed.Revision+1 })
		return ok
	}); err != nil {
		return fmt.Errorf("插件草稿未提交：%w", err)
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		return err
	}
	path := filepath.Join(evidence, "e6-plugin.wav")
	req := playback.ExportRequest{ProjectID: seed.ID, Revision: seed.Revision + 1, Bits: 16}
	if err := a.service.ExportProjectAudio(context.Background(), req, path); err != nil {
		return err
	}
	wav, err := os.ReadFile(path)
	if err != nil || len(wav) <= 44 || !slices.ContainsFunc(wav[44:], func(b byte) bool { return b != 0 }) {
		return fmt.Errorf("Native 插件导出无音频：%v", err)
	}
	return a.onMain(func() {
		a.install(original)
		a.selectClip(before.Track, before.Clip)
	})
}
