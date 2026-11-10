package desktop

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/audioasset"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

type propertyValues struct {
	Sampler                                      *project.Sampler      `json:",omitempty"`
	Polish                                       *polishInputs         `json:",omitempty"`
	Comp                                         *compInputs           `json:",omitempty"`
	FreezeAssets                                 *[]project.AudioAsset `json:",omitempty"`
	Mixer                                        *project.Mixer        `json:",omitempty"`
	Audio                                        *audioFormValues      `json:",omitempty"`
	Name, Start, Length, Channel, Program, Color string
	Position, LoopStart, LoopEnd, Driver         string
	Loop                                         bool
	MIDI                                         *midiInputs `json:",omitempty"`
}
type propertyForm struct {
	preparing              bool
	cancelPrepare          func()
	capturePlugin          func()
	sampleAsset            string
	sampleIndex            int
	compTake, compPosition string
	compSource, compFrames int64
	compTakes              []audioasset.TakeStatus
	mixer                  mixerControls
	meter                  meterProjection
	open                   bool
	kind                   string
	entry                  musicDraft
	values                 propertyValues
	dirty                  bool
	applying               bool
	saveFailed             bool
	writes                 int
	version                uint64
	err                    string
}

func (a *Workspace) beginProperties(kind string, track project.Track, clip project.Clip) {
	if (kind != "create" && !a.writable()) || (kind == "create" && !a.projectAvailable()) || a.form != nil || a.fileBusy {
		return
	}
	v := propertyValues{Name: a.active.Title}
	if kind == "track" {
		v = propertyValues{Name: track.Name, Channel: strconv.Itoa(track.Channel + 1), Program: strconv.Itoa(track.Program + 1), Color: track.Color}
	}
	if kind == "clip" {
		v = propertyValues{Name: clip.Name, Start: strconv.FormatInt(clip.Start, 10), Length: strconv.FormatInt(clip.Length, 10), Color: clip.Color}
	}
	if kind == "create" {
		v.Name = "新工程"
	}
	target := EditTarget{a.active.ID, a.active.Revision}
	if kind == "create" {
		target = EditTarget{"new_project", 0}
	}
	tid, cid := "", ""
	if kind == "sampler" || kind == "track" || kind == "clip" || kind == "velocity" || kind == "audio_clip" || kind == "audio_polish" || midiFormKind(kind) {
		tid = track.ID
	}
	if kind == "clip" || kind == "velocity" || kind == "audio_clip" || kind == "audio_polish" || midiFormKind(kind) {
		cid = clip.ID
	}
	entry := newMusicDraft(target, a.active.Title, tid, cid, kind, "")
	for _, e := range a.savedInputs {
		if e.Key == entry.Key {
			a.message = "已有未应用输入，请先在草稿中恢复或丢弃"
			a.showDrafts = true
			return
		}
	}
	a.form = &propertyForm{open: true, kind: kind, entry: entry, values: v, meter: func() meterProjection { m, _ := newMeterProjection(a.active.PPQ, a.active.State.Meter); return m }()}
	a.preview.freeze(true)
	a.notifyPiano()
}
func (a *Workspace) changedForm(f *propertyForm) {
	f.dirty = true
	f.version++
	b, err := json.Marshal(f.values)
	if err != nil {
		f.saveFailed = true
		f.err = err.Error()
		return
	}
	if limit := app.MusicInputRawLimit(f.entry.Label); len(b) > limit {
		f.saveFailed = true
		f.err = fmt.Sprintf("输入超过草稿上限（%d 字节），请缩短", limit)
		return
	}
	f.entry.Raw = string(b)
	if f.kind == "field" {
		f.entry.Raw = f.values.Name
	}
	if f.entry.Label == "播放与音频设置" {
		raw, _ := json.Marshal(struct {
			Position string `json:"position"`
			Start    string `json:"start"`
			End      string `json:"end"`
			Loop     bool   `json:"loop"`
			Driver   string `json:"driver,omitempty"`
		}{f.values.Position, f.values.LoopStart, f.values.LoopEnd, f.values.Loop, f.values.Driver})
		f.entry.Raw = string(raw)
	}
	entry := f.entry
	f.writes++
	// The single queue retains input order; close sees writes before it can quit.
	a.draftQueue <- func() {
		err := a.drafts.put(entry, false)
		a.update(func() {
			f.writes--
			if err != nil {
				f.saveFailed = true
				f.err = "草稿未获保存确认：" + err.Error()
			} else if f.writes == 0 {
				f.saveFailed = false
				f.err = ""
			}
			a.reloadDrafts()
		})
	}
}
func (a *Workspace) reloadDrafts() {
	if a.drafts == nil {
		return
	}
	a.draftSequence++
	request := a.draftSequence
	go func() {
		entries, err := a.drafts.list()
		a.update(func() {
			if request != a.draftSequence {
				return
			}
			if err != nil {
				a.draftError = err.Error()
			} else {
				a.savedInputs = entries
				a.draftError = ""
			}
		})
	}()
}
func (a *Workspace) dismissForm() {
	a.stopMaterialPreview()
	if a.form != nil {
		a.finishMixerWrite(a.form)
	}
	a.form = nil
	a.preview.freeze(a.closeFrozen || a.mapForm != nil)
	a.notifyPiano()
}
func (a *Workspace) clearForm(f *propertyForm, done func()) {
	if f.writes != 0 {
		return
	}
	if !f.dirty {
		done()
		return
	}
	f.writes++
	f.applying = true
	a.draftQueue <- func() {
		err := a.drafts.put(f.entry, true)
		a.update(func() {
			f.writes--
			if err != nil {
				f.applying = false
				f.err = "清理草稿失败：" + err.Error()
				return
			}
			a.reloadDrafts()
			done()
		})
	}
}
func propertyOps(f *propertyForm) ([]project.Op, error) {
	v := f.values
	switch f.kind {
	case "sampler":
		if v.Sampler == nil {
			return nil, fmt.Errorf("采样器草稿无效")
		}
		var sampler *project.Sampler
		if len(v.Sampler.Zones) > 0 {
			sampler = v.Sampler.Copy()
		}
		return []project.Op{{Kind: "sampler.set", TrackID: f.entry.TrackID, Sampler: sampler}}, nil
	case "audio_polish":
		if v.Polish == nil || len(v.Polish.Ops) == 0 {
			return nil, fmt.Errorf("请先生成候选")
		}
		return v.Polish.Ops, nil
	case "mixer":
		if v.Mixer == nil {
			return nil, fmt.Errorf("混音草稿缺少快照")
		}
		var ops []project.Op
		var assets []project.AudioAsset
		if v.FreezeAssets != nil {
			assets = *v.FreezeAssets
		}
		for i := range assets {
			asset := &assets[i]
			if slices.ContainsFunc(v.Mixer.Channels, func(ch project.MixChannel) bool { return ch.FrozenAssetID == asset.ID }) {
				ops = append(ops, project.Op{Kind: "audio.asset", AudioAsset: asset})
			}
		}
		return append(ops, project.Op{Kind: "mixer.set", Mixer: v.Mixer}), nil
	case "field":
		return ordinaryFieldOps(f)
	case "velocity":
		return velocityOps(f)
	case "rename":
		if strings.TrimSpace(v.Name) == "" {
			return nil, fmt.Errorf("工程名称不能为空")
		}
		return []project.Op{{Kind: "project.rename", Title: v.Name}}, nil
	case "track":
		ch, err := strconv.Atoi(v.Channel)
		if err != nil || ch < 1 || ch > 16 {
			return nil, fmt.Errorf("通道须为 1–16 的整数")
		}
		program, err := strconv.Atoi(v.Program)
		if err != nil || program < 1 || program > 128 {
			return nil, fmt.Errorf("乐器程序须为 1–128 的整数")
		}
		if !project.ValidColor(v.Color) {
			return nil, fmt.Errorf("颜色无效")
		}
		ch--
		program--
		return []project.Op{{Kind: "track.update", TrackID: f.entry.TrackID, TrackPatch: &project.TrackPatch{Name: &v.Name, Channel: &ch, Program: &program, Color: &v.Color}}}, nil
	case "clip":
		start, err := strconv.ParseInt(v.Start, 10, 64)
		if err != nil || start < 0 || start > project.MaxTick {
			return nil, fmt.Errorf("起点须为 0–%d 的整数 tick", project.MaxTick)
		}
		length, err := strconv.ParseInt(v.Length, 10, 64)
		if err != nil || length < 1 || length > project.MaxTick {
			return nil, fmt.Errorf("长度须为 1–%d 的整数 tick", project.MaxTick)
		}
		if !project.ValidColor(v.Color) {
			return nil, fmt.Errorf("颜色无效")
		}
		return []project.Op{{Kind: "clip.update", TrackID: f.entry.TrackID, ClipID: f.entry.ClipID, ClipPatch: &project.ClipPatch{Name: &v.Name, Start: &start, Length: &length, Color: &v.Color}}}, nil
	}
	return nil, fmt.Errorf("不支持的表单")
}
func (a *Workspace) applyForm(f *propertyForm) {
	if f.preparing || f.mixer.write != nil || f.writes != 0 || f.err != "" || a.closeFrozen || (f.kind != "create" && !a.writable()) || a.editBusy || a.fileBusy {
		return
	}
	if f.kind == "comp" {
		a.applyComp(f)
		return
	}
	if f.kind == "create" {
		a.createFromForm(f)
		return
	}
	var ops []project.Op
	var err error
	if f.kind == "sampler" || f.kind == "audio_polish" {
		if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
			f.err = "原工程或修订已变化，保留输入供核对"
			return
		}
		if sampler := f.values.Sampler; f.kind == "sampler" && sampler != nil && len(sampler.Zones) > 0 {
			if e := project.ValidateSampler(&a.active.State, sampler); e != nil {
				f.err = e.Error()
				return
			}
		}
		ops, err = propertyOps(f)
	} else if f.kind == "mixer" {
		if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
			f.err = "原工程修订已变化，草稿保留供核对"
			return
		}
		if f.values.Mixer != nil {
			if _, _, e := project.MixOrder(&a.active.State, *f.values.Mixer); e != nil {
				f.err = e.Error()
				return
			}
		}
		ops, err = propertyOps(f)
	} else if midiFormKind(f.kind) || f.kind == "audio_clip" {
		if f.entry.Target.ID != a.active.ID || f.entry.Target.Revision != a.active.Revision {
			f.err = "原工程或修订已变化，保留输入供核对"
			return
		}
		if f.kind == "audio_clip" {
			ops, err = audioFormOps(f, &a.active.State)
		} else {
			ops, err = midiFormOps(f, &a.active.State)
		}
	} else {
		ops, err = propertyOps(f)
	}
	if err != nil {
		f.err = err.Error()
		return
	}
	target := EditTarget{f.entry.Target.ID, f.entry.Target.Revision}
	if target.ProjectID != a.active.ID {
		f.err = "原工程尚未打开或已删除，请保留输入并核对"
		return
	}
	f.applying = true
	a.submitEdit(target, "edit", ops, func() { a.clearForm(f, a.dismissForm) })
}
func (a *Workspace) propertiesDialog(c *ui.Context) {
	f := a.form
	if f == nil || a.closeFrozen {
		return
	}
	if !f.open {
		a.finishMixerWrite(f)
		f.open = true
		if f.writes == 0 && !f.saveFailed && !f.applying && !f.preparing && !a.editBusy {
			a.dismissForm()
		}
		return
	}
	names := map[string]string{"sampler": "采样器与鼓垫", "audio_polish": "音频精修", "comp": "Take 选段与 Comp", "mixer": "混音与自动化", "audio_clip": "音频片段", "track": "轨道属性", "clip": "片段属性", "rename": "工程名称", "create": "新建工程", "transport": "播放位置与循环", "field": f.entry.Label, "velocity": "所选音符力度", "midi_arrange": "批量片段", "midi_control": "MIDI 控制事件", "midi_steps": "鼓组步进器", "midi_chords": "音阶与和弦", "midi_marker": "段落标记", "midi_pattern": "Pattern 资源库"}
	width, height := c.Size()
	formWidth := min(420, width-64)
	ui.Modal(c, &f.open, func() {
		ui.Text(c, names[f.kind]).FontSize(18).Bold()
		ui.Textf(c, "冻结基准修订 %d；应用时校验，冲突不覆盖", f.entry.Target.Revision).FontSize(11)
		ui.Scroll(c).Width(formWidth).MaxHeight(height - 174).Disabled(f.applying || f.writes > 32).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				field := func(label string, v *string) {
					ui.Row(c.Key(label)).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, label).Width(90)
						ui.TextInput(c, v).Grow(1).Label(label).Disabled(a.editBusy || f.writes > 32).OnChange(func() { a.changedForm(f) })
					})
				}
				if f.kind != "sampler" && f.kind != "comp" && f.kind != "audio_polish" && f.kind != "mixer" && f.kind != "transport" && f.kind != "audio_clip" && !midiFormKind(f.kind) {
					label := "名称"
					if f.kind == "field" || f.kind == "velocity" {
						label = "原始整数值"
					}
					field(label, &f.values.Name)
				}
				if f.kind == "transport" {
					field("播放位置", &f.values.Position)
					field("循环起点", &f.values.LoopStart)
					field("循环终点", &f.values.LoopEnd)
					ui.Text(c, "小节:拍:拍内tick；也可输入整数 tick。变拍以当前工程为准。").FontSize(11).MaxWidth(400)
					ui.Checkbox(c, &f.values.Loop, "循环").Label("循环").OnChange(func() { a.changedForm(f) })
				}
				if f.kind == "track" {
					field("新音符通道", &f.values.Channel)
					field("乐器程序", &f.values.Program)
					ui.Text(c, "通道 10 使用 GM 打击组；改通道不改已有音符路由。").FontSize(11)
					a.instrumentChoices(c, f)
				}
				if f.kind == "clip" {
					field("起点 tick", &f.values.Start)
					field("长度 tick", &f.values.Length)
					ui.Text(c, "缩短不得截断已有音符、控制或原始事件。").FontSize(11)
				}
				if midiFormKind(f.kind) {
					a.midiEditorFields(c, f, field)
				}
				if f.kind == "mixer" {
					a.mixerFields(c, f)
				}
				if f.kind == "sampler" {
					a.samplerFields(c, f)
				}
				if f.kind == "audio_polish" {
					a.polishFields(c, f)
				}
				if f.kind == "comp" {
					ui.Box(c).Disabled(f.cancelPrepare != nil).Children(func() { a.compFields(c, f) })
				}
				if f.kind == "audio_clip" {
					a.audioClipFields(c, f, field)
					a.audioExtraFields(c, f)
				}
				if f.kind == "track" || f.kind == "clip" {
					colorPicker(c, &f.values.Color, func() { a.changedForm(f) })
				}
				if f.err != "" {
					ui.Text(c, f.err).TextColor(c.Theme().Warning)
					if (f.kind == "mixer" || f.kind == "sampler" || f.kind == "audio_polish" || f.kind == "comp") && !f.saveFailed {
						ui.Button(c, "确认提示").OnClick(func() { f.err = "" })
					}
				}
				if f.writes > 0 {
					ui.Text(c, "正在持久保存输入…")
				}
			})
		})
		ui.Row(c).Width(formWidth).Wrap().Gap(6).Justify(ui.End).Disabled(f.applying || f.writes > 32).Children(func() {
			if f.saveFailed {
				ui.Button(c, "重试保留输入").Disabled(f.writes > 0).OnClick(func() { a.changedForm(f) })
			}
			ui.Button(c, "保留输入并返回").Disabled(f.preparing || f.mixer.write != nil || f.writes > 0 || f.saveFailed || a.editBusy).OnClick(a.dismissForm)
			ui.Button(c, "丢弃输入").Disabled(f.preparing || f.mixer.write != nil || f.writes > 0 || a.editBusy).OnClick(func() { a.clearForm(f, a.dismissForm) })
			if f.kind == "transport" {
				ui.Button(c, "定位 / 应用循环").Disabled(f.writes > 0 || f.err != "" || a.audioBusy).OnClick(func() { a.applyTransport(f, true) })
			}
			if f.capturePlugin != nil {
				ui.PrimaryButton(c, "获取插件窗口更改").OnClick(func() {
					f.capturePlugin()
					f.capturePlugin = nil
				})
			}
			if f.preparing && f.cancelPrepare != nil {
				ui.Button(c, "取消后台处理").OnClick(f.cancelPrepare)
			}
			ui.PrimaryButton(c, "应用").Disabled(f.kind == "comp" && (f.values.Comp == nil || len(f.values.Comp.Parts) == 0 || f.values.Comp.Track == "") ||
				f.kind == "audio_polish" && (f.values.Polish == nil || len(f.values.Polish.Ops) == 0) || f.preparing || f.mixer.write != nil || f.writes > 0 || f.err != "" || a.editBusy || a.fileBusy || (f.kind != "create" && !a.writable())).OnClick(func() {
				if f.kind == "transport" {
					a.applyTransport(f, false)
				} else {
					a.applyForm(f)
				}
			})
		})
	})
}

