package desktop

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/project"
)

type compInputs struct {
	Track string
	Parts []app.CompPart
}

func (a *Workspace) openComp() {
	a.beginProperties("comp", project.Track{}, project.Clip{})
	if f := a.form; f != nil && f.kind == "comp" && f.values.Comp == nil {
		f.values.Comp = &compInputs{Track: a.defaultSampleTrack()}
		f.compPosition = musicTime(f.meter, a.playPosition)
		a.loadCompTakes(f)
	}
}
func (a *Workspace) loadCompTakes(f *propertyForm) {
	if f.preparing {
		return
	}
	f.preparing = true
	go func() {
		takes, err := a.service.AudioTakes(f.entry.Target.ID)
		a.update(func() {
			f.preparing = false
			if a.form != f {
				return
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			f.compTakes = takes
		})
	}()
}
func (a *Workspace) compFields(c *ui.Context, f *propertyForm) {
	v := f.values.Comp
	if v == nil {
		return
	}
	ui.Text(c, "从原 take 选取帧区间，在目标轨上组装。提交整组可一次撤销。").MaxWidth(408).FontSize(11)
	ui.Button(c, "刷新音频 take").Disabled(f.preparing).OnClick(func() { a.loadCompTakes(f) })
	tracks := make([]mixChoice, 0, len(a.active.State.Tracks))
	for _, t := range a.active.State.Tracks {
		tracks = append(tracks, mixChoice{t.ID, t.Name})
	}
	if mixSelect(c, "Comp 目标轨", &v.Track, tracks) {
		a.changedForm(f)
	}
	options := make([]mixChoice, 0, len(f.compTakes))
	for _, t := range f.compTakes {
		options = append(options, mixChoice{t.ID, fmt.Sprintf("%s · %.2fs · tick %d", t.ID, float64(t.Frames)/48000, t.Tick)})
	}
	if mixSelect(c, "选择原 take", &f.compTake, options) {
		f.compSource, f.compFrames = 0, 0
	}
	for _, t := range f.compTakes {
		if t.ID != f.compTake {
			continue
		}
		if f.compFrames == 0 {
			f.compFrames = t.Frames
		}
		if t.LoopFrames > 0 {
			ui.Textf(c, "循环窗口 %d 帧，保留最后不足一轮的片段。", t.LoopFrames).FontSize(11)
			numeric(c, "取第几轮", 1+f.compSource/t.LoopFrames, 1, (t.Frames+t.LoopFrames-1)/t.LoopFrames, func(n int64) {
				f.compSource = (n - 1) * t.LoopFrames
				f.compFrames = min(t.LoopFrames, t.Frames-f.compSource)
			})
		}
		numeric(c, "源起点帧", f.compSource, 0, t.Frames-1, func(n int64) { f.compSource = n; f.compFrames = min(f.compFrames, t.Frames-n) })
		numeric(c, "保留帧数", f.compFrames, 1, t.Frames-f.compSource, func(n int64) { f.compFrames = n })
		ui.TextInput(c, &f.compPosition).Label("Comp 放置位置").Placeholder("小节:拍:tick 或 tick")
		ui.Row(c).Gap(5).Children(func() {
			ui.Button(c, "预听选段").OnClick(func() {
				id, take, start, frames := f.entry.Target.ID, f.compTake, f.compSource, f.compFrames
				a.materialPreview(func(ctx context.Context, vol float64) error {
					return a.service.PreviewTake(ctx, id, take, start, frames, vol)
				})
			})
			ui.Button(c, "加入选段").Disabled(len(v.Parts) >= 32).OnClick(func() {
				m, err := newMeterProjection(a.active.PPQ, a.active.State.Meter)
				if err != nil {
					f.err = err.Error()
					return
				}
				tick, err := parseMusicTime(m, f.compPosition)
				if err != nil {
					f.err = err.Error()
					return
				}
				v.Parts = append(v.Parts, app.CompPart{TakeID: t.ID, Source: f.compSource, Frames: f.compFrames, Tick: tick})
				a.changedForm(f)
			})
		})
		break
	}
	ui.Scroll(c).MaxHeight(124).FillWidth().Children(func() {
		for i, part := range v.Parts {
			ui.Row(c.Key(fmt.Sprint(i))).Gap(5).Children(func() {
				ui.Textf(c, "#%d · %d tick · %d + %d 帧", i+1, part.Tick, part.Source, part.Frames).Grow(1)
				ui.Button(c, "移除").OnClick(func() { v.Parts = append(v.Parts[:i], v.Parts[i+1:]...); a.changedForm(f) })
			})
		}
	})
}

func (a *Workspace) applyComp(f *propertyForm) {
	if f.values.Comp == nil || f.preparing || a.fileBusy {
		return
	}
	if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
		f.err = "原工程修订已变化，选段保留供核对"
		return
	}
	v := *f.values.Comp
	v.Parts = append([]app.CompPart(nil), v.Parts...)
	target := EditTarget{f.entry.Target.ID, f.entry.Target.Revision}
	ctx, cancel := context.WithCancel(context.Background())
	f.preparing, f.cancelPrepare, a.fileBusy = true, cancel, true
	go func() {
		defer cancel()
		ops, err := a.service.PrepareComp(ctx, target.ProjectID, target.Revision, v.Track, v.Parts)
		a.update(func() {
			f.preparing, f.cancelPrepare, a.fileBusy = false, nil, false
			if a.form != f {
				return
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			if a.closeFrozen {
				return
			}
			f.applying = true
			a.submitEdit(target, "edit", ops, func() { a.clearForm(f, a.dismissForm) })
		})
	}()
}
