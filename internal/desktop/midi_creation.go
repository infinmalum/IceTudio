package desktop

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

type drumLane struct {
	Mute, Solo      bool `json:",omitempty"`
	Pitch, Velocity string
	Steps           uint32
}

type midiInputs struct {
	PatternID                                           string     `json:",omitempty"`
	ReplaceDrums                                        bool       `json:",omitempty"`
	DrumLanes                                           []drumLane `json:",omitempty"`
	StepCount                                           string     `json:",omitempty"`
	Remove                                              bool       `json:",omitempty"`
	Action, Destination, First, Last, Count             string
	Clips                                               []string
	Kind, Controller, Value, Event                      string
	Pitch, Velocity, Division, Swing, Probability, Bars string
	Steps                                               uint32
	Scale, Progression, Voicing                         string
}

func (a *Workspace) openMIDIEditor(kind string, track project.Track, clip project.Clip) {
	a.beginProperties(kind, track, clip)
	f := a.form
	if f == nil || f.kind != kind {
		return
	}
	f.values.MIDI = &midiInputs{
		Action: "移动", Clips: []string{clip.ID}, First: musicTime(f.meter, clip.Start),
		Last: musicTime(f.meter, clip.Start+clip.Length), Count: "1",
		Kind: "CC", Controller: "64", Value: "127",
		Pitch: "36", Velocity: "90", Division: "4", Swing: "0", Probability: "100", Bars: "1",
		Scale: "大调", Progression: "I IV V I",
	}
	if kind == "midi_steps" {
		f.values.MIDI.StepCount = "16"
		f.values.MIDI.DrumLanes = []drumLane{{Pitch: "36", Velocity: "90"}, {Pitch: "38", Velocity: "90"}, {Pitch: "42", Velocity: "72"}}
		if track.Sampler != nil {
			var lanes []drumLane
			seen := map[int]bool{}
			for _, z := range track.Sampler.Zones {
				if !z.OneShot || z.Low != z.High || seen[z.Low] {
					continue
				}
				seen[z.Low] = true
				lanes = append(lanes, drumLane{Pitch: strconv.Itoa(z.Low), Velocity: strconv.Itoa(max(z.MinVelocity, min(90, z.MaxVelocity)))})
				if len(lanes) == 8 {
					break
				}
			}
			if len(lanes) > 0 {
				f.values.MIDI.DrumLanes = lanes
			}
		}
	}
	if kind == "midi_arrange" {
		selected := f.values.MIDI.Clips
		for _, id := range a.arrangementUI.marked {
			if _, found := project.ClipTrack(&a.active.State, id); found && !slices.Contains(selected, id) {
				selected = append(selected, id)
			}
		}
		f.values.MIDI.Clips = selected
	}
	if kind == "midi_marker" {
		f.values.MIDI.Value = "段落"
	}
	if kind == "midi_chords" {
		f.values.MIDI.Pitch = "60"
	}
}

func midiFormKind(kind string) bool {
	switch kind {
	case "midi_arrange", "midi_control", "midi_steps", "midi_chords", "midi_marker", "midi_pattern":
		return true
	}
	return false
}

