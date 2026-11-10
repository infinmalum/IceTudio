package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/audioasset"
	"icetudio/internal/project"
)

type materialShelf struct {
	open, linked, favorite, busy, volumeReady   bool
	items                                       []audioasset.Material
	query, tempo, selected, tags, bpm, key, err string
	meta                                        audioasset.Material
	dirty                                       bool
	page, previewSerial                         int
	volume                                      float64
	previewCancel                               context.CancelFunc
	refs                                        map[string]*ui.Handle
}
type materialDrag struct {
	target    EditTarget
	id, track string
	tick      int64
	linked    bool
}

func (a *Workspace) stopMaterialPreview() {
	m := &a.materialShelf
	m.previewSerial++
	if m.previewCancel != nil {
		m.previewCancel()
		m.previewCancel = nil
	}
	if a.service != nil {
		a.service.StopMaterialPreview()
	}
}

func (a *Workspace) materialPreview(run func(context.Context, float64) error) {
	a.stopMaterialPreview()
	m := &a.materialShelf
	ctx, cancel := context.WithCancel(context.Background())
	m.previewCancel = cancel
	if !m.volumeReady {
		m.volume, m.volumeReady = .65, true
	}
	serial, volume := m.previewSerial, m.volume
	m.err = ""
	go func() {
		err := run(ctx, volume)
		a.update(func() {
			if serial == m.previewSerial && err != nil && !errors.Is(err, context.Canceled) {
				m.err = err.Error()
				a.message = m.err
			}
		})
	}()
}

func (a *Workspace) loadMaterials(scan bool) {
	m := &a.materialShelf
	if a.service == nil || m.busy {
		return
	}
	m.busy = true
	go func() {
		var err error
		if scan {
			var dirs []string
			dirs, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "索引本地 WAV / FLAC 素材目录", Directory: true})
			if err == nil && len(dirs) > 0 {
				_, err = a.service.ScanMaterials(context.Background(), dirs[0])
			}
		}
		var items []audioasset.Material
		if err == nil {
			items, err = a.service.Materials()
		}
		a.update(func() {
			m.busy = false
			if err != nil {
				m.err = err.Error()
				return
			}
			m.items, m.err = items, ""
		})
	}()
}

func (a *Workspace) chooseMaterial(item audioasset.Material) {
	m := &a.materialShelf
	if item.ID != m.selected {
		if m.dirty {
			m.err = "请先保存或丢弃当前素材标签修改"
			return
		}
		a.stopMaterialPreview()
		m.selected, m.meta, m.tags, m.key = item.ID, item, item.Tags, item.Key
		m.bpm = ""
		if item.BPM > 0 {
			m.bpm = strconv.Itoa(item.BPM)
		}
		m.dirty = false
	}
}
func (a *Workspace) updateMaterialMeta(analyze bool) {
	m := &a.materialShelf
	if m.selected == "" || m.busy {
		return
	}
	item := m.meta
	if !analyze {
		bpm := 0
		if strings.TrimSpace(m.bpm) != "" {
			var err error
			bpm, err = strconv.Atoi(m.bpm)
			if err != nil {
				m.err = "BPM 须为整数"
				return
			}
		}
		item.EstimatedBPM = item.EstimatedBPM && item.BPM == bpm
		item.EstimatedKey = item.EstimatedKey && item.Key == m.key
		item.Tags, item.Key, item.BPM = m.tags, m.key, bpm
	}
	m.busy = true
	go func() {
		var err error
		if analyze {
			var result audioasset.Analysis
			result, err = a.service.AnalyzeMaterial(context.Background(), item.ID)
			if err == nil {
				if item.BPM == 0 || item.EstimatedBPM {
					item.BPM, item.EstimatedBPM = result.BPM, result.BPM > 0
				}
				if item.Key == "" || item.EstimatedKey {
					item.Key, item.EstimatedKey = result.Key, result.Key != ""
				}
			}
		}
		if err == nil {
			err = a.service.UpdateMaterial(item)
		}
		a.update(func() {
			m.busy = false
			if err != nil {
				m.err = err.Error()
				return
			}
			for i, v := range m.items {
				if v.ID == item.ID {
					m.items[i] = item
					break
				}
			}
			if m.selected == item.ID {
				m.selected, m.dirty = "", false
				a.chooseMaterial(item)
			}
			m.err = ""
		})
	}()
}