var paletteNames = []string{"自动", "冰蓝", "薄荷", "紫罗兰", "琥珀", "珊瑚", "玫红", "青灰", "苔绿"}
var paletteHex = []string{"#5ec8e8", "#6fd6a3", "#a78bfa", "#f2b544", "#f28c6b", "#ec7bae", "#8fa3b8", "#9db85c"}

func colorPicker(c *ui.Context, value *string, changed func()) {
	ui.Row(c).Gap(6).Children(func() {
		ui.Text(c, "颜色")
		p := ui.SelectBase(c, value)
		p.Trigger.Padding(5, 8).Background(c.Theme().SurfaceHover).Border(1, c.Theme().Border).Width(160).Label("颜色").Children(func() {
			i := 0
			for j, id := range project.Palette {
				if id == *value {
					i = j + 1
				}
			}
			ui.Text(c, paletteNames[i]+" ▾")
		})
		p.Popup(func(panel ui.Element) {
			panel.Padding(4).Background(c.Theme().Surface)
			p.Item("").Padding(5, 8).Children(func() { ui.Text(c, paletteNames[0]) })
			for i, id := range project.Palette {
				p.Item(id).Padding(5, 8).Children(func() { ui.Text(c, paletteNames[i+1]) })
			}
		})
		if p.Trigger.Changed() {
			changed()
		}
	})
}
func displayObjectColor(clipColor, trackColor string, index int) ui.Color {
	if clipColor == "" {
		clipColor = trackColor
	}
	for i, id := range project.Palette {
		if id == clipColor {
			return ui.Hex(paletteHex[i])
		}
	}
	return ui.Hex(paletteHex[index%len(paletteHex)])
}
func (a *Workspace) instrumentChoices(c *ui.Context, f *propertyForm) {
	ui.Row(c).Gap(6).Children(func() {
		program, _ := strconv.Atoi(f.values.Program)
		name := "选择乐器"
		if program >= 1 && program <= len(gmNames) {
			name = gmNames[program-1]
		}
		p := ui.SelectBase(c, &f.values.Program)
		p.Trigger.Padding(5, 8).Width(280).Background(c.Theme().SurfaceHover).Border(1, c.Theme().Border).Label("GM 乐器").Children(func() { ui.Text(c, name+" ▾").SingleLine() })
		p.Popup(func(panel ui.Element) {
			panel.Padding(4).Background(c.Theme().Surface)
			ui.Scroll(c).Width(360).MaxHeight(300).Children(func() {
				for i, name := range gmNames {
					p.Item(strconv.Itoa(i+1)).Padding(5, 8).Children(func() { ui.Text(c, fmt.Sprintf("%03d  %s", i+1, name)) })
				}
			})
		})
		if p.Trigger.Changed() {
			a.changedForm(f)
		}
		ui.Button(c, "试听音色").Disabled(!a.audio.Available).OnClick(func() {
			program, err := strconv.Atoi(f.values.Program)
			if err != nil || program < 1 || program > 128 {
				return
			}
			bank := 0
			keys := []int{60}
			if f.values.Channel == "10" {
				bank = 128
				keys = []int{36, 38}
			}
			go func() {
				err := a.service.AuditionInstrument(playback.AuditionRequest{Program: program - 1, Bank: bank, Keys: keys, Velocity: 90, DurationMs: 700})
				if err != nil {
					a.update(func() { a.message = err.Error() })
				}
			}()
		})
	})
}