func midiFormOps(f *propertyForm, state *project.State) ([]project.Op, error) {
	v := f.values.MIDI
	if v == nil {
		return nil, fmt.Errorf("缺少编辑输入")
	}
	meter, err := newMeterProjection(state.PPQ, state.Meter)
	if err != nil {
		return nil, err
	}
	if f.kind == "midi_pattern" {
		if v.PatternID == "" {
			return nil, fmt.Errorf("请选择 Pattern 资源")
		}
		if v.Remove {
			return []project.Op{{Kind: "pattern.remove", PatternID: v.PatternID}}, nil
		}
		tick, err := parseMusicTime(meter, v.First)
		if err != nil {
			return nil, err
		}
		count, err := strconv.Atoi(v.Count)
		if err != nil {
			return nil, fmt.Errorf("铺排次数须为整数")
		}
		return []project.Op{{Kind: "pattern.place", PatternID: v.PatternID, Arrangement: &project.ArrangementEdit{Track: v.Destination, Tick: tick, Repeat: count}}}, nil
	}

	if f.kind == "midi_arrange" {
		action := map[string]string{"移动": "move", "复制 / 铺排": "copy", "拆分": "split", "合并": "merge", "裁剪": "crop", "共享铺排": "link", "解除共享": "detach", "分组": "group", "解除分组": "ungroup", "保存到 Pattern 库": "makePattern"}[v.Action]
		p := &project.ArrangementEdit{Action: action, IDs: slices.Clone(v.Clips), Track: v.Destination}
		if action != "move" && action != "copy" && action != "link" {
			p.Track = ""
		}
		if action != "merge" && action != "detach" && action != "group" && action != "ungroup" && action != "makePattern" {
			p.Tick, err = parseMusicTime(meter, v.First)
			if err != nil {
				return nil, err
			}
		}
		if action == "crop" {
			p.End, err = parseMusicTime(meter, v.Last)
			if err != nil {
				return nil, err
			}
		}
		if action == "copy" || action == "link" {
			p.Repeat, err = strconv.Atoi(v.Count)
			if err != nil {
				return nil, fmt.Errorf("铺排次数须为整数")
			}
		}
		return []project.Op{{Kind: "clip.arrange", Arrangement: p}}, nil
	}
	for _, t := range state.Tracks {
		for _, clip := range t.Clips {
			if clip.ID == f.entry.ClipID && t.ID == f.entry.TrackID {
				return compileMIDIForm(f.kind, v, meter, state.PPQ, t, clip)
			}
		}
	}
	return nil, fmt.Errorf("原片段已不存在")
}

