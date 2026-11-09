package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func (a *Workspace) projectAvailable() bool {
	if a.edits == nil || a.editBusy || a.fileBusy || a.closeFrozen || a.form != nil || a.mapForm != nil || a.arrangementUI.gesture != nil || a.arrangementUI.marquee != nil || a.arrangementUI.patternDrag != nil || a.arrangementUI.materialDrag != nil || a.arrangementUI.audioDrag != nil {
		return false
	}
	s := a.edits.State()
	return s.Ready && !s.Busy && s.Pending == nil
}
func (a *Workspace) projectTools(c *ui.Context) {
	ui.Row(c).Gap(4).Children(func() {
		ui.Button(c, "新建").Disabled(!a.projectAvailable()).OnClick(func() { a.beginProperties("create", project.Track{}, project.Clip{}) })
		ui.Button(c, "导入 MIDI").Disabled(!a.projectAvailable()).OnClick(a.importMIDI)
	})
	ui.Row(c).Gap(4).Children(func() {
		ui.Button(c, "打开资源包").Disabled(!a.projectAvailable()).OnClick(a.importAudioBundle)
		ui.Button(c, "保存资源包").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(a.exportAudioBundle)
	})
	if a.active.ID != "" {
		if len(a.active.State.Tracks) > 0 {
			ui.Button(c, "采样器 / 鼓垫").Disabled(!a.projectAvailable()).OnClick(a.openSampler)
		}
		if len(a.active.State.AudioAssets) > 0 {
			ui.Button(c, "定位音频引用").Disabled(!a.projectAvailable()).OnClick(a.locateProjectAudio)
		}
		ui.Row(c).Gap(4).Children(func() {
			ui.Button(c, "重命名").Disabled(!a.projectAvailable()).OnClick(func() { a.beginProperties("rename", project.Track{}, project.Clip{}) })
			ui.Button(c, "导出音频").Disabled(!a.projectAvailable()).OnClick(func() {
				a.audioExport = &audioExportForm{open: true, projectID: a.active.ID, revision: a.active.Revision, format: "wav", rate: "48000", start: "0", bits: "16", tail: "250"}
			})
			ui.Button(c, "导出 MIDI").Tooltip("标准 MIDI 导出音乐事件；音频请用导出音频或保存资源包").Disabled(!a.projectAvailable()).OnClick(a.exportMIDI)
		})
	}
	if a.active.ID != "" {
		ui.Button(c, "删除当前工程").FillWidth().Disabled(!a.projectAvailable()).OnClick(a.deleteProject)
	}
	ui.Button(c, fmt.Sprintf("未应用输入 (%d)", len(a.savedInputs))).Disabled(a.service == nil || a.closeFrozen).OnClick(func() { a.showDrafts = true; a.reloadDrafts() })
}
func (a *Workspace) fileOperation(run func() (string, error), done func(string)) {
	if !a.projectAvailable() {
		return
	}
	a.fileBusy = true
	a.preview.freeze(true)
	a.notifyPiano()
	go func() {
		value, err := run()
		a.update(func() {
			a.fileBusy = false
			a.preview.freeze(a.closeFrozen || a.form != nil || a.mapForm != nil)
			a.notifyPiano()
			if err != nil {
				a.message = err.Error()
				return
			}
			if done != nil {
				done(value)
			}
		})
	}()
}
func (a *Workspace) importMIDI() {
	a.fileOperation(func() (string, error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "导入 MIDI", Filters: []mygo.FileFilter{{Name: "Standard MIDI", Extensions: []string{"mid", "midi"}}}})
		if err != nil || len(paths) == 0 {
			return "", err
		}
		p, err := a.service.ImportProjectMIDI(paths[0])
		return p.ID, err
	}, func(id string) {
		if id != "" {
			a.open(id)
			a.reloadProjectList()
			a.message = "已导入 MIDI"
		}
	})
}
func safeMIDIName(title string) string {
	title = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, title)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "project"
	}
	return title + ".mid"
}
func (a *Workspace) exportMIDI() {
	p := a.active
	a.fileOperation(func() (string, error) {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "导出 MIDI", DefaultPath: safeMIDIName(p.Title), Filters: []mygo.FileFilter{{Name: "Standard MIDI", Extensions: []string{"mid"}}}})
		if err != nil || path == "" {
			return "", err
		}
		err = a.service.ExportProjectMIDI(p.ID, path)
		return path, err
	}, func(path string) {
		if path != "" {
			a.message = "已导出 " + filepath.Base(path)
		}
	})
}
func (a *Workspace) deleteProject() {
	p := a.active
	a.fileOperation(func() (string, error) {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: "删除工程", Message: "删除「" + p.Title + "」及其编辑历史？", Detail: "此操作不能撤销。对话保留；尚未应用的草稿保留，不能恢复到已删除工程。", Buttons: []string{"保留", "删除工程"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
		if err != nil || res.Button != 1 {
			return "", err
		}
		err = a.service.DeleteProject(p.ID)
		return p.ID, err
	}, func(id string) {
		if id != "" {
			a.message = "工程已删除"
			a.refresh()
		}
	})
}
func (a *Workspace) createFromForm(f *propertyForm) {
	if strings.TrimSpace(f.values.Name) == "" {
		f.err = "请输入工程名称"
		return
	}
	title := f.values.Name
	f.applying = true
	a.editBusy = true
	go func() {
		p, err := a.service.CreateProject(title, 480)
		a.update(func() {
			a.editBusy = false
			if err != nil {
				f.applying = false
				f.err = err.Error()
				return
			}
			a.install(p)
			a.reloadProjectList()
			a.message = "工程已创建；编辑自动保存"
			a.clearForm(f, a.dismissForm)
		})
	}()
}
func (a *Workspace) reloadProjectList() {
	a.listSequence++
	request := a.listSequence
	go func() {
		items, err := a.service.ListProjects()
		a.update(func() {
			if request != a.listSequence {
				return
			}
			if err != nil {
				a.message = err.Error()
			} else {
				a.projects = items
			}
		})
	}()
}
func (a *Workspace) addTrack() {
	if !a.projectAvailable() || a.active.ID == "" {
		return
	}
	tid, cid := project.NewID("t"), project.NewID("c")
	ops := []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: tid, Name: fmt.Sprintf("音轨 %d", len(a.active.State.Tracks)+1), Channel: len(a.active.State.Tracks) % 16}}, {Kind: "clip.add", TrackID: tid, Clip: &project.ClipSpec{ID: cid, TrackID: tid, Name: "片段 1", Length: int64(a.active.PPQ) * 16}}}
	a.selected = cid
	a.mutate("edit", ops)
}
func (a *Workspace) addClip(track project.Track) {
	if !a.projectAvailable() {
		return
	}
	start := int64(0)
	for _, clip := range track.Clips {
		if clip.Start+clip.Length > start {
			start = clip.Start + clip.Length
		}
	}
	id := project.NewID("c")
	a.selected = id
	a.mutate("edit", []project.Op{{Kind: "clip.add", TrackID: track.ID, Clip: &project.ClipSpec{ID: id, TrackID: track.ID, Name: fmt.Sprintf("片段 %d", len(track.Clips)+1), Start: start, Length: int64(a.active.PPQ) * 16}}})
}
func (a *Workspace) removeObject(track project.Track, clip *project.Clip) {
	if !a.projectAvailable() {
		return
	}
	target := EditTarget{a.active.ID, a.active.Revision}
	name := track.Name
	op := project.Op{Kind: "track.remove", TrackID: track.ID}
	kind := "轨道"
	detail := "可以用共享撤销恢复。确认后仍按原修订检查冲突。"
	if clip == nil && a.active.State.Mixer != nil {
		detail = "关联的混音设置、路由和自动化也会移除。可以用共享撤销恢复。"
	}
	if clip != nil {
		name = clip.Name
		kind = "片段"
		op = project.Op{Kind: "clip.remove", TrackID: track.ID, ClipID: clip.ID}
	}
	a.fileBusy = true
	a.preview.freeze(true)
	a.notifyPiano()
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: "删除" + kind, Message: "删除「" + name + "」及其全部事件？", Detail: detail, Buttons: []string{"保留", "删除"}, CancelButton: 0, DestructiveButtons: []int{1}})
		a.update(func() {
			a.fileBusy = false
			a.preview.freeze(a.closeFrozen)
			a.notifyPiano()
			if err != nil {
				a.message = err.Error()
				return
			}
			if res.Button == 1 {
				a.mutateAt(target, "edit", []project.Op{op})
			}
		})
	}()
}
func (a *Workspace) objectTools(c *ui.Context) {
	ui.ScrollHorizontal(c).Height(30).Children(func() {
		ui.Row(c).Gap(4).Children(func() {
			a.timeMapTools(c)
			ui.Button(c, "添加音轨").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(a.addTrack)
			ui.Button(c, "导入音频").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(func() {
				track, _ := project.ClipTrack(&a.active.State, a.selected)
				if track == "" && len(a.active.State.Tracks) > 0 {
					track = a.active.State.Tracks[0].ID
				}
				a.importAudio(track)
			})
			ui.Button(c, "Pattern 库").Disabled(!a.projectAvailable() || len(a.active.State.Patterns) == 0).OnClick(a.openPatternLibrary)
			for _, t := range a.active.State.Tracks {
				for _, clip := range t.Clips {
					if clip.ID != a.selected {
						continue
					}
					for _, tool := range []struct{ label, kind string }{
						{"批量片段", "midi_arrange"}, {"控制事件", "midi_control"}, {"鼓组", "midi_steps"}, {"和弦", "midi_chords"}, {"段落", "midi_marker"},
					} {
						ui.Button(c, tool.label).Disabled(!a.writable()).OnClick(func() { a.openMIDIEditor(tool.kind, t, clip) })
					}
					ui.Button(c, "轨道属性").Disabled(!a.projectAvailable()).OnClick(func() { a.beginProperties("track", t, clip) })
					ui.Button(c, "片段属性").Disabled(!a.projectAvailable()).OnClick(func() { a.beginProperties("clip", t, clip) })
					ui.Button(c, "添加片段").Disabled(!a.projectAvailable()).OnClick(func() { a.addClip(t) })
					ui.Button(c, "删除片段").Disabled(!a.projectAvailable()).OnClick(func() { a.removeObject(t, &clip) })
					ui.Button(c, "删除轨道").Disabled(!a.projectAvailable()).OnClick(func() { a.removeObject(t, nil) })
				}
			}
		})
	})
}
func (a *Workspace) draftsDialog(c *ui.Context) {
	if !a.showDrafts || a.closeFrozen {
		return
	}
	ui.Modal(c, &a.showDrafts, func() {
		ui.Text(c, "未应用的音乐输入").FontSize(18).Bold()
		ui.Text(c, "恢复不自动提交，保留原目标和修订。工程删除后草稿仍保留。").MaxWidth(560)
		if a.draftError != "" {
			ui.Text(c, a.draftError).TextColor(c.Theme().Warning)
		}
		ui.Scroll(c).Width(560).MaxHeight(420).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				for _, e := range a.savedInputs {
					ui.Column(c.Key(e.Key)).Gap(4).Children(func() {
						ui.Textf(c, "%s · %s · 修订 %d", e.Title, e.Label, e.Target.Revision)
						if nativeDraft(e) {
							ui.Row(c).Gap(6).Children(func() {
								ui.Button(c, "恢复输入").Disabled(a.form != nil || !a.projectAvailable()).OnClick(func() {
									kind, v, err := restoreMusicDraft(e)
									if err != nil {
										a.message = err.Error()
										return
									}
									a.form = &propertyForm{open: true, kind: kind, entry: e, values: v, dirty: true}
									a.showDrafts = false
									a.preview.freeze(true)
									a.notifyPiano()
									if e.Target.ID != "new_project" {
										a.open(e.Target.ID)
									}
								})
								ui.Button(c, "丢弃这项输入").Disabled(a.editBusy || a.form != nil).OnClick(func() { a.discardDraft(e) })
							})
						} else {
							ui.Text(c, "原编辑器输入已保留，卷帘输入可在对应片段显式恢复；此处仅保留并展示状态。").FontSize(11)
						}
					})
				}
			})
		})
		ui.Button(c, "返回").OnClick(func() { a.showDrafts = false })
	})
}
func (a *Workspace) discardDraft(e musicDraft) {
	if !nativeDraft(e) {
		return
	}
	a.editBusy = true
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Title: "丢弃未应用输入", Message: "丢弃「" + e.Label + "」的输入？工程和历史不变。", Buttons: []string{"保留", "丢弃"}, CancelButton: 0, DestructiveButtons: []int{1}})
		if err == nil && res.Button == 1 {
			err = a.drafts.put(e, true)
		}
		a.update(func() {
			a.editBusy = false
			if err != nil {
				a.message = err.Error()
			}
			a.reloadDrafts()
		})
	}()
}
