package desktop

import (
	"fmt"
	"math"
	"sort"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

const arrangementBeats int64 = 16
const clipRowHeight float32 = 46

type clipGesture struct {
	Target        EditTarget
	Track         project.Track
	Clip          project.Clip
	Resize        bool
	X             float32
	Zoom, Snap    float64
	PPQ           int
	Start, Length int64
	Error         string
}

type arrangementView struct {
	page    int64
	zoom    float64
	snap    string
	locate  string
	gesture *clipGesture
	handles map[string]*ui.Handle
}
type arrangementActions struct {
	Writable    func() bool
	Select      func(project.Track, project.Clip)
	Open        func()
	SnapChanged func()
	Freeze      func(bool)
	Commit      func(EditTarget, []project.Op)
	Error       func(string)
}
type arrangedClip struct {
	Clip        project.Clip
	Lane        int
	Left, Width float32
	RealEnd     bool
}

func (v *arrangementView) defaults() {
	if v.zoom == 0 {
		v.zoom = 64
	}
	if v.snap == "" {
		v.snap = "1"
	}
}
func arrangementPages(p storage.Project) int64 {
	end := int64(1)
	for _, t := range p.State.Tracks {
		for _, c := range t.Clips {
			end = max(end, c.Start+c.Length)
		}
	}
	if p.PPQ <= 0 {
		return 1
	}
	size := int64(p.PPQ) * arrangementBeats
	return max(1, (end+size-1)/size)
}
func arrangementClips(clips []project.Clip, start, end int64, ppq int, zoom float64) ([]arrangedClip, int) {
	ordered := make([]project.Clip, 0, len(clips))
	for _, c := range clips {
		if c.Start < end && c.Start+c.Length > start {
			ordered = append(ordered, c)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.Start+a.Length != b.Start+b.Length {
			return a.Start+a.Length < b.Start+b.Length
		}
		return a.ID < b.ID
	})
	ends := []int64{}
	out := make([]arrangedClip, 0, len(ordered))
	for _, c := range ordered {
		lane := 0
		for lane < len(ends) && ends[lane] > c.Start {
			lane++
		}
		if lane == len(ends) {
			ends = append(ends, 0)
		}
		ends[lane] = c.Start + c.Length
		left := float32(float64(max(start, c.Start)-start) / float64(ppq) * zoom)
		width := float32(float64(min(end, c.Start+c.Length)-max(start, c.Start)) / float64(ppq) * zoom)
		out = append(out, arrangedClip{c, lane, left, max(5, width), c.Start+c.Length <= end})
	}
	return out, max(1, len(ends))
}
func (g *clipGesture) move(x float32) {
	steps := float64(x-g.X) / g.Zoom / g.Snap
	delta := int64(math.Copysign(math.Round(math.Abs(math.Round(steps))*g.Snap*float64(g.PPQ)), steps))
	g.Start, g.Length = g.Clip.Start, g.Clip.Length
	if g.Resize {
		g.Length += delta
	} else {
		g.Start += delta
	}
	g.Error = ""
	if g.Start < 0 || g.Start > project.MaxTick || g.Length < 1 || g.Length > project.MaxTick {
		g.Error = "起点或长度超出允许范围，本次拖动不会提交"
	}
}
func (v *arrangementView) cancel(actions arrangementActions) {
	if v.gesture != nil {
		v.gesture = nil
		actions.Freeze(false)
	}
}
func (v *arrangementView) locateClip(p storage.Project, id string) {
	v.locate = id
	for _, t := range p.State.Tracks {
		for _, clip := range t.Clips {
			if clip.ID == id && p.PPQ > 0 {
				v.page = clip.Start / (arrangementBeats * int64(p.PPQ))
				return
			}
		}
	}
}
func (v *arrangementView) tracks(p storage.Project) []project.Track {
	g := v.gesture
	if g == nil {
		return p.State.Tracks
	}
	tracks := append([]project.Track(nil), p.State.Tracks...)
	found := false
	for i, t := range tracks {
		if t.ID == g.Track.ID {
			found = true
			clips := append([]project.Clip(nil), t.Clips...)
			hasClip := false
			for j, c := range clips {
				if c.ID == g.Clip.ID {
					clips[j] = g.Clip
					hasClip = true
				}
			}
			if !hasClip {
				clips = append(clips, g.Clip)
			}
			tracks[i].Clips = clips
		}
	}
	if !found {
		t := g.Track
		t.Clips = []project.Clip{g.Clip}
		tracks = append(tracks, t)
	}
	return tracks
}
func (v *arrangementView) draw(c *ui.Context, p storage.Project, selected string, actions arrangementActions, head func(*ui.Context, project.Track)) {
	v.defaults()
	pages := arrangementPages(p)
	if v.gesture == nil {
		v.page = min(v.page, pages-1)
	}
	if p.PPQ <= 0 {
		ui.Text(c, "选择工程后显示编排")
		return
	}
	start := v.page * arrangementBeats * int64(p.PPQ)
	end := start + arrangementBeats*int64(p.PPQ)
	width := float32(v.zoom * float64(arrangementBeats))
	locked := v.gesture != nil
	ui.Column(c).Fill().AlignItems(ui.Stretch).Gap(4).Children(func() {
		ui.ScrollHorizontal(c).Height(32).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "ARRANGEMENT").FontSize(11).Bold()
				ui.Button(c, "上一页编排").Disabled(locked || v.page == 0).OnClick(func() { v.page-- })
				ui.Textf(c, "%d/%d 页", v.page+1, pages).FontSize(11)
				ui.Button(c, "下一页编排").Disabled(locked || v.page >= pages-1).OnClick(func() { v.page++ })
				ui.Button(c, "定位片段").Disabled(locked || selected == "").OnClick(func() { v.locateClip(p, selected) })
				ui.Text(c, "缩放").FontSize(11)
				ui.Slider(c, &v.zoom, 16, 200).Width(90).Disabled(locked).Label("编排缩放")
				ui.Text(c, "吸附").FontSize(11)
				snap := ui.Select(c, &v.snap, []string{"1/4", "1/2", "1", "2", "4"}).Width(64).Disabled(locked).Label("编排吸附")
				if snap.Changed() && actions.SnapChanged != nil {
					actions.SnapChanged()
				}
			})
		})
		ui.Row(c).Height(28).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "全部片段").FontSize(11)
			parts := ui.SelectBase[string](c, &v.locate)
			parts.Trigger.Width(260).Disabled(locked).Label("全部编排片段")
			chosen := "选择片段 ▾"
			for _, t := range p.State.Tracks {
				for _, clip := range t.Clips {
					if clip.ID == v.locate {
						chosen = t.Name + " · " + clip.Name + " ▾"
					}
				}
			}
			parts.Trigger.Padding(4, 8).Background(c.Theme().Surface).Border(1, c.Theme().Border).Children(func() { ui.Text(c, chosen).SingleLine() })
			parts.Popup(func(panel ui.Element) {
				panel.Padding(4).Background(c.Theme().Surface)
				ui.Scroll(c).Width(360).MaxHeight(320).Children(func() {
					for _, t := range p.State.Tracks {
						for _, clip := range t.Clips {
							item := parts.Item(clip.ID).Padding(5, 8)
							if item.Highlighted() {
								item.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
							}
							item.Children(func() { ui.Text(c, t.Name+" · "+clip.Name).SingleLine() })
						}
					}
				})
			})
			if parts.Trigger.Changed() && v.locate != "" && !locked {
				for _, t := range p.State.Tracks {
					for _, clip := range t.Clips {
						if clip.ID == v.locate {
							actions.Select(t, clip)
							v.locateClip(p, clip.ID)
							c.Invalidate()
						}
					}
				}
			}
			ui.Text(c, "拖动移动 · 右端拉伸 · 双击打开卷帘 · Esc 取消").FontSize(10).TextColor(c.Theme().TextMuted)
		})
		start = v.page * arrangementBeats * int64(p.PPQ)
		end = start + arrangementBeats*int64(p.PPQ)
		// One vertical scroll tree keeps headers and lanes aligned; only the right
		// group scrolls horizontally; both groups share the same vertical parent.
		outer := ui.Scroll(c).Grow(1).Background(ui.Hex("#191c20"))
		outer.HandleInput(func(ev ui.InputEvent) bool {
			return v.gesture != nil && (ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom)
		})
		outer.Children(func() {
			ui.Row(c).AlignItems(ui.Stretch).Children(func() {
				tracks := v.tracks(p)
				ui.Column(c).Width(172).Gap(1).Children(func() {
					ui.Text(c, "TRACK").Height(52).Padding(8).FontSize(10).TextColor(c.Theme().TextMuted)
					for _, t := range tracks {
						_, lanes := arrangementClips(t.Clips, start, end, p.PPQ, v.zoom)
						ui.Column(c.Key(t.ID)).Height(max(64, float32(lanes)*clipRowHeight+8)).Padding(8).Gap(4).Background(c.Theme().Surface).Children(func() { head(c, t) })
					}
				})
				ui.ScrollHorizontal(c).Width(min(width, float32(3200))).Shrink(1).MinWidth(0).HandleInput(func(ev ui.InputEvent) bool {
					return v.gesture != nil && (ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom)
				}).Children(func() {
					ui.Column(c).Width(width).Gap(1).Children(func() {
						drawArrangementRuler(c, p, start, end, v.zoom)
						for i, t := range tracks {
							clips, lanes := arrangementClips(t.Clips, start, end, p.PPQ, v.zoom)
							ui.Box(c.Key(t.ID)).Width(width).Height(max(64, float32(lanes)*clipRowHeight+8)).Clip().Draw(func(paint *ui.Painter, r ui.Rect) {
								for beat := 0; beat <= 16; beat++ {
									x := r.X + float32(float64(beat)*v.zoom)
									paint.Line(x, r.Y, x, r.Y+r.H, 1, ui.Hex("#2b3037"))
								}
							}).Children(func() {
								for _, item := range clips {
									v.clip(c, p, t, item, i, start, selected, actions)
								}
							})
						}
					})
				})
			})
		})
		if g := v.gesture; g != nil {
			msg := fmt.Sprintf("预览 · 起点 %d · 长度 %d tick", g.Start, g.Length)
			if g.Error != "" {
				msg = g.Error
			}
			ui.Text(c, msg).FontSize(11).TextColor(c.Theme().Warning)
		}
	})
}
func (v *arrangementView) clip(c *ui.Context, p storage.Project, track project.Track, item arrangedClip, index int, start int64, selected string, actions arrangementActions) {
	clip := item.Clip
	label := clip.Name
	if label == "" {
		label = "片段"
	}
	color := displayObjectColor(clip.Color, track.Color, index)
	b := ui.Button(c.Key(clip.ID), label).Absolute().Left(item.Left).Top(4 + float32(item.Lane)*clipRowHeight).Width(item.Width).Height(clipRowHeight - 4).Background(color).TextColor(ui.Hex("#15202b")).Label(label)
	if item.RealEnd && item.Width >= 12 {
		b.Draw(func(paint *ui.Painter, r ui.Rect) {
			paint.Line(r.X+r.W-5, r.Y+9, r.X+r.W-5, r.Y+r.H-9, 2, ui.RGBA(20, 32, 43, .5))
		})
	}
	b.Tooltip("拖动移动；右端竖线拉伸；双击打开卷帘")
	if selected == clip.ID {
		b.Border(2, c.Theme().Text)
	}
	if v.handles == nil {
		v.handles = map[string]*ui.Handle{}
	}
	ref := v.handles[clip.ID]
	if ref == nil {
		ref = &ui.Handle{}
		v.handles[clip.ID] = ref
	}
	b.Bind(ref)
	b.OnClick(func() {
		if !v.isOtherGesture(clip.ID) {
			actions.Select(track, clip)
		}
	})
	// Keep the original hit box fixed through the gesture: local coordinates
	// must not drift as the preview moves. The ghost is painted separately.
	b.HandleInput(func(ev ui.InputEvent) bool {
		if v.gesture != nil && (ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom) {
			return true
		}
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && v.gesture != nil {
			v.cancel(actions)
			return true
		}
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 || v.gesture != nil {
				return false
			}
			actions.Select(track, clip)
			if ev.Clicks == 2 {
				actions.Open()
				return true
			}
			if !actions.Writable() {
				return false
			}
			snap := map[string]float64{"1/4": .25, "1/2": .5, "1": 1, "2": 2, "4": 4}[v.snap]
			v.gesture = &clipGesture{Target: EditTarget{p.ID, p.Revision}, Track: track, Clip: clip, Resize: item.RealEnd && item.Width >= 12 && ev.X >= item.Width-8, X: ev.X, Zoom: v.zoom, Snap: snap, PPQ: p.PPQ, Start: clip.Start, Length: clip.Length}
			actions.Freeze(true)
			return true
		case ui.InputPointerMove:
			if g := v.gesture; g != nil && g.Clip.ID == clip.ID {
				g.move(ev.X)
				return true
			}
		case ui.InputPointerUp:
			g := v.gesture
			if g == nil || g.Clip.ID != clip.ID {
				return false
			}
			if !ref.Focused(c) {
				v.cancel(actions)
				return true
			} // includes MyGo's synthetic blur release
			g.move(ev.X)
			v.cancel(actions)
			if g.Error != "" {
				actions.Error(g.Error)
				return true
			}
			if g.Start == g.Clip.Start && g.Length == g.Clip.Length {
				return true
			}
			patch := project.ClipPatch{}
			if g.Resize {
				patch.Length = &g.Length
			} else {
				patch.Start = &g.Start
			}
			actions.Commit(g.Target, []project.Op{{Kind: "clip.update", TrackID: g.Track.ID, ClipID: g.Clip.ID, ClipPatch: &patch}})
			return true
		}
		return false
	})
	if g := v.gesture; g != nil && g.Clip.ID == clip.ID {
		left := float32(float64(g.Start-start) / float64(g.PPQ) * g.Zoom)
		width := float32(float64(g.Length) / float64(g.PPQ) * g.Zoom)
		ui.Box(c.Key(clip.ID+"-preview")).Absolute().Left(left).Top(4+float32(item.Lane)*clipRowHeight).Width(max(5, width)).Height(clipRowHeight-4).Border(2, c.Theme().Warning).Background(ui.RGBA(240, 190, 90, .28))
	}
}
func (v *arrangementView) isOtherGesture(id string) bool {
	return v.gesture != nil && v.gesture.Clip.ID != id
}

func arrangementSnap(value string) float64 {
	snap := map[string]float64{"1/4": .25, "1/2": .5, "1": 1, "2": 2, "4": 4}[value]
	if snap == 0 {
		return 1
	}
	return snap
}