func compileMIDIForm(kind string, v *midiInputs, meter meterProjection, ppq int, track project.Track, clip project.Clip) ([]project.Op, error) {
	if kind == "midi_marker" && v.Event != "" {
		return []project.Op{{Kind: "marker.remove", ClipID: clip.ID, EventIDs: []string{v.Event}}}, nil
	}
	if kind == "midi_control" && v.Event != "" && v.Remove {
		if !slices.ContainsFunc(clip.Controls, func(e project.ControlEvent) bool {
			return e.ID == v.Event && (e.Kind == project.KindCC || e.Kind == project.KindPitchBend)
		}) {
			return nil, fmt.Errorf("原控制事件不存在")
		}
		return []project.Op{{Kind: "control.remove", ClipID: clip.ID, EventIDs: []string{v.Event}}}, nil
	}
	start, err := parseMusicTime(meter, v.First)
	if err != nil {
		return nil, err
	}
	start -= clip.Start
	if start < 0 || start >= clip.Length {
		return nil, fmt.Errorf("插入位置须在片段内")
	}
	if kind == "midi_marker" {
		return []project.Op{{Kind: "marker.add", ClipID: clip.ID, Marker: &project.MarkerSpec{Tick: start, Name: v.Value}}}, nil
	}
	if kind == "midi_control" {
		value, err := strconv.Atoi(v.Value)
		if err != nil {
			return nil, fmt.Errorf("控制值须为整数")
		}
		event := project.EventSpec{Tick: start, Kind: project.KindPitchBend, Value: value, Channel: track.Channel}
		if v.Kind == "CC" {
			cc, err := strconv.Atoi(v.Controller)
			if err != nil {
				return nil, fmt.Errorf("控制器编号须为整数")
			}
			event.Kind, event.Controller = project.KindCC, cc
		}
		op := "control.add"
		if v.Event != "" {
			i := slices.IndexFunc(clip.Controls, func(e project.ControlEvent) bool {
				return e.ID == v.Event && (e.Kind == project.KindCC || e.Kind == project.KindPitchBend)
			})
			if i < 0 {
				return nil, fmt.Errorf("原控制事件不存在")
			}
			op, event.ID, event.Channel = "control.update", v.Event, clip.Controls[i].Channel
		}
		return []project.Op{{Kind: op, ClipID: clip.ID, Events: []project.EventSpec{event}}}, nil
	}

	pitch, pitchErr := strconv.Atoi(v.Pitch)
	velocity, velErr := strconv.Atoi(v.Velocity)
	if pitchErr != nil || pitch < 0 || pitch > 127 || velErr != nil || velocity < 1 || velocity > 127 {
		return nil, fmt.Errorf("音高须为 0–127，力度须为 1–127")
	}
	notes := make([]project.NewNote, 0, 128)
	var replaceIDs []string
	if kind == "midi_steps" {
		div, divErr := strconv.Atoi(v.Division)
		swing, swingErr := strconv.Atoi(v.Swing)
		prob, probErr := strconv.Atoi(v.Probability)
		bars, barsErr := strconv.Atoi(v.Bars)
		if divErr != nil || div < 1 || div > 16 || swingErr != nil || swing < 0 || swing > 75 || probErr != nil || prob < 1 || prob > 100 || barsErr != nil || bars < 1 || bars > 32 {
			return nil, fmt.Errorf("每拍步数 1–16，摆动 0–75%%，概率 1–100%%，重复 1–32，至少打开一步")
		}
		at := func(step int) int64 {
			scaled := step * 100
			if step%2 == 1 {
				scaled += swing
			}
			return start + (int64(scaled)*int64(ppq)+int64(div*50))/int64(div*100)
		}
		steps := 16
		if v.StepCount != "" {
			steps, err = strconv.Atoi(v.StepCount)
			if err != nil || steps < 1 || steps > 32 {
				return nil, fmt.Errorf("步数须为 1–32")
			}
		}
		lanes := v.DrumLanes
		if len(lanes) == 0 || v.Steps != 0 {
			lanes = []drumLane{{Pitch: v.Pitch, Velocity: v.Velocity, Steps: v.Steps}}
		}
		if len(lanes) > 8 {
			return nil, fmt.Errorf("最多 8 行鼓音色")
		}
		used := map[int]bool{}
		solo := slices.ContainsFunc(lanes, func(l drumLane) bool { return l.Solo })
		for _, lane := range lanes {
			if lane.Mute || solo && !lane.Solo {
				continue
			}
			if key, err := strconv.Atoi(lane.Pitch); err == nil {
				used[key] = true
			}
		}
		if v.ReplaceDrums {
			end := at(steps * bars)
			for _, n := range clip.Notes {
				if n.Channel == 9 && used[n.Pitch] && n.Start >= start && n.Start < end {
					replaceIDs = append(replaceIDs, n.ID)
				}
			}
		}
		used = map[int]bool{}
		for _, lane := range lanes {
			if lane.Mute || solo && !lane.Solo {
				continue
			}
			key, keyErr := strconv.Atoi(lane.Pitch)
			gain, gainErr := strconv.Atoi(lane.Velocity)
			if keyErr != nil || key < 0 || key > 127 || gainErr != nil || gain < 1 || gain > 127 {
				return nil, fmt.Errorf("鼓组音高须为 0–127，力度 1–127")
			}
			if lane.Steps == 0 {
				continue
			}
			if used[key] {
				return nil, fmt.Errorf("鼓组行音高不能重复")
			}
			used[key] = true
			for step := 0; step < steps*bars; step++ {
				if lane.Steps&(1<<uint(step%steps)) == 0 || (step*67+key*23)%100 >= prob {
					continue
				}
				tick := at(step)
				notes = append(notes, project.NewNote{Start: tick, Duration: max(1, (at(step+1)-tick)/2), Pitch: key, Velocity: gain, Channel: 9})
			}
		}
	} else {
		scale := []int{0, 2, 4, 5, 7, 9, 11}
		if v.Scale == "自然小调" {
			scale = []int{0, 2, 3, 5, 7, 8, 10}
		}
		degrees := strings.Fields(strings.ToUpper(v.Progression))
		if len(degrees) == 0 || len(degrees) > 32 {
			return nil, fmt.Errorf("输入 1–32 个罗马数字级数")
		}
		for i, degree := range degrees {
			d := slices.Index([]string{"I", "II", "III", "IV", "V", "VI", "VII"}, degree)
			if d < 0 {
				return nil, fmt.Errorf("无法识别级数 %q", degree)
			}
			offsets := []int{0, 2, 4}
			if v.Voicing == "贝斯根音" {
				offsets = offsets[:1]
			}
			for _, offset := range offsets {
				n := d + offset
				key := pitch + scale[n%7] + 12*(n/7)
				notes = append(notes, project.NewNote{Start: start + int64(i*ppq), Duration: int64(ppq), Pitch: key, Velocity: velocity, Channel: track.Channel})
			}
		}
	}
	if len(notes) == 0 {
		return nil, fmt.Errorf("本次概率计算未产生音符，请增加概率")
	}
	for _, n := range notes {
		if n.Start+n.Duration > clip.Length || n.Pitch > 127 {
			return nil, fmt.Errorf("结果超出片段或音域，请缩短、提前或降低根音")
		}
	}
	var ops []project.Op
	if len(replaceIDs) > 0 {
		ops = append(ops, project.Op{Kind: "note.remove", ClipID: clip.ID, NoteIDs: replaceIDs})
	}
	return append(ops, project.Op{Kind: "note.add", ClipID: clip.ID, Notes: notes}), nil
}