// Existing unapplied ordinary fields move to Native without rewriting their
// records or applying them against a fresh revision. This includes velocity
// inputs with frozen note IDs. It is UI draft takeover, not a project format reader.
func restoreMusicDraft(e musicDraft) (string, propertyValues, error) {
	var v propertyValues
	if strings.HasPrefix(e.Label, nativeInputPrefix) {
		kind := strings.TrimPrefix(e.Label, nativeInputPrefix)
		switch kind {
		case "track", "clip", "rename", "create", "transport", "velocity", "midi_arrange", "midi_control", "midi_steps", "midi_chords", "midi_marker", "midi_pattern", "audio_clip", "mixer", "sampler", "audio_polish", "comp":
		default:
			return "", v, fmt.Errorf("不支持的 Native 草稿类型；原记录保留")
		}
		err := json.Unmarshal([]byte(e.Raw), &v)
		return strings.TrimPrefix(e.Label, nativeInputPrefix), v, err
	}
	if e.Label == "播放与音频设置" {
		var old struct {
			Position string `json:"position"`
			Start    string `json:"start"`
			End      string `json:"end"`
			Loop     bool   `json:"loop"`
			Driver   string `json:"driver"`
		}
		if err := json.Unmarshal([]byte(e.Raw), &old); err != nil {
			return "", v, err
		}
		v.Position = old.Position
		v.LoopStart = old.Start
		v.LoopEnd = old.End
		v.Loop = old.Loop
		v.Driver = old.Driver
		return "transport", v, nil
	}
	if nativeDraft(e) {
		v.Name = e.Raw
		return "field", v, nil
	}
	return "", v, fmt.Errorf("该输入由卷帘管理")
}
func ordinaryFieldOps(f *propertyForm) ([]project.Op, error) {
	value, err := strconv.ParseInt(f.values.Name, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("请输入原始整数值，未改写输入")
	}
	if f.entry.Label == "音符力度" {
		return velocityOps(f)
	}
	op := project.Op{TrackID: f.entry.TrackID, ClipID: f.entry.ClipID}
	switch f.entry.Label {
	case "新音符通道（1–16）":
		if value < 1 || value > 16 {
			return nil, fmt.Errorf("通道须在 1–16")
		}
		v := int(value) - 1
		op.Kind = "track.update"
		op.TrackPatch = &project.TrackPatch{Channel: &v}
	case "乐器程序（1–128）":
		if value < 1 || value > 128 {
			return nil, fmt.Errorf("程序须在 1–128")
		}
		v := int(value) - 1
		op.Kind = "track.update"
		op.TrackPatch = &project.TrackPatch{Program: &v}
	case "片段起点（tick）":
		if value < 0 || value > project.MaxTick {
			return nil, fmt.Errorf("起点超出范围")
		}
		op.Kind = "clip.update"
		op.ClipPatch = &project.ClipPatch{Start: &value}
	case "片段长度（tick）":
		if value < 1 || value > project.MaxTick {
			return nil, fmt.Errorf("长度超出范围")
		}
		op.Kind = "clip.update"
		op.ClipPatch = &project.ClipPatch{Length: &value}
	default:
		return nil, fmt.Errorf("不支持的普通输入类型，原记录保留")
	}
	return []project.Op{op}, nil
}

func velocityOps(f *propertyForm) ([]project.Op, error) {
	value, err := strconv.Atoi(f.values.Name)
	if err != nil || value < 1 || value > 127 {
		return nil, fmt.Errorf("力度须为 1–127 的整数")
	}
	if len(f.entry.NoteIDs) == 0 {
		return nil, fmt.Errorf("请先选择音符")
	}
	deltas := make([]project.NoteDelta, 0, len(f.entry.NoteIDs))
	for _, id := range f.entry.NoteIDs {
		deltas = append(deltas, project.NoteDelta{ID: id, Velocity: &value})
	}
	return []project.Op{{Kind: "note.update", TrackID: f.entry.TrackID, ClipID: f.entry.ClipID, NoteDeltas: deltas}}, nil
}
func (a *Workspace) beginVelocity() {
	if !a.preview.toolsReady() {
		return
	}
	context := a.preview.Context()
	command := a.preview.command("velocity", 0)
	if len(command.NoteIDs) == 0 {
		return
	}
	a.beginProperties("velocity", context.Track, context.Clip)
	if a.form != nil && a.form.kind == "velocity" {
		a.form.values.Name = "100"
		a.form.entry.NoteIDs = command.NoteIDs
	}
}