func (a *Workspace) materialBrowser(c *ui.Context) {
	m := &a.materialShelf
	ui.Button(c, "本地素材").FillWidth().Disabled(a.service == nil || a.closeFrozen).OnClick(func() {
		m.open = !m.open
		if m.open {
			if !m.volumeReady {
				m.volume, m.volumeReady = .65, true
			}
			a.loadMaterials(false)
		} else {
			a.stopMaterialPreview()
		}
	})
	if !m.open {
		return
	}
	ui.Scroll(c).MaxHeight(326).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() { a.materialShelfFields(c, m) })
	})
}
func (a *Workspace) materialShelfFields(c *ui.Context, m *materialShelf) {
	ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
		ui.Button(c, "索引目录").Disabled(m.busy).OnClick(func() { a.loadMaterials(true) })
		ui.Checkbox(c, &m.favorite, "收藏").OnChange(func() { m.page = 0 })
	})
	ui.TextInput(c, &m.query).Placeholder("搜索文件名、标签或调性").Label("搜索本地素材").FillWidth().OnChange(func() { m.page = 0 })
	ui.TextInput(c, &m.tempo).Placeholder("BPM（±5）").Label("按速度筛选").FillWidth().OnChange(func() { m.page = 0 })
	tempo, _ := strconv.Atoi(m.tempo)
	query := strings.ToLower(strings.TrimSpace(m.query))
	var filtered []audioasset.Material
	for _, item := range m.items {
		text := strings.ToLower(filepath.Base(item.Path) + " " + item.Tags + " " + item.Key)
		if (!m.favorite || item.Favorite) && (tempo <= 0 || item.BPM > 0 && item.BPM >= tempo-5 && item.BPM <= tempo+5) && strings.Contains(text, query) {
			filtered = append(filtered, item)
		}
	}
	const pageSize = 40
	m.page = max(0, min(m.page, max(0, (len(filtered)-1)/pageSize)))
	first, last := m.page*pageSize, min(len(filtered), (m.page+1)*pageSize)
	ui.Scroll(c).MaxHeight(140).Children(func() {
		for _, item := range filtered[first:last] {
			a.materialRow(c, item)
		}
	})
	ui.Row(c).Gap(4).Children(func() {
		ui.Button(c, "‹").Disabled(m.page == 0).OnClick(func() { m.page-- })
		ui.Textf(c, "%d 项", len(filtered)).Grow(1)
		ui.Button(c, "›").Disabled(last == len(filtered)).OnClick(func() { m.page++ })
	})
	if m.selected != "" {
		ui.Text(c, filepath.Base(m.meta.Path)).SingleLine().Bold()
		ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
			ui.Button(c, "预听").OnClick(func() {
				id := m.selected
				a.materialPreview(func(ctx context.Context, vol float64) error { return a.service.PreviewMaterial(ctx, id, vol) })
			})
			ui.Button(c, "停止").OnClick(a.stopMaterialPreview)
		})
		ui.Slider(c, &m.volume, 0, 1).Label("下次预听音量").FillWidth()
		ui.Checkbox(c, &m.linked, "引用原文件").Tooltip("关闭时复制到工程资产库。")
		ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
			ui.Button(c, "放入编排").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(func() {
				a.placeMaterial(EditTarget{a.active.ID, a.active.Revision}, m.selected, a.defaultSampleTrack(), a.playPosition, m.linked, false)
			})
			ui.Button(c, "建采样轨").Disabled(!a.projectAvailable() || a.active.ID == "").OnClick(func() {
				a.placeMaterial(EditTarget{a.active.ID, a.active.Revision}, m.selected, "", a.playPosition, m.linked, true)
			})
		})
		ui.TextInput(c, &m.tags).Placeholder("标签，逗号分隔").Label("素材标签").Disabled(m.busy).OnChange(func() { m.dirty = true })
		ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
			ui.TextInput(c, &m.bpm).Placeholder("BPM").Label("素材 BPM").MinWidth(64).Grow(1).Disabled(m.busy).OnChange(func() { m.dirty = true })
			ui.TextInput(c, &m.key).Placeholder("调性").Label("素材调性").MinWidth(64).Grow(1).Disabled(m.busy).OnChange(func() { m.dirty = true })
		})
		if m.meta.EstimatedBPM || m.meta.EstimatedKey {
			ui.Text(c, "算法推断 · 可修改").FontSize(10).TextColor(c.Theme().TextMuted)
		}
		ui.Checkbox(c, &m.meta.Favorite, "加入收藏").Disabled(m.busy).OnChange(func() { m.dirty = true })
		ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
			ui.Button(c, "保存标签").Disabled(m.busy || !m.dirty).OnClick(func() { a.updateMaterialMeta(false) })
			if m.dirty {
				ui.Button(c, "丢弃修改").Disabled(m.busy).OnClick(func() {
					for _, item := range m.items {
						if item.ID == m.selected {
							m.selected, m.dirty = "", false
							a.chooseMaterial(item)
							break
						}
					}
				})
			}
			ui.Button(c, "分析 BPM / 调性").Disabled(m.busy || m.dirty).OnClick(func() { a.updateMaterialMeta(true) })
		})
	}
	if m.busy {
		ui.Text(c, "正在处理素材…").FontSize(10)
	}
	if m.err != "" {
		ui.Text(c, m.err).MaxWidth(240).FontSize(10).TextColor(c.Theme().Warning)
	}
}

