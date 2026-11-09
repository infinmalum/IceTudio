package desktop

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func (a *Workspace) openPatternLibrary() {
	a.beginProperties("midi_pattern", project.Track{}, project.Clip{})
	if a.form == nil || a.form.kind != "midi_pattern" {
		return
	}
	v := &midiInputs{Count: "1", First: musicTime(a.form.meter, a.arrangementUI.start)}
	if len(a.active.State.Patterns) > 0 {
		v.PatternID = a.active.State.Patterns[0].ID
	}
	if len(a.active.State.Tracks) > 0 {
		v.Destination = a.active.State.Tracks[0].ID
	}
	a.form.values.MIDI = v
}

func (a *Workspace) patternLibraryFields(c *ui.Context, f *propertyForm, field func(string, *string)) {
	v := f.values.MIDI
	choose := func(label string, selected *string, ids, names []string) {
		p := ui.SelectBase(c, selected)
		title := label
		for i, id := range ids {
			if id == *selected {
				title = names[i]
				break
			}
		}
		p.Trigger.Label(label).Padding(5, 8).Background(c.Theme().SurfaceHover).Children(func() { ui.Text(c, title+" ▾") })
		p.Popup(func(panel ui.Element) {
			panel.Background(c.Theme().Surface)
			ui.Scroll(c).MaxHeight(160).Children(func() {
				for i, id := range ids {
					p.Item(id).Padding(5, 8).Children(func() { ui.Text(c, names[i]) })
				}
			})
		})
		if p.Trigger.Changed() {
			a.changedForm(f)
		}
	}
	var ids, names []string
	for _, p := range a.active.State.Patterns {
		ids = append(ids, p.ID)
		names = append(names, fmt.Sprintf("%s · %d 音符 · %d tick", p.Name, len(p.Body.Notes), p.Body.Length))
	}
	choose("Pattern", &v.PatternID, ids, names)
	ui.Checkbox(c, &v.Remove, "删除未使用的资源").OnChange(func() { a.changedForm(f) })
	if v.Remove {
		ui.Text(c, "仍有编排实例的资源会保留。").FontSize(12)
		return
	}
	ids, names = nil, nil
	for _, t := range a.active.State.Tracks {
		ids = append(ids, t.ID)
		names = append(names, t.Name)
	}
	choose("目标轨", &v.Destination, ids, names)
	field("放置位置", &v.First)
	field("铺排次数", &v.Count)
}

type patternDrag struct {
	target  EditTarget
	pattern project.Pattern
	trackID string
	tick    int64
}

func (v *arrangementView) trackDrop(id string) *ui.Handle {
	if v.trackDrops == nil {
		v.trackDrops = map[string]*ui.Handle{}
	}
	if v.trackDrops[id] == nil {
		v.trackDrops[id] = &ui.Handle{}
	}
	return v.trackDrops[id]
}

func (a *Workspace) patternBrowser(c *ui.Context) {
	if len(a.active.State.Patterns) == 0 {
		return
	}
	ui.Text(c, "PATTERNS").FontSize(12)
	ui.Scroll(c).MaxHeight(108).Children(func() {
		for _, p := range a.active.State.Patterns {
			if a.arrangementUI.patternSources == nil {
				a.arrangementUI.patternSources = map[string]*ui.Handle{}
			}
			ref := a.arrangementUI.patternSources[p.ID]
			if ref == nil {
				ref = &ui.Handle{}
				a.arrangementUI.patternSources[p.ID] = ref
			}
			row := ui.Button(c.Key(p.ID), p.Name).Label("放置 Pattern " + p.Name).FillWidth().
				Tooltip("拖至编排轨道，或使用 Pattern 库指定目标")
			row.Bind(ref)
			open := func() {
				if !a.projectAvailable() {
					return
				}
				a.openPatternLibrary()
				if a.form != nil && a.form.kind == "midi_pattern" {
					a.form.values.MIDI.PatternID = p.ID
				}
			}
			row.OnClick(open)
			row.HandleInput(func(ev ui.InputEvent) bool {
				v := &a.arrangementUI
				switch ev.Kind {
				case ui.InputPointerDown:
					if ev.Button != 0 || !a.projectAvailable() || a.browserDrawer {
						return false
					}
					_, h := c.Size()
					if a.preferences.View == "piano" || h < 640 && a.preferences.View != "arrangement" {
						return false
					}
					v.patternDrag = &patternDrag{target: EditTarget{a.active.ID, a.active.Revision}, pattern: p}
					a.arrangementActions().Freeze(true)
					return true
				case ui.InputKeyDown:
					if ev.Key == ui.KeyEscape && v.patternDrag != nil {
						a.cancelArrangement()
						return true
					}
				case ui.InputPointerMove, ui.InputPointerUp:
					d := v.patternDrag
					if d == nil || d.pattern.ID != p.ID {
						return false
					}
					if ev.Canceled {
						a.cancelArrangement()
						return true
					}
					bounds := ref.Bounds(c)
					x, y := bounds.X+ev.X, bounds.Y+ev.Y
					d.trackID = ""
					for _, t := range a.active.State.Tracks {
						h := v.trackDrops[t.ID]
						if h == nil {
							continue
						}
						b := h.Bounds(c)
						if b.W <= 0 || b.H <= 0 || x < b.X || x >= b.X+b.W || y < b.Y || y >= b.Y+b.H {
							continue
						}
						d.trackID = t.ID
						d.tick = patternDropTick(v.start, x-b.X, a.active.PPQ, v.zoom, editorSnap(v.snap, v.zoom, a.active.PPQ))
						break
					}
					if ev.Kind == ui.InputPointerUp {
						a.cancelArrangement()
						if d.trackID != "" && ref.Focused(c) {
							a.mutateAt(d.target, "edit", []project.Op{{Kind: "pattern.place", PatternID: p.ID,
								Arrangement: &project.ArrangementEdit{Track: d.trackID, Tick: d.tick, Repeat: 1}}})
						} else if ref.Focused(c) && ev.X >= 0 && ev.Y >= 0 && ev.X < bounds.W && ev.Y < bounds.H {
							open()
						}
					}
					return true
				}
				return false
			})
		}
	})
}

func patternDropTick(start int64, x float32, ppq int, zoom, snap float64) int64 {
	if ppq < 1 || zoom <= 0 || snap <= 0 {
		return start
	}
	step := snap * float64(ppq)
	tick := float64(start) + float64(x)/zoom*float64(ppq)
	return max(0, min(project.MaxTick, int64(math.Round(math.Round(tick/step)*step))))
}