func (a *Workspace) midiEditorFields(c *ui.Context, f *propertyForm, field func(string, *string)) {
	v := f.values.MIDI
	if v == nil {
		return
	}
	changed := func() { a.changedForm(f) }
	if f.kind == "midi_pattern" {
		a.patternLibraryFields(c, f, field)
		return
	}
	if members := project.PatternMembers(&a.active.State, f.entry.ClipID); len(members) > 0 {
		ui.Textf(c, "共享 Pattern · %d 个实例", len(members))
		ui.Scroll(c).MaxHeight(70).Children(func() {
			for _, part := range members {
				ui.Textf(c, "%s / %s · %s", part.TrackName, part.ClipName, musicTime(f.meter, part.Start)).FontSize(12)
			}
		})
	}

	if f.kind == "midi_arrange" {
		ui.Select(c, &v.Action, []string{"移动", "复制 / 铺排", "拆分", "合并", "裁剪", "共享铺排", "解除共享", "分组", "解除分组", "保存到 Pattern 库"}).Label("片段操作").OnChange(changed)
		ui.Textf(c, "已选 %d 个片段", len(v.Clips))
		ui.Scroll(c).MaxHeight(140).Children(func() {
			for _, t := range a.active.State.Tracks {
				for _, clip := range t.Clips {
					checked := slices.Contains(v.Clips, clip.ID)
					ui.Checkbox(c.Key(clip.ID), &checked, t.Name+" / "+clip.Name).OnChange(func() {
						if checked {
							v.Clips = append(v.Clips, clip.ID)
						} else {
							v.Clips = slices.DeleteFunc(v.Clips, func(id string) bool { return id == clip.ID })
						}
						changed()
					})
				}
			}
		})
		if v.Action == "移动" || v.Action == "复制 / 铺排" || v.Action == "共享铺排" {
			p := ui.SelectBase(c, &v.Destination)
			title := "保留各自原轨"
			for _, t := range a.active.State.Tracks {
				if t.ID == v.Destination {
					title = t.Name
				}
			}
			p.Trigger.Label("目标轨").Children(func() { ui.Text(c, title+" ▾") })
			p.Popup(func(panel ui.Element) {
				panel.Background(c.Theme().Surface)
				ui.Scroll(c).MaxHeight(180).Children(func() {
					p.Item("").Children(func() { ui.Text(c, "保留各自原轨") })
					for _, t := range a.active.State.Tracks {
						p.Item(t.ID).Children(func() { ui.Text(c, t.Name) })
					}
				})
			})
			if p.Trigger.Changed() {
				changed()
			}
		}
		if v.Action != "合并" && v.Action != "解除共享" && v.Action != "分组" && v.Action != "解除分组" && v.Action != "保存到 Pattern 库" {
			field("目标位置", &v.First)
		}
		if v.Action == "复制 / 铺排" || v.Action == "共享铺排" {
			field("铺排次数", &v.Count)
		}
		if v.Action == "裁剪" {
			field("保留终点", &v.Last)
			ui.Text(c, "应用将移除区间外事件并截短交界音符。可以撤销。").MaxWidth(400)
		}
		return
	}

	field("插入位置", &v.First)
	if f.kind == "midi_marker" {
		field("段落名称", &v.Value)
		ui.Text(c, "新增标准 MIDI 段落标记。点击已有标记选择删除。")
		ui.Scroll(c).MaxHeight(130).Children(func() {
			for _, t := range a.active.State.Tracks {
				for _, clip := range t.Clips {
					if clip.ID != f.entry.ClipID {
						continue
					}
					for _, e := range clip.Raw {
						name, ok := project.MarkerName(e.Data)
						if !ok {
							continue
						}
						label := musicTime(f.meter, clip.Start+e.Tick) + " · " + name
						if e.ID == v.Event {
							label = "删除 · " + label
						}
						ui.Button(c.Key(e.ID), label).OnClick(func() {
							if e.ID == v.Event {
								v.Event = ""
							} else {
								v.Event = e.ID
							}
							changed()
						})
					}
				}
			}
		})
		return
	}

	if f.kind == "midi_control" {
		if v.Event != "" {
			ui.Checkbox(c, &v.Remove, "应用时删除所选事件").OnChange(changed)
		}
		ui.Select(c, &v.Kind, []string{"CC", "弯音"}).Label("控制类型").OnChange(changed)
		if v.Kind == "CC" {
			field("CC 编号", &v.Controller)
		}
		field("控制值", &v.Value)
		ui.Text(c, "CC 值 0–127，弯音 0–16383（8192 居中）。点击事件可修改原值。").MaxWidth(400)
		ui.Scroll(c).MaxHeight(130).Children(func() {
			for _, t := range a.active.State.Tracks {
				for _, clip := range t.Clips {
					if clip.ID != f.entry.ClipID {
						continue
					}
					for _, e := range clip.Controls {
						if e.Kind != project.KindCC && e.Kind != project.KindPitchBend {
							continue
						}
						title := fmt.Sprintf("%s · %s %d → %d", musicTime(f.meter, clip.Start+e.Tick), e.Kind, e.Controller, e.Value)
						if e.ID == v.Event {
							title = "已选 · " + title
						}
						ui.Button(c.Key(e.ID), title).OnClick(func() {
							if v.Event == e.ID {
								v.Event = ""
							} else {
								v.Event = e.ID
								v.First = strconv.FormatInt(clip.Start+e.Tick, 10)
								v.Value = strconv.Itoa(e.Value)
								v.Controller = strconv.Itoa(e.Controller)
								v.Kind = "CC"
								if e.Kind == project.KindPitchBend {
									v.Kind = "弯音"
								}
							}
							v.Remove = false
							changed()
						})
					}
				}
			}
		})
		return
	}
	if f.kind == "midi_steps" {
		field("每拍步数", &v.Division)
		field("循环步数", &v.StepCount)
		field("奇数步摆动 %", &v.Swing)
		field("落点概率 %", &v.Probability)
		field("重复次数", &v.Bars)
		ui.Checkbox(c, &v.ReplaceDrums, "替换范围内对应鼓音高的已有音符").OnChange(changed)
		count, _ := strconv.Atoi(v.StepCount)
		if count < 1 || count > 32 {
			count = 16
		}
		ui.Scroll(c).MaxHeight(205).Children(func() {
			for index := range v.DrumLanes {
				lane := &v.DrumLanes[index]
				ui.Column(c.Key(fmt.Sprintf("drum-%d", index))).Gap(3).Children(func() {
					ui.Row(c).Gap(4).Children(func() {
						ui.Textf(c, "鼓 %d", index+1)
						ui.TextInput(c, &lane.Pitch).Width(70).Label("GM 音高").OnChange(changed)
						ui.TextInput(c, &lane.Velocity).Width(70).Label("力度").OnChange(changed)
						ui.Checkbox(c, &lane.Mute, "M").Label("静音").OnChange(changed)
						ui.Checkbox(c, &lane.Solo, "S").Label("独奏").OnChange(changed)
						ui.Button(c, "移除行").OnClick(func() { v.DrumLanes = slices.Delete(v.DrumLanes, index, index+1); changed() })
					})
					for row := 0; row < count; row += 8 {
						ui.Row(c).Gap(3).Children(func() {
							for i := row; i < min(row+8, count); i++ {
								bit := uint32(1) << i
								label := fmt.Sprintf("%d", i+1)
								if lane.Steps&bit != 0 {
									label += " ✓"
								}
								ui.Button(c.Key(fmt.Sprint(i)), label).OnClick(func() { lane.Steps ^= bit; changed() })
							}
						})
					}
				})
			}
		})
		ui.Row(c).Gap(4).Children(func() {
			ui.Button(c, "增加鼓音色").Disabled(len(v.DrumLanes) >= 8).OnClick(func() {
				v.DrumLanes = append(v.DrumLanes, drumLane{Pitch: "46", Velocity: "85"})
				changed()
			})
			a.midiAuditionButton(c, f)
		})
		return
	}
	field("根音", &v.Pitch)
	field("力度", &v.Velocity)
	ui.Select(c, &v.Scale, []string{"大调", "自然小调"}).Label("音阶").OnChange(changed)
	voicing := v.Voicing
	if voicing == "" {
		voicing = "三和弦"
	}
	ui.Select(c, &voicing, []string{"三和弦", "贝斯根音"}).Label("声部").OnChange(func() { v.Voicing = voicing; changed() })
	field("级数（每拍）", &v.Progression)
	ui.Row(c).Gap(4).Children(func() {
		for _, candidate := range []string{"I IV V I", "I V VI IV", "II V I I"} {
			ui.Button(c.Key(candidate), candidate).OnClick(func() { v.Progression = candidate; changed() })
		}
	})
	ui.Text(c, "输入 I–VII，如 I IV V I，按所选音阶生成三和弦。").MaxWidth(400)
	a.midiAuditionButton(c, f)
}

func (a *Workspace) midiAuditionButton(c *ui.Context, f *propertyForm) {
	ui.Button(c, "试听草稿").Disabled(a.audioBusy || !a.audio.Available).OnClick(func() {
		if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
			f.err = "原工程修订已变化"
			return
		}
		ops, err := midiFormOps(f, &a.active.State)
		if err != nil {
			f.err = err.Error()
			return
		}
		a.audioBusy = true
		a.audioPending++
		target := f.entry
		go func() {
			err := a.service.PlayMIDISketch(target.Target.ID, target.Target.Revision, target.TrackID, target.ClipID, ops[len(ops)-1].Notes)
			a.update(func() {
				a.audioPending--
				a.audioBusy = a.audioPending > 0
				if err != nil {
					a.message = err.Error()
				}
			})
		}()
	})
	ui.Button(c, "停止试听").Disabled(a.audioBusy || a.service == nil).OnClick(func() {
		go a.service.StopMIDISketch()
	})
}
