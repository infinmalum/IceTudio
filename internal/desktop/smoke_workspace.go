package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// Opt-in smoke-edit only. Extra projects are created in the disposable host;
// the original music is untouched. This exercises the actual Native callbacks,
// asynchronous draft queue, service events, SQLite, SMF and live WebView.
func (a *Workspace) verifyNativeWorkspace(dir string) error {
	original := a.preview.Context()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	waitUI := func(read func() bool) error {
		return waitCondition(func() bool {
			value := false
			if err := a.onMain(func() { value = read() }); err != nil {
				return false
			}
			return value
		})
	}
	if err := waitUI(func() bool { return a.projectAvailable() && a.mapReady }); err != nil {
		return err
	}
	var f *propertyForm
	if err := a.onMain(func() {
		a.beginProperties("create", project.Track{}, project.Clip{})
		f = a.form
		if f != nil {
			f.values.Name = "Native 创建 / 导入导出验证"
			a.changedForm(f)
		}
	}); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("Native 新建入口不可用")
	}
	if err := waitUI(func() bool { return f.writes == 0 && f.err == "" }); err != nil {
		return fmt.Errorf("新建草稿保存：%w", err)
	}
	entries, err := a.drafts.list()
	if err != nil || len(entries) == 0 {
		return fmt.Errorf("新建输入未持久确认：%v", err)
	}
	if err := a.onMain(func() { a.applyForm(f) }); err != nil {
		return err
	}
	var created storage.Project
	if err := waitUI(func() bool {
		if a.form == nil && a.active.Title == "Native 创建 / 导入导出验证" {
			created = a.active
			return true
		}
		return false
	}); err != nil {
		return fmt.Errorf("Native 新建工程：%w", err)
	}
	if err := a.onMain(a.addTrack); err != nil {
		return err
	}
	if err := waitUI(func() bool {
		return a.active.ID == created.ID && a.active.Revision == 1 && !a.editBusy && len(a.active.State.Tracks) == 1
	}); err != nil {
		return fmt.Errorf("Native 添加音轨：%w", err)
	}
	if err := a.onMain(func() {
		track := a.active.State.Tracks[0]
		a.beginProperties("track", track, track.Clips[0])
		f = a.form
		if f != nil {
			f.values.Name = "中文弦乐"
			f.values.Program = "41"
			f.values.Color = "mint"
			a.changedForm(f)
		}
	}); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("Native 轨道属性不可用")
	}
	if err := waitUI(func() bool { return f.writes == 0 && f.err == "" }); err != nil {
		return err
	}
	if err := a.onMain(func() { a.applyForm(f) }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.form == nil && a.active.ID == created.ID && a.active.Revision == 2 }); err != nil {
		return fmt.Errorf("Native 音色属性提交：%w", err)
	}
	var mf *timeMapForm
	if err := a.onMain(func() {
		a.openTimeMap(false)
		mf = a.mapForm
		if mf != nil {
			mf.snapshot.Draft.Tempo[0].BPM = "90"
			mf.snapshot.Draft.Meter[0].Numerator = "7"
			mf.snapshot.Draft.Meter[0].Denominator = "8"
			a.checkpointMap(mf)
		}
	}); err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("Native 时间图不可用")
	}
	if err := waitUI(func() bool { return mf.writes == 0 && mf.err == "" }); err != nil {
		return err
	}
	if err := a.onMain(func() { a.applyTimeMap(mf) }); err != nil {
		return err
	}
	if err := waitUI(func() bool {
		return a.mapForm == nil && a.active.ID == created.ID && a.active.Revision == 3 && !a.editBusy
	}); err != nil {
		return fmt.Errorf("Native 时间图提交：%w", err)
	}
	// Exercise the clear tombstone by reopening and writing another checkpoint.
	if err := a.onMain(func() {
		a.openTimeMap(false)
		mf = a.mapForm
		if mf != nil {
			mf.snapshot.Draft.Tempo[0].BPM = "91"
			a.checkpointMap(mf)
		}
	}); err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("清除后不能重开时间图")
	}
	if err := waitUI(func() bool { return mf.writes == 0 && mf.err == "" }); err != nil {
		return fmt.Errorf("时间图清除屏障后保存：%w", err)
	}
	if err := a.onMain(func() { a.clearMap(mf) }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.mapForm == nil }); err != nil {
		return err
	}
	// Capture the real native form before applying, so compositing and layout are reviewable.
	if err := a.onMain(func() {
		t := a.active.State.Tracks[0]
		a.beginProperties("clip", t, t.Clips[0])
		f = a.form
		if f != nil {
			f.values.Name = "精确位置"
			f.values.Start = "12345"
			a.changedForm(f)
		}
	}); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("Native 片段属性不可用")
	}
	if err := waitUI(func() bool { return f.writes == 0 && f.err == "" }); err != nil {
		return err
	}
	img, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "native-properties.png"), img, 0600); err != nil {
		return err
	}
	if err := a.onMain(func() { a.applyForm(f) }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.form == nil && a.active.ID == created.ID && a.active.Revision == 4 }); err != nil {
		return err
	}
	saved, err := a.service.GetProject(created.ID)
	if err != nil {
		return err
	}
	if saved.State.Tracks[0].Program != 40 || saved.State.Tracks[0].Color != "mint" || saved.State.Tracks[0].Clips[0].Start != 12345 || saved.State.Tempo[0].USPerQuarter != 666667 || saved.State.Meter[0].Numerator != 7 {
		return fmt.Errorf("Native 属性持久结果不一致")
	}
	path := filepath.Join(dir, "native-roundtrip.mid")
	if err := a.service.ExportProjectMIDI(saved.ID, path); err != nil {
		return err
	}
	imported, err := a.service.ImportProjectMIDI(path)
	if err != nil {
		return err
	}
	if len(imported.State.Tracks) == 0 || imported.State.Tempo[0].USPerQuarter != 666667 || imported.State.Meter[0].Numerator != 7 {
		return fmt.Errorf("MIDI 导出导入时间图不一致")
	}
	if err := a.service.DeleteProject(imported.ID); err != nil {
		return err
	}
	if err := a.service.DeleteProject(saved.ID); err != nil {
		return err
	}
	entries, err = a.drafts.list()
	if err != nil || len(entries) != 0 {
		return fmt.Errorf("应用成功后仍有 Native 草稿：%v", err)
	}
	r, err := a.service.GetTimeMapDraft()
	if err != nil || r == nil || r.Payload != "" {
		return fmt.Errorf("时间图草稿未清理：%v", err)
	}
	if err := a.onMain(func() { a.open(original.ProjectID) }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.active.ID == original.ProjectID && a.projectAvailable() }); err != nil {
		return err
	}
	if err := a.onMain(func() {
		for _, t := range a.active.State.Tracks {
			for _, clip := range t.Clips {
				if clip.ID == original.ClipID {
					a.selectClip(t, clip)
				}
			}
		}
	}); err != nil {
		return err
	}
	got, err := a.service.GetProject(original.ProjectID)
	if err != nil {
		return err
	}
	sameClip := false
	for _, t := range got.State.Tracks {
		for _, clip := range t.Clips {
			if t.ID == original.TrackID && clip.ID == original.ClipID {
				sameClip = reflect.DeepEqual(clip, original.Clip)
			}
		}
	}
	if got.Revision != original.Revision || !sameClip {
		return fmt.Errorf("原测试工程被生命周期探针改动")
	}
	var historyID string
	if err = a.onMain(func() {
		if len(a.agent.conversations) > 0 {
			historyID = a.agent.conversations[0].ID
			a.selectConversation(historyID)
		}
	}); err != nil {
		return err
	}
	if historyID != "" {
		if err = waitUI(func() bool { return a.agent.selected == historyID && !a.agent.loading && a.agent.err == "" }); err != nil {
			return fmt.Errorf("Native 对话历史：%w", err)
		}
		if err = a.onMain(func() { a.smokeAgentHistory = true }); err != nil {
			return err
		}
	}
	return nil
}
