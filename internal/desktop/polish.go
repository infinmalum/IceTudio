package desktop

import (
	"context"
	"strconv"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/audioasset"
	"icetudio/internal/project"
)

type polishInputs struct {
	Percent, Cents, Target string
	Correct                bool
	Ops                    []project.Op
	Asset                  *project.AudioAsset
}

func (a *Workspace) openPolish(track project.Track, clip project.AudioClip) {
	a.beginProperties("audio_polish", track, project.Clip{ID: clip.ID})
	if f := a.form; f != nil && f.kind == "audio_polish" && f.values.Polish == nil {
		f.values.Polish = &polishInputs{Percent: "100", Cents: "0"}
	}
}

func (a *Workspace) polishFields(c *ui.Context, f *propertyForm) {
	v := f.values.Polish
	if v == nil {
		return
	}
	changed := func() { v.Asset = nil; v.Ops = nil; a.changedForm(f) }
	ui.Text(c, "拉伸保持音高。校正检测稳定单声部基频并整体移调。先生成并预听候选，再应用。").MaxWidth(408).FontSize(11)
	ui.TextInput(c, &v.Percent).Disabled(f.preparing).Label("拉伸长度百分比").Placeholder("长度 50–200%").OnChange(changed)
	ui.Checkbox(c, &v.Correct, "校正稳定单声部").Disabled(f.preparing).OnChange(changed)
	if v.Correct {
		ui.TextInput(c, &v.Target).Disabled(f.preparing).Label("目标 MIDI 音高").Placeholder("留空取最近半音，或输入 MIDI 0–127").OnChange(changed)
	} else {
		ui.TextInput(c, &v.Cents).Disabled(f.preparing).Label("手动移调音分").Placeholder("−1200 … 1200").OnChange(changed)
	}
	ui.Row(c).Gap(5).Children(func() {
		ui.Button(c, "生成精修候选").Disabled(f.preparing || a.fileBusy).OnClick(func() { a.preparePolish(f, false) })
		ui.Button(c, "生成瞬态切片").Disabled(f.preparing || a.fileBusy).OnClick(func() { a.preparePolish(f, true) })
	})
	if v.Asset != nil {
		asset := *v.Asset
		ui.Textf(c, "候选 · %.2f 秒", float64(asset.Frames)/48000).FontSize(11)
		ui.Button(c, "预听候选").OnClick(func() {
			a.materialPreview(func(ctx context.Context, vol float64) error {
				return a.service.PreviewAudioAsset(ctx, asset, 0, asset.Frames, vol)
			})
		})
	} else if len(v.Ops) > 0 {
		ui.Textf(c, "候选 · %d 个切片 · 共享原音频", len(v.Ops)).FontSize(11)
	}
	if f.preparing && f.cancelPrepare != nil {
		ui.Button(c, "取消后台处理").OnClick(f.cancelPrepare)
	}
}

func (a *Workspace) preparePolish(f *propertyForm, slicesOnly bool) {
	v := f.values.Polish
	if v == nil || f.preparing || a.fileBusy || f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
		return
	}
	spec := audioasset.Polish{Target: -1, Correct: v.Correct}
	if !slicesOnly {
		var err error
		spec.Percent, err = strconv.Atoi(v.Percent)
		if err != nil {
			f.err = "长度须为整数百分比"
			return
		}
		if v.Correct && v.Target != "" {
			spec.Target, err = strconv.Atoi(v.Target)
		} else if !v.Correct {
			spec.Cents, err = strconv.Atoi(v.Cents)
		}
		if err != nil {
			f.err = "目标音或移调须为整数"
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.preparing, f.cancelPrepare, a.fileBusy = true, cancel, true
	f.err = ""
	entry := f.entry
	go func() {
		defer cancel()
		var ops []project.Op
		var asset project.AudioAsset
		var err error
		if slicesOnly {
			ops, err = a.service.PrepareAudioSlices(ctx, entry.Target.ID, entry.Target.Revision, entry.TrackID, entry.ClipID)
		} else {
			ops, asset, err = a.service.PrepareAudioPolish(ctx, entry.Target.ID, entry.Target.Revision, entry.TrackID, entry.ClipID, spec)
		}
		a.update(func() {
			f.preparing, f.cancelPrepare, a.fileBusy = false, nil, false
			if a.form != f {
				return
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			v.Ops, v.Asset = ops, nil
			if !slicesOnly {
				v.Asset = &asset
			}
			a.changedForm(f)
		})
	}()
}

func (a *Workspace) currentAudioForForm(f *propertyForm) (project.Track, project.AudioClip, bool) {
	if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
		return project.Track{}, project.AudioClip{}, false
	}
	for _, t := range a.active.State.Tracks {
		if t.ID != f.entry.TrackID {
			continue
		}
		for _, clip := range t.Audio {
			if clip.ID == f.entry.ClipID {
				return t, clip, true
			}
		}
	}
	return project.Track{}, project.AudioClip{}, false
}

func (a *Workspace) audioExtraFields(c *ui.Context, f *propertyForm) {
	track, clip, ok := a.currentAudioForForm(f)
	if !ok {
		return
	}
	ui.Row(c).Gap(5).Children(func() {
		ui.Button(c, "切片 / 拉伸 / 校正").Disabled(f.dirty || f.writes > 0).OnClick(func() { a.dismissForm(); a.openPolish(track, clip) })
		ui.Button(c, "映射源窗口").Disabled(f.dirty || f.writes > 0 || track.Sampler != nil && len(track.Sampler.Zones) >= project.MaxSamplerZones).OnClick(func() {
			asset, ok := project.FindAudioAsset(&a.active.State, clip.AssetID)
			if !ok {
				return
			}
			a.dismissForm()
			a.openTrackSampler(track)
			if form := a.form; form != nil && form.kind == "sampler" {
				zone := defaultZone(asset, 60, false)
				zone.Source, zone.Frames, zone.Name = clip.Source, clip.Frames, clip.Name
				form.values.Sampler.Zones = append(form.values.Sampler.Zones, zone)
				form.sampleIndex = len(form.values.Sampler.Zones) - 1
				a.changedForm(form)
			}
		})
	})
}
