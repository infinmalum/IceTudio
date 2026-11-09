package desktop

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

const clipRowHeight float32 = 46

type clipGesture struct {
	Batch         []string
	BatchStart    int64
	Target        EditTarget
	Track         project.Track
	Clip          project.Clip
	Resize        bool
	X             float32
	Left, Width   float32
	ViewStart     int64
	PointerX      float32
	Zoom, Snap    float64
	PPQ           int
	Start, Length int64
	Error         string
}

type arrangementView struct {
	materialDrag   *materialDrag
	audioDrag      *audioClipDrag
	trackDrops     map[string]*ui.Handle
	patternSources map[string]*ui.Handle
	patternDrag    *patternDrag
	marquee        *arrangementMarquee
	marqueeHandle  ui.Handle
	marked         []string
	start          int64
	width          float32
	playTick       float64
	playing        bool
	follow         string
	followPaused   bool
	position       string
	positionError  string
	rulerTarget    EditTarget
	rulerDown      bool
	rulerTick      int64
	rulerX         float32
	zoom           float64
	snap           string
	locate         string
	gesture        *clipGesture
	handles        map[string]*ui.Handle
}
type arrangementActions struct {
	AutomationOpen func(string, project.Automation, int64)
	AudioOpen      func(project.Track, project.AudioClip)
	Writable       func() bool
	Select         func(project.Track, project.Clip)
	Open           func()
	SnapChanged    func()
	Freeze         func(bool)
	Commit         func(EditTarget, []project.Op)
	Error          func(string)
	Seek           func(int64)
	Loop           func(int64, int64)
	Keyboard       func(ui.InputEvent) bool
	Navigate       func()
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
		v.snap = "1/4"
	}
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
	if len(g.Batch) > 1 && g.BatchStart+g.Start-g.Clip.Start < 0 {
		g.Error = "批量片段不能越过工程起点"
	}
	if g.Start < 0 || g.Start > project.MaxTick || g.Length < 1 || g.Length > project.MaxTick {
		g.Error = "起点或长度超出允许范围，本次拖动不会提交"
	}
}
func (v *arrangementView) cancel(actions arrangementActions) {
	if v.gesture != nil || v.marquee != nil || v.patternDrag != nil || v.materialDrag != nil || v.audioDrag != nil {
		v.audioDrag = nil
		v.patternDrag = nil
		v.materialDrag = nil
		v.marquee = nil
		v.gesture = nil
		actions.Freeze(false)
	}
}
func (v *arrangementView) locateClip(p storage.Project, id string) {
	v.locate = id
	for _, t := range p.State.Tracks {
		for _, clip := range t.Clips {
			if clip.ID == id && p.PPQ > 0 {
				v.start = clip.Start
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
	if v.width <= 0 {
		v.width = 1024
	}
	v.start = max(0, min(project.MaxTick*2, v.start))
	if p.PPQ <= 0 {
		ui.Text(c, "选择工程后显示编排")
		return
	}
	start := v.start
	end := start + int64(math.Ceil(float64(v.width)/v.zoom*float64(p.PPQ)))
	width := v.width
	locked := v.gesture != nil || v.marquee != nil || v.patternDrag != nil || v.materialDrag != nil || v.audioDrag != nil
	ui.Column(c).Fill().AlignItems(ui.Stretch).Gap(4).Children(func() {
		ui.ScrollHorizontal(c).Height(32).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "ARRANGEMENT").FontSize(12).Bold()
				ui.Button(c, "回到开头").Disabled(locked).OnClick(func() {
					v.start = 0
					if actions.Navigate != nil {
						actions.Navigate()
					}
				})
				index, _ := newMeterProjection(p.PPQ, p.State.Meter)
				ui.Text(c, musicTime(index, start)).FontSize(12)

				ui.Button(c, "定位片段").Disabled(locked || selected == "").OnClick(func() { v.locateClip(p, selected) })
				ui.Text(c, "缩放").FontSize(12)
				ui.Slider(c, &v.zoom, 16, 200).Width(90).Disabled(locked).Label("编排缩放").OnChange(func() {
					if actions.SnapChanged != nil {
						actions.SnapChanged()
					}
				})
				ui.Text(c, "吸附").FontSize(12)
				snap := ui.Select(c, &v.snap, snapLabels).Width(64).Disabled(locked).Label("编排吸附")
				if snap.Changed() && actions.SnapChanged != nil {
					actions.SnapChanged()
				}
			})
		})
		ui.Row(c).Height(28).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "全部片段").FontSize(12)
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
			ui.Text(c, "Shift 拖动框选 · 拖动选区可一起移动 · Shift 滚轮连续导航").FontSize(12).TextColor(c.Theme().TextMuted)
		})
		start = v.start
		end = start + int64(math.Ceil(float64(v.width)/v.zoom*float64(p.PPQ)))
		// One vertical scroll tree keeps headers and lanes aligned; only the right
		// group scrolls horizontally; both groups share the same vertical parent.
		outer := ui.Scroll(c).Grow(1).Background(ui.Hex("#191c20"))
		outer.HandleInput(func(ev ui.InputEvent) bool {
			return (v.gesture != nil || v.marquee != nil || v.patternDrag != nil || v.materialDrag != nil || v.audioDrag != nil) && (ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom)
		})
		outer.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Stretch).Children(func() {
				tracks := v.tracks(p)
				timeline, timelineErr := playback.NewTimeline(&p.State)
				assets := make(map[string]project.AudioAsset, len(p.State.AudioAssets))
				for _, asset := range p.State.AudioAssets {
					assets[asset.ID] = asset
				}
				waveform := func(t project.Track) ([]waveClip, int) {
					if timelineErr != nil || len(t.Audio) == 0 {
						return nil, 0
					}
					return waveClips(t, assets, timeline, start, end, v.zoom)
				}
				ui.Column(c).Width(172).Gap(1).Children(func() {
					ui.Text(c, "TRACK").Height(52).Padding(8).FontSize(12).TextColor(c.Theme().TextMuted)
					for _, t := range tracks {
						_, lanes := arrangementClips(t.Clips, start, end, p.PPQ, v.zoom)
						_, waves := waveform(t)
						ui.Column(c.Key(t.ID)).Height(max(64, float32(lanes)*clipRowHeight+8) + float32(waves)*waveHeight + float32(len(trackAutomation(p.State.Mixer, t.ID))*automationRowHeight)).Padding(8).Gap(4).Background(c.Theme().Surface).Children(func() { head(c, t) })
					}
				})
				right := ui.Column(c).Width(1).Grow(1).MinWidth(0).Clip().Focusable()
				right.Bind(&v.marqueeHandle)
				right.DrawOver(func(paint *ui.Painter, r ui.Rect) {
					if m := v.marquee; m != nil {
						x1, x2 := r.X+min(m.x, m.endX), r.X+max(m.x, m.endX)
						y1, y2 := r.Y+min(m.y, m.endY), r.Y+max(m.y, m.endY)
						color := c.Theme().Accent
						paint.Line(x1, y1, x2, y1, 1, color)
						paint.Line(x2, y1, x2, y2, 1, color)
						paint.Line(x2, y2, x1, y2, 1, color)
						paint.Line(x1, y2, x1, y1, 1, color)
					}
				})
				if bounds := right.Bounds(); bounds.W > 0 {
					v.width = bounds.W
					width = v.width
				}
				right.HandleInput(func(ev ui.InputEvent) bool {
					if v.marqueeInput(ev, c, p, tracks, start, end, actions) {
						return true
					}
					return v.navigation(ev, p, actions)
				}).Children(func() {

					ui.Column(c).Width(width).Gap(1).Children(func() {
						v.ruler(c, p, start, end, width, actions)
						for i, t := range tracks {
							clips, lanes := arrangementClips(t.Clips, start, end, p.PPQ, v.zoom)
							waves, waveCount := waveform(t)
							baseHeight := max(64, float32(lanes)*clipRowHeight+8)
							automation := trackAutomation(p.State.Mixer, t.ID)
							audioBottom := baseHeight + float32(waveCount)*waveHeight
							ui.Box(c.Key(t.ID)).Bind(v.trackDrop(t.ID)).Width(width).Height(audioBottom + float32(len(automation)*automationRowHeight)).Clip().Draw(func(paint *ui.Painter, r ui.Rect) {
								for beat := math.Ceil(float64(start) / float64(p.PPQ)); beat <= float64(end)/float64(p.PPQ); beat++ {
									x := r.X + float32((beat-float64(start)/float64(p.PPQ))*v.zoom)
									paint.Line(x, r.Y, x, r.Y+r.H, 1, ui.Hex("#2b3037"))
								}
							}).DrawOver(func(paint *ui.Painter, r ui.Rect) {
								if v.playing {
									x := r.X + float32((v.playTick-float64(start))/float64(p.PPQ)*v.zoom)
									if x >= r.X && x <= r.X+r.W {
										paint.Line(x, r.Y, x, r.Y+r.H, 2, c.Theme().Accent)
									}
								}
							}).Children(func() {
								if g := v.gesture; g != nil && g.Track.ID == t.ID {
									found := false
									for _, item := range clips {
										if item.Clip.ID == g.Clip.ID {
											found = true
										}
									}
									if !found {
										clips = append(clips, arrangedClip{Clip: g.Clip, Width: g.Width, RealEnd: true})
									}
								}
								for _, item := range clips {
									v.clip(c, p, t, item, i, start, selected, actions)
								}
								for _, wave := range waves {
									v.drawWave(c, p, t, wave, baseHeight, actions)
								}
								for ai, lane := range automation {
									v.drawAutomation(c, lane, t.ID, automationLabel(lane, p.State.Mixer.Channel(t.ID)), audioBottom+float32(ai*automationRowHeight), start, end, p.PPQ, actions)
								}
								if d := v.materialDrag; d != nil && d.track == t.ID {
									x := float32(float64(d.tick-start) / float64(p.PPQ) * v.zoom)
									ui.Box(c.Key("material-drop")).Absolute().Left(x).Top(4).Width(4).Height(baseHeight - 8).Background(c.Theme().Accent)
								}
								if d := v.patternDrag; d != nil && d.trackID == t.ID {
									x := float32(float64(d.tick-start) / float64(p.PPQ) * v.zoom)
									w := float32(float64(d.pattern.Body.Length) / float64(p.PPQ) * v.zoom)
									ui.Box(c.Key("pattern-drop")).Absolute().Left(x).Top(5).Width(max(5, w)).Height(36).
										Background(ui.RGBA(111, 185, 240, .24)).Border(1, c.Theme().Accent)
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
			ui.Text(c, msg).FontSize(12).TextColor(c.Theme().Warning)
		}
	})
}
func (v *arrangementView) clip(c *ui.Context, p storage.Project, track project.Track, item arrangedClip, index int, start int64, selected string, actions arrangementActions) {
	clip := item.Clip
	if g := v.gesture; g != nil && g.Clip.ID == clip.ID {
		item.Left = g.Left
		item.Width = g.Width
	}
	label := clip.Name
	if label == "" {
		label = "片段"
	}
	if clip.GroupID != "" {
		label = "▣ " + label
	}
	if clip.PatternID != "" {
		label = "↔ " + label
	}
	color := displayObjectColor(clip.Color, track.Color, index)
	b := ui.Button(c.Key(clip.ID), label).Absolute().Left(item.Left).Top(4 + float32(item.Lane)*clipRowHeight).Width(item.Width).Height(clipRowHeight - 4).Background(color).TextColor(ui.Hex("#15202b")).Label(label)
	if item.RealEnd && item.Width >= 12 {
		b.Draw(func(paint *ui.Painter, r ui.Rect) {
			paint.Line(r.X+r.W-5, r.Y+9, r.X+r.W-5, r.Y+r.H-9, 2, ui.RGBA(20, 32, 43, .5))
		})
	}
	b.Tooltip("拖动移动；右端拉伸；双击打开卷帘；Ctrl/Cmd 点击选入批量片段")
	b.ContextMenu(func(m *ui.Menu) {
		if m.Item("选择片段").Chosen() {
			v.marked = nil
			actions.Select(track, clip)
		}
		if m.Item("打开钢琴卷帘").Chosen() {
			actions.Select(track, clip)
			actions.Open()
		}
		if m.Item("定位到片段起点").Chosen() {
			v.start = clip.Start
			if actions.Seek != nil {
				actions.Seek(clip.Start)
			}
		}
	})
	if selected == clip.ID {
		b.Border(2, c.Theme().Text)
	} else if slices.Contains(v.marked, clip.ID) {
		b.Border(2, c.Theme().Warning)
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
		if actions.Keyboard != nil && v.gesture == nil && actions.Keyboard(ev) {
			return true
		}
		if ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom {
			return v.navigation(ev, p, actions)
		}
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && v.gesture != nil {
			v.cancel(actions)
			return true
		}
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 || v.gesture != nil || v.marquee != nil || ev.Mods&ui.Shift != 0 {
				return false
			}
			if ev.Mods&(ui.Ctrl|ui.Cmd) != 0 {
				if i := slices.Index(v.marked, clip.ID); i >= 0 {
					v.marked = slices.Delete(v.marked, i, i+1)
				} else if len(v.marked) < 63 {
					v.marked = append(v.marked, clip.ID)
				}
				return true
			}
			batch, first := v.dragSelection(p, selected, clip)
			v.marked = batch
			actions.Select(track, clip)
			if ev.Clicks == 2 {
				actions.Open()
				return true
			}
			if !actions.Writable() {
				return false
			}
			snap := editorSnap(v.snap, v.zoom, p.PPQ)
			if ev.Mods&ui.Alt != 0 {
				snap = 1 / float64(p.PPQ)
			}
			v.gesture = &clipGesture{Target: EditTarget{p.ID, p.Revision}, Track: track, Clip: clip, Batch: batch, BatchStart: first, Resize: len(batch) < 2 && item.RealEnd && item.Width >= 12 && ev.X >= item.Width-8, X: ev.X, Left: item.Left, Width: item.Width, ViewStart: v.start, Zoom: v.zoom, Snap: snap, PPQ: p.PPQ, Start: clip.Start, Length: clip.Length}
			actions.Freeze(true)
			return true
		case ui.InputPointerMove:
			if g := v.gesture; g != nil && g.Clip.ID == clip.ID {
				v.moveGesture(ev.X, p)
				return true
			}
		case ui.InputPointerUp:
			g := v.gesture
			if g == nil || g.Clip.ID != clip.ID {
				return false
			}
			if ev.Canceled || !ref.Focused(c) {
				v.cancel(actions)
				return true
			}
			v.moveGesture(ev.X, p)
			v.cancel(actions)
			if g.Error != "" {
				actions.Error(g.Error)
				return true
			}
			if g.Start == g.Clip.Start && g.Length == g.Clip.Length {
				return true
			}
			if len(g.Batch) > 1 {
				actions.Commit(g.Target, []project.Op{{Kind: "clip.arrange", Arrangement: &project.ArrangementEdit{
					Action: "move", IDs: g.Batch, Tick: g.BatchStart + g.Start - g.Clip.Start,
				}}})
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
	if g := v.gesture; g != nil && (g.Clip.ID == clip.ID || slices.Contains(g.Batch, clip.ID)) {
		previewStart, previewLength := clip.Start+g.Start-g.Clip.Start, clip.Length
		if g.Clip.ID == clip.ID {
			previewLength = g.Length
		}
		left := float32(float64(previewStart-start) / float64(g.PPQ) * g.Zoom)
		width := float32(float64(previewLength) / float64(g.PPQ) * g.Zoom)
		ui.Box(c.Key(clip.ID+"-preview")).Absolute().Left(left).Top(4+float32(item.Lane)*clipRowHeight).Width(max(5, width)).Height(clipRowHeight-4).Border(2, c.Theme().Warning).Background(ui.RGBA(240, 190, 90, .28))
	}
}
func (v *arrangementView) isOtherGesture(id string) bool {
	return v.gesture != nil && v.gesture.Clip.ID != id
}

// Snap is shared by Native and the WebView; musical labels are quarter-note units.
func arrangementSnap(value string) float64 { return editorSnap(value, 64, 480) }