func (a *Workspace) materialRow(c *ui.Context, item audioasset.Material) {
	m := &a.materialShelf
	row := ui.Button(c.Key(item.ID), filepath.Base(item.Path)).FillWidth().Label("选择素材 " + filepath.Base(item.Path)).Tooltip(item.Path)
	if item.ID == m.selected {
		row.Background(c.Theme().SurfaceHover)
	}
	row.OnClick(func() { a.chooseMaterial(item) })
	if a.browserDrawer {
		return
	}
	if m.refs == nil {
		m.refs = make(map[string]*ui.Handle)
	}
	ref := m.refs[item.ID]
	if ref == nil {
		ref = &ui.Handle{}
		m.refs[item.ID] = ref
	}
	row.Bind(ref)
	row.HandleInput(func(ev ui.InputEvent) bool {
		v := &a.arrangementUI
		switch ev.Kind {
		case ui.InputPointerDown:
			_, height := c.Size()
			if ev.Button != 0 || a.browserDrawer || !a.projectAvailable() || a.active.ID == "" ||
				!a.arrangementVisible(height) {
				return false
			}
			a.chooseMaterial(item)
			if m.selected != item.ID {
				return true
			}
			v.materialDrag = &materialDrag{target: EditTarget{a.active.ID, a.active.Revision}, id: item.ID, linked: m.linked}
			a.arrangementActions().Freeze(true)
			return true
		case ui.InputKeyDown:
			if ev.Key == ui.KeyEscape && v.materialDrag != nil {
				a.cancelArrangement()
				return true
			}
		case ui.InputPointerMove, ui.InputPointerUp:
			d := v.materialDrag
			if d == nil || d.id != item.ID {
				return false
			}
			if ev.Canceled {
				a.cancelArrangement()
				return true
			}
			bounds := ref.Bounds(c)
			x, y := bounds.X+ev.X, bounds.Y+ev.Y
			d.track = ""
			for _, track := range a.active.State.Tracks {
				ref := v.trackDrops[track.ID]
				if ref == nil {
					continue
				}
				b := ref.Bounds(c)
				if b.W > 0 && b.H > 0 && x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
					d.track = track.ID
					snap := editorSnap(v.snap, v.zoom, a.active.PPQ)
					d.tick = patternDropTick(v.start, x-b.X, a.active.PPQ, v.zoom, snap)
					break
				}
			}
			if ev.Kind == ui.InputPointerUp {
				a.cancelArrangement()
				if d.track != "" && ref.Focused(c) {
					a.placeMaterial(d.target, d.id, d.track, d.tick, d.linked, false)
				}
			}
			c.Invalidate()
			return true
		}
		return false
	})
}

