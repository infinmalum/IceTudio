package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (a *Workspace) importAudio(trackID string) {
	target := EditTarget{a.active.ID, a.active.Revision}
	position := a.playPosition
	var ops []project.Op
	a.fileOperation(func() (string, error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "导入音频", Filters: []mygo.FileFilter{{Name: "WAV / FLAC", Extensions: []string{"wav", "flac"}}}})
		if err != nil || len(paths) == 0 {
			return "", err
		}
		asset, err := a.service.PrepareAudioAsset(context.Background(), paths[0])
		if err != nil {
			return "", err
		}
		if trackID == "" {
			trackID = project.NewID("t")
			ops = append(ops, project.Op{Kind: "track.add", Track: &project.TrackSpec{ID: trackID, Name: "音频"}})
		}
		ops = append(ops, project.Op{Kind: "audio.asset", AudioAsset: &asset},
			project.Op{Kind: "audio.add", TrackID: trackID, AudioClip: &project.AudioClip{ID: project.NewID("ac"), AssetID: asset.ID, Name: asset.Name, Start: position, Frames: asset.Frames, Gain: 100}})
		return asset.Name, nil
	}, func(name string) {
		if name != "" {
			a.mutateAt(target, "edit", ops)
		}
	})
}

type audioFormValues struct {
	Name, Start, Source, Frames, FadeIn, FadeOut, Gain string
	Reverse, Remove, Split                             bool
	SplitAt                                            string
}

func (a *Workspace) openAudioClip(track project.Track, clip project.AudioClip) {
	if !a.projectAvailable() {
		return
	}
	a.beginProperties("audio_clip", track, project.Clip{ID: clip.ID})
	if a.form == nil || a.form.kind != "audio_clip" {
		return
	}
	s := func(v int64) string { return strconv.FormatInt(v, 10) }
	a.form.values.Audio = &audioFormValues{Name: clip.Name, Start: musicTime(a.form.meter, clip.Start),
		Source: s(clip.Source), Frames: s(clip.Frames), FadeIn: s(clip.FadeIn), FadeOut: s(clip.FadeOut), Gain: strconv.Itoa(clip.Gain), Reverse: clip.Reverse}
}

func audioFormOps(f *propertyForm, st *project.State) ([]project.Op, error) {
	v := f.values.Audio
	if v == nil {
		return nil, fmt.Errorf("音频输入不存在")
	}
	meter, err := newMeterProjection(st.PPQ, st.Meter)
	if err != nil {
		return nil, err
	}
	for _, track := range st.Tracks {
		if track.ID != f.entry.TrackID {
			continue
		}
		for _, clip := range track.Audio {
			if clip.ID != f.entry.ClipID {
				continue
			}
			if v.Remove {
				return []project.Op{{Kind: "audio.remove", TrackID: track.ID, ClipID: clip.ID}}, nil
			}
			if v.Split {
				tick, err := parseMusicTime(meter, v.SplitAt)
				if err != nil {
					return nil, err
				}
				timeline, err := playback.NewTimeline(st)
				if err != nil {
					return nil, err
				}
				offset := timeline.Frame(float64(tick)) - timeline.Frame(float64(clip.Start))
				if offset <= 0 || offset >= clip.Frames {
					return nil, fmt.Errorf("拆分位置须位于片段内")
				}
				right := clip
				right.ID, right.Start, right.Frames = project.NewID("ac"), tick, clip.Frames-offset
				if clip.Reverse {
					clip.Source += right.Frames
				} else {
					right.Source += offset
				}
				clip.Frames = offset
				clip.FadeIn, clip.FadeOut = min(clip.FadeIn, clip.Frames), 0
				right.FadeIn, right.FadeOut = 0, min(right.FadeOut, right.Frames)
				return []project.Op{{Kind: "audio.update", TrackID: track.ID, AudioClip: &clip}, {Kind: "audio.add", TrackID: track.ID, AudioClip: &right}}, nil
			}
			clip.Start, err = parseMusicTime(meter, v.Start)
			if err != nil {
				return nil, err
			}
			clip.Name, clip.Reverse = v.Name, v.Reverse
			for _, field := range []struct {
				raw  string
				dest *int64
			}{{v.Source, &clip.Source}, {v.Frames, &clip.Frames}, {v.FadeIn, &clip.FadeIn}, {v.FadeOut, &clip.FadeOut}} {
				*field.dest, err = strconv.ParseInt(field.raw, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("源窗口和淡变必须为整数采样帧")
				}
			}
			clip.Gain, err = strconv.Atoi(v.Gain)
			if err != nil {
				return nil, fmt.Errorf("增益须为整数百分比")
			}
			return []project.Op{{Kind: "audio.update", TrackID: track.ID, AudioClip: &clip}}, nil
		}
	}
	return nil, fmt.Errorf("原音频片段不存在")
}

