package desktop

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func defaultZone(asset project.AudioAsset, key int, drum bool) project.SampleZone {
	lo, hi := max(0, key-24), min(127, key+24)
	if drum {
		lo, hi = key, key
	}
	return project.SampleZone{AssetID: asset.ID, Name: asset.Name, Frames: asset.Frames, Root: key, Low: lo, High: hi,
		MinVelocity: 1, MaxVelocity: 127, Attack: 2, Sustain: 100, Release: 12, Gain: 100, OneShot: drum}
}

func (a *Workspace) openSampler() {
	id := a.defaultSampleTrack()
	for _, track := range a.active.State.Tracks {
		if track.ID == id {
			a.openTrackSampler(track)
			return
		}
	}
}
func (a *Workspace) openTrackSampler(track project.Track) {
	a.beginProperties("sampler", track, project.Clip{})
	if f := a.form; f != nil && f.kind == "sampler" && f.values.Sampler == nil {
		f.values.Sampler = track.Sampler.Copy()
		if f.values.Sampler == nil {
			f.values.Sampler = &project.Sampler{}
		}
	}
}

func numeric(c *ui.Context, label string, value, lo, hi int64, change func(int64)) {
	n := float64(value)
	ui.Row(c.Key(label)).Gap(5).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(102)
		ui.NumberInput(c, &n, float64(lo), float64(hi), 1).Label(label).Grow(1).OnChange(func() { change(int64(n)) })
	})
}

func (a *Workspace) samplerFields(c *ui.Context, f *propertyForm) {
	s := f.values.Sampler
	if s == nil {
		return
	}
	state := &a.active.State
	changed := func() { a.changedForm(f) }
	ui.Text(c, "区域按音高和力度匹配，重叠区域可叠加。鼓垫单次播放，其他区域随音符时长释放。").MaxWidth(415).FontSize(11)
	assetChoices := make([]mixChoice, 0, len(state.AudioAssets))
	for _, asset := range state.AudioAssets {
		assetChoices = append(assetChoices, mixChoice{asset.ID, fmt.Sprintf("%s · %.2fs", asset.Name, float64(asset.Frames)/48000)})
	}
	if len(assetChoices) == 0 {
		ui.Text(c, "先从素材库导入，或把音频放入工程。").FontSize(11)
	}
	ui.Row(c).Gap(5).Children(func() {
		mixSelect(c, "采样源", &f.sampleAsset, assetChoices)
		ui.Button(c, "新增区域").Disabled(f.sampleAsset == "" || len(s.Zones) >= project.MaxSamplerZones).OnClick(func() {
			if asset, ok := project.FindAudioAsset(state, f.sampleAsset); ok {
				s.Zones = append(s.Zones, defaultZone(asset, 60, false))
				f.sampleIndex = len(s.Zones) - 1
				changed()
			}
		})
	})
	ui.Text(c, "鼓垫 · MIDI 36–51").Bold()
	for row := 0; row < 4; row++ {
		ui.Row(c).Gap(5).Children(func() {
			for col := 0; col < 4; col++ {
				key := 36 + row*4 + col
				index := slices.IndexFunc(s.Zones, func(z project.SampleZone) bool { return z.OneShot && z.Low == key && z.High == key })
				name := strconv.Itoa(key)
				if index >= 0 {
					name += " ●"
				}
				ui.Button(c.Key(name), name).Width(73).Disabled(index < 0 && (f.sampleAsset == "" || len(s.Zones) >= project.MaxSamplerZones)).
					Tooltip("点击试听并选中已有鼓垫；空垫使用当前采样源").OnClick(func() {
					if index >= 0 {
						f.sampleIndex = index
						a.previewZone(state, s.Zones[index])
						return
					}
					if asset, ok := project.FindAudioAsset(state, f.sampleAsset); ok {
						s.Zones = append(s.Zones, defaultZone(asset, key, true))
						f.sampleIndex = len(s.Zones) - 1
						changed()
					}
				})
			}
		})
	}
	if len(s.Zones) == 0 {
		return
	}
	f.sampleIndex = max(0, min(f.sampleIndex, len(s.Zones)-1))
	options := make([]mixChoice, 0, len(s.Zones))
	for i, z := range s.Zones {
		options = append(options, mixChoice{strconv.Itoa(i), fmt.Sprintf("%02d · %s · %d–%d", i+1, z.Name, z.Low, z.High)})
	}
	sel := strconv.Itoa(f.sampleIndex)
	if mixSelect(c, "当前区域", &sel, options) {
		f.sampleIndex, _ = strconv.Atoi(sel)
	}
	z := &s.Zones[f.sampleIndex]
	ui.Row(c).Gap(5).Children(func() {
		ui.Button(c, "预听源窗口").OnClick(func() { a.previewZone(state, *z) })
		ui.Button(c, "删除区域").OnClick(func() { s.Zones = slices.Delete(s.Zones, f.sampleIndex, f.sampleIndex+1); changed() })
	})
	ui.Scroll(c).MaxHeight(232).FillWidth().Children(func() {
		ui.Column(c).Gap(5).FillWidth().Children(func() {
			ui.TextInput(c, &z.Name).Label("区域名称").OnChange(changed)
			asset, ok := project.FindAudioAsset(state, z.AssetID)
			if ok {
				ui.Text(c, "源 · "+asset.Name).SingleLine().FontSize(11)
				numeric(c, "源起点帧", z.Source, 0, asset.Frames-1, func(v int64) { z.Source = v; z.Frames = min(z.Frames, asset.Frames-v); changed() })
				numeric(c, "窗口帧数", z.Frames, 1, asset.Frames-z.Source, func(v int64) { z.Frames = v; changed() })
			}
			for _, field := range []struct {
				label  string
				value  *int
				lo, hi int64
			}{
				{"根音", &z.Root, 0, 127}, {"最低音", &z.Low, 0, 127}, {"最高音", &z.High, 0, 127},
				{"最低力度", &z.MinVelocity, 1, 127}, {"最高力度", &z.MaxVelocity, 1, 127},
				{"起音 ms", &z.Attack, 0, 5000}, {"衰减 ms", &z.Decay, 0, 5000}, {"持续 %", &z.Sustain, 0, 100},
				{"释放 ms", &z.Release, 0, 5000}, {"增益 %", &z.Gain, 0, 200},
			} {
				numeric(c, field.label, int64(*field.value), field.lo, field.hi, func(v int64) { *field.value = int(v); changed() })
			}
			ui.Checkbox(c, &z.OneShot, "单次触发").OnChange(changed)
		})
	})
}

func (a *Workspace) previewZone(state *project.State, z project.SampleZone) {
	if asset, ok := project.FindAudioAsset(state, z.AssetID); ok {
		a.materialPreview(func(ctx context.Context, v float64) error {
			return a.service.PreviewAudioAsset(ctx, asset, z.Source, z.Frames, v)
		})
	}
}