func (a *Workspace) defaultSampleTrack() string {
	if t := a.mixerSelection(); slices.ContainsFunc(a.active.State.Tracks, func(track project.Track) bool { return track.ID == t }) {
		return t
	}
	if len(a.active.State.Tracks) > 0 {
		return a.active.State.Tracks[0].ID
	}
	return ""
}
func (a *Workspace) placeMaterial(target EditTarget, id, track string, tick int64, linked, sampler bool) {
	if target.ProjectID != a.active.ID || target.Revision != a.active.Revision {
		a.message = "工程已变化，请重新放置素材"
		return
	}
	ppq := a.active.PPQ
	var ops []project.Op
	var newClip string
	a.stopMaterialPreview()
	a.fileOperation(func() (string, error) {
		asset, err := a.service.PrepareMaterialAudio(context.Background(), id, linked)
		if err != nil {
			return "", err
		}
		if track == "" {
			track = project.NewID("t")
			ops = append(ops, project.Op{Kind: "track.add", Track: &project.TrackSpec{ID: track, Name: asset.Name}})
		}
		ops = append(ops, project.Op{Kind: "audio.asset", AudioAsset: &asset})
		if sampler {
			newClip = project.NewID("c")
			ops = append(ops, project.Op{Kind: "sampler.set", TrackID: track, Sampler: &project.Sampler{Zones: []project.SampleZone{defaultZone(asset, 60, false)}}},
				project.Op{Kind: "clip.add", Clip: &project.ClipSpec{ID: newClip, TrackID: track, Name: asset.Name, Start: tick, Length: int64(ppq) * 4}})
		} else {
			ops = append(ops, project.Op{Kind: "audio.add", TrackID: track, AudioClip: &project.AudioClip{ID: project.NewID("ac"), Name: asset.Name, AssetID: asset.ID, Start: tick, Frames: asset.Frames, Gain: 100}})
		}
		return asset.Name, nil
	}, func(name string) {
		if newClip != "" {
			a.selected = newClip
		}
		a.mutateAt(target, "edit", ops)
		a.message = fmt.Sprintf("已准备素材 %s", name)
	})
}

func (a *Workspace) locateProjectAudio() {
	target := EditTarget{a.active.ID, a.active.Revision}
	assets := append([]project.AudioAsset(nil), a.active.State.AudioAssets...)
	if len(assets) == 0 {
		return
	}
	a.locateAudio = &locateAudioForm{open: true, target: target, assets: assets}
}

type locateAudioForm struct {
	open     bool
	target   EditTarget
	assets   []project.AudioAsset
	selected string
}

func (a *Workspace) locateAudioDialog(c *ui.Context) {
	f := a.locateAudio
	if f == nil || !f.open || a.closeFrozen {
		return
	}
	ui.Modal(c, &f.open, func() {
		ui.Text(c, "定位音频引用").FontSize(18).Bold()
		ui.Text(c, "选择新路径，IceTudio 会比对解码后的音频哈希。").MaxWidth(410)
		options := make([]mixChoice, 0, len(f.assets))
		for _, asset := range f.assets {
			options = append(options, mixChoice{asset.ID, fmt.Sprintf("%s · %.2f 秒 · %s", asset.Name, float64(asset.Frames)/48000, asset.Digest[:min(8, len(asset.Digest))])})
		}
		mixSelect(c, "工程音频资产", &f.selected, options)
		ui.PrimaryButton(c, "选择原音频").Disabled(f.selected == "" || !a.projectAvailable() || f.target.ProjectID != a.active.ID || f.target.Revision != a.active.Revision).OnClick(func() {
			asset := f.selected
			a.fileOperation(func() (string, error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "定位原音频", Filters: []mygo.FileFilter{{Name: "WAV / FLAC", Extensions: []string{"wav", "flac"}}}})
				if err != nil || len(paths) == 0 {
					return "", err
				}
				if err = a.service.RelinkAudioAsset(context.Background(), f.target.ProjectID, f.target.Revision, asset, paths[0]); err != nil {
					return "", err
				}
				return paths[0], nil
			}, func(path string) {
				if path != "" {
					f.open = false
					a.message = "原音频引用已更新"
				}
			})
		})
		ui.Button(c, "关闭").OnClick(func() { f.open = false })
	})
}