func (a *Workspace) audioClipFields(c *ui.Context, f *propertyForm, field func(string, *string)) {
	v := f.values.Audio
	if v == nil {
		return
	}
	changed := func() { a.changedForm(f) }
	ui.Checkbox(c, &v.Remove, "应用时删除片段").OnChange(changed)
	if v.Remove {
		return
	}
	ui.Checkbox(c, &v.Split, "在指定音乐位置拆分").OnChange(changed)
	if v.Split {
		field("拆分位置", &v.SplitAt)
		return
	}
	field("名称", &v.Name)
	field("起点", &v.Start)
	field("源起点帧", &v.Source)
	field("窗口帧数", &v.Frames)
	field("淡入帧数", &v.FadeIn)
	field("淡出帧数", &v.FadeOut)
	field("增益 %", &v.Gain)
	ui.Checkbox(c, &v.Reverse, "反向").OnChange(changed)
	ui.Text(c, "48 kHz 源窗口。48000 帧为一秒。").FontSize(12)
}

type audioExportForm struct {
	rate                                    string
	open                                    bool
	projectID                               string
	revision                                int64
	format, trackID, start, end, tail, bits string
	normalize, dither                       bool
	cancel                                  context.CancelFunc
	err                                     string
}

func (a *Workspace) audioExportDialog(c *ui.Context) {
	f := a.audioExport
	if f == nil || !f.open || a.closeFrozen {
		return
	}
	width, height := c.Size()
	formWidth := min(360, width-64)
	ui.Modal(c, &f.open, func() {
		ui.Text(c, "导出音频").Bold()
		ui.Scroll(c).Width(formWidth).MaxHeight(height - 145).Children(func() {
			ui.Column(c).FillWidth().Gap(6).Disabled(f.cancel != nil).Children(func() {
				ui.Select(c, &f.format, []string{"wav", "flac"}).Label("格式")
				ui.Select(c, &f.rate, []string{"44100", "48000", "96000"}).Label("采样率")
				ui.Select(c, &f.bits, []string{"16", "24"}).Label("位深")
				ui.TextInput(c, &f.start).Label("起点").Placeholder("起点 · 小节:拍:tick")
				ui.TextInput(c, &f.end).Label("终点").Placeholder("终点 · 留空为工程末尾")
				ui.TextInput(c, &f.tail).Label("尾音毫秒").Placeholder("尾音毫秒")
				audioTrackSelect(c, a.active.State.Tracks, &f.trackID, "主混音")
				ui.Checkbox(c, &f.normalize, "归一化")
				ui.Checkbox(c, &f.dither, "抖动")
			})
			if f.err != "" {
				ui.Text(c, f.err).TextColor(c.Theme().Warning)
			}
		})
		ui.Row(c).Width(formWidth).Wrap().Gap(6).Children(func() {
			if f.cancel != nil {
				ui.Button(c, "取消渲染").OnClick(f.cancel)
				return
			}
			ui.Button(c, "关闭").OnClick(func() { f.open = false })
			ui.PrimaryButton(c, "选择文件并导出").Disabled(!a.projectAvailable()).OnClick(func() { a.runAudioExport(f) })
		})
	})
}
func audioTrackSelect(c *ui.Context, tracks []project.Track, chosen *string, none string) {
	p := ui.SelectBase(c, chosen)
	name := none
	for _, t := range tracks {
		if t.ID == *chosen {
			name = t.Name
			break
		}
	}
	p.Trigger.Label("音轨").Padding(6, 8).Background(c.Theme().SurfaceHover).Children(func() { ui.Text(c, name+" ▾") })
	p.Popup(func(panel ui.Element) {
		panel.Background(c.Theme().Surface)
		ui.Scroll(c).MaxHeight(160).Children(func() {
			if none != "" {
				p.Item("").Padding(6, 8).Children(func() { ui.Text(c, none) })
			}
			for _, t := range tracks {
				p.Item(t.ID).Padding(6, 8).Children(func() { ui.Text(c, t.Name) })
			}
		})
	})
}
func (a *Workspace) runAudioExport(f *audioExportForm) {
	if a.active.ID != f.projectID || a.active.Revision != f.revision {
		f.err = "工程修订变化，请重新打开导出"
		return
	}
	m, err := newMeterProjection(a.active.PPQ, a.active.State.Meter)
	if err != nil {
		f.err = err.Error()
		return
	}
	start, err := parseMusicTime(m, f.start)
	if err != nil {
		f.err = err.Error()
		return
	}
	end := int64(0)
	if f.end != "" {
		end, err = parseMusicTime(m, f.end)
		if err != nil {
			f.err = err.Error()
			return
		}
	}
	tail, err := strconv.Atoi(f.tail)
	if err != nil {
		f.err = "尾音须为整数毫秒"
		return
	}
	bits, _ := strconv.Atoi(f.bits)
	rate, _ := strconv.Atoi(f.rate)
	req := playback.ExportRequest{ProjectID: f.projectID, Revision: f.revision, TrackID: f.trackID, Start: start, End: end, TailMS: tail, Bits: bits, Rate: rate, Normalize: f.normalize, Dither: f.dither}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	a.fileBusy = true
	a.preview.freeze(true)
	a.notifyPiano()
	format, title := f.format, a.active.Title
	go func() {
		defer cancel()
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "导出音频", DefaultPath: title + "." + format, Filters: []mygo.FileFilter{{Name: format, Extensions: []string{format}}}})
		if err == nil && path != "" {
			if filepath.Ext(path) == "" {
				path += "." + format
			}
			err = a.service.ExportProjectAudio(ctx, req, path)
		}
		a.update(func() {
			f.cancel = nil
			a.fileBusy = false
			a.preview.freeze(a.closeFrozen)
			a.notifyPiano()
			if err != nil {
				f.err = err.Error()
			} else if path != "" {
				f.open = false
				a.message = "已导出 " + filepath.Base(path)
			}
		})
	}()
}

func (a *Workspace) importAudioBundle() {
	a.fileOperation(func() (string, error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "打开工程资源包", Filters: []mygo.FileFilter{{Name: "IceTudio 资源包", Extensions: []string{"icezip"}}}})
		if err != nil || len(paths) == 0 {
			return "", err
		}
		p, err := a.service.ImportAudioBundle(context.Background(), paths[0])
		return p.ID, err
	}, func(id string) {
		if id != "" {
			a.open(id)
			a.reloadProjectList()
		}
	})
}
func (a *Workspace) exportAudioBundle() {
	p := a.active
	a.fileOperation(func() (string, error) {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "保存工程资源包", DefaultPath: p.Title + ".icezip", Filters: []mygo.FileFilter{{Name: "IceTudio 资源包", Extensions: []string{"icezip"}}}})
		if err != nil || path == "" {
			return "", err
		}
		if filepath.Ext(path) == "" {
			path += ".icezip"
		}
		return path, a.service.ExportAudioBundle(context.Background(), p.ID, p.Revision, path)
	}, func(path string) {
		if path != "" {
			a.message = "已保存资源包 " + filepath.Base(path)
		}
	})
}
