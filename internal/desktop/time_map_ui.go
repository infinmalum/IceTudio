package desktop

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// These DTOs preserve the current time-map-draft.json contract, including
// originalUS: unchanged imported tempos must never be rounded through BPM.
type tempoInput struct {
	Key         int    `json:"key"`
	Tick        string `json:"tick"`
	BPM         string `json:"bpm"`
	OriginalBPM string `json:"originalBPM"`
	OriginalUS  int    `json:"originalUS"`
}
type meterInput struct {
	Key         int    `json:"key"`
	Tick        string `json:"tick"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}
type timeMapInputs struct {
	Tempo []tempoInput `json:"tempo"`
	Meter []meterInput `json:"meter"`
}
type timeMapSnapshot struct {
	Version      int                  `json:"version"`
	ProjectID    string               `json:"projectID"`
	Title        string               `json:"title"`
	PPQ          int                  `json:"ppq"`
	BaseRevision int64                `json:"baseRevision"`
	Tempo        []project.TempoPoint `json:"tempo"`
	Meter        []project.MeterPoint `json:"meter"`
	Draft        timeMapInputs        `json:"draft"`
}
type timeMapForm struct {
	open                       bool
	snapshot                   timeMapSnapshot
	record                     *app.TimeMapDraftRecord
	nextKey                    int
	tempoPage, meterPage       int
	writes                     int
	dirty, applying, committed bool
	saveFailed                 bool
	version                    uint64
	err                        string
}

func newTimeMap(p storage.Project) *timeMapForm {
	f := &timeMapForm{open: true, nextKey: 1, snapshot: timeMapSnapshot{Version: 1, ProjectID: p.ID, Title: p.Title, PPQ: p.PPQ, BaseRevision: p.Revision, Tempo: append([]project.TempoPoint(nil), p.State.Tempo...), Meter: append([]project.MeterPoint(nil), p.State.Meter...)}}
	for i, p := range p.State.Tempo {
		bpm := strconv.FormatFloat(60000000/float64(p.USPerQuarter), 'g', -1, 64)
		f.snapshot.Draft.Tempo = append(f.snapshot.Draft.Tempo, tempoInput{i, strconv.FormatInt(p.Tick, 10), bpm, bpm, p.USPerQuarter})
		f.nextKey = max(f.nextKey, i+1)
	}
	for i, p := range p.State.Meter {
		f.snapshot.Draft.Meter = append(f.snapshot.Draft.Meter, meterInput{i, strconv.FormatInt(p.Tick, 10), strconv.Itoa(p.Numerator), strconv.Itoa(p.Denominator)})
		f.nextKey = max(f.nextKey, i+1)
	}
	return f
}
func compileTimeMap(s timeMapSnapshot) ([]project.Op, error) {
	tempo := make([]project.TempoPoint, 0, len(s.Draft.Tempo))
	meter := make([]project.MeterPoint, 0, len(s.Draft.Meter))
	for _, r := range s.Draft.Tempo {
		tick, err := strconv.ParseInt(r.Tick, 10, 64)
		if err != nil || tick < 0 || tick > project.MaxTick {
			return nil, fmt.Errorf("速度位置须为有效整数 tick")
		}
		us := r.OriginalUS
		if r.BPM != r.OriginalBPM {
			bpm, err := strconv.ParseFloat(r.BPM, 64)
			value := math.Round(60000000 / bpm)
			if err != nil || math.IsNaN(bpm) || math.IsInf(bpm, 0) || bpm <= 0 || value < 1 || value > 0xffffff {
				return nil, fmt.Errorf("BPM 须对应 1–16777215 微秒/四分音符")
			}
			us = int(value)
		}
		tempo = append(tempo, project.TempoPoint{Tick: tick, USPerQuarter: us})
	}
	for _, r := range s.Draft.Meter {
		tick, err := strconv.ParseInt(r.Tick, 10, 64)
		if err != nil || tick < 0 || tick > project.MaxTick {
			return nil, fmt.Errorf("拍号位置须为有效整数 tick")
		}
		n, err := strconv.Atoi(r.Numerator)
		if err != nil || n < 1 || n > 255 {
			return nil, fmt.Errorf("拍号分子须为 1–255 的整数")
		}
		d, err := strconv.Atoi(r.Denominator)
		if err != nil || d < 1 || d > 128 || d&(d-1) != 0 {
			return nil, fmt.Errorf("拍号分母须为 1–128 中 2 的幂")
		}
		meter = append(meter, project.MeterPoint{Tick: tick, Numerator: n, Denominator: d})
	}
	sort.Slice(tempo, func(i, j int) bool { return tempo[i].Tick < tempo[j].Tick })
	sort.Slice(meter, func(i, j int) bool { return meter[i].Tick < meter[j].Tick })
	if len(tempo) == 0 || len(meter) == 0 || tempo[0].Tick != 0 || meter[0].Tick != 0 {
		return nil, fmt.Errorf("速度与拍号均须保留 tick 0 起点")
	}
	for i := 1; i < len(tempo); i++ {
		if tempo[i].Tick == tempo[i-1].Tick {
			return nil, fmt.Errorf("速度图含重复 tick")
		}
	}
	for i := 1; i < len(meter); i++ {
		if meter[i].Tick == meter[i-1].Tick {
			return nil, fmt.Errorf("拍号图含重复 tick")
		}
	}
	ops := []project.Op{}
	if !reflect.DeepEqual(tempo, s.Tempo) {
		ops = append(ops, project.Op{Kind: "tempo.set", Tempo: tempo})
	}
	if !reflect.DeepEqual(meter, s.Meter) {
		ops = append(ops, project.Op{Kind: "meter.set", Meter: meter})
	}
	return ops, nil
}
func (a *Workspace) checkTimeMap() {
	if a.service == nil {
		return
	}
	a.mapReady = false
	a.mapSequence++
	request := a.mapSequence
	go func() {
		r, err := a.service.GetTimeMapDraft()
		a.update(func() {
			if request != a.mapSequence {
				return
			}
			a.mapReady = err == nil
			if err != nil {
				a.mapError = err.Error()
			} else {
				a.savedMap = r
				a.mapError = ""
			}
		})
	}()
}
func (a *Workspace) openTimeMap(restore bool) {
	if !a.projectAvailable() || !a.mapReady || a.mapForm != nil {
		return
	}
	f := newTimeMap(a.active)
	if restore {
		if a.savedMap == nil || a.savedMap.Payload == "" {
			return
		}
		if err := json.Unmarshal([]byte(a.savedMap.Payload), &f.snapshot); err != nil {
			a.mapError = err.Error()
			return
		}
		f.record = a.savedMap
		f.dirty = true
		for _, r := range f.snapshot.Draft.Tempo {
			f.nextKey = max(f.nextKey, r.Key+1)
		}
		for _, r := range f.snapshot.Draft.Meter {
			f.nextKey = max(f.nextKey, r.Key+1)
		}
		a.open(f.snapshot.ProjectID)
	} else if a.savedMap != nil && a.savedMap.Payload != "" {
		a.message = "请先恢复或丢弃时间图草稿"
		return
	} else {
		f.record = a.savedMap
	}
	a.mapForm = f
	a.preview.freeze(true)
	a.notifyPiano()
}
func (a *Workspace) checkpointMap(f *timeMapForm) {
	f.dirty = true
	f.version++
	b, err := json.Marshal(f.snapshot)
	if err != nil {
		f.err = err.Error()
		return
	}
	payload := string(b)
	f.writes++
	a.draftQueue <- func() {
		expected := ""
		if f.record != nil {
			expected = f.record.Token
		}
		token := project.NewID("map")
		err := a.service.SaveTimeMapDraft(expected, token, payload)
		if err == nil {
			f.record = &app.TimeMapDraftRecord{Version: 1, Token: token, Payload: payload}
		}
		a.update(func() {
			f.writes--
			if err != nil {
				f.saveFailed = true
				f.err = "时间图草稿未保存：" + err.Error()
			} else {
				a.savedMap = &app.TimeMapDraftRecord{Version: 1, Token: token, Payload: payload}
				if f.writes == 0 {
					f.saveFailed = false
					f.err = ""
				}
			}
		})
	}
}
func (a *Workspace) dismissMap() {
	a.mapForm = nil
	a.preview.freeze(a.closeFrozen || a.form != nil)
	a.notifyPiano()
}
func (a *Workspace) clearMap(f *timeMapForm) {
	if f.writes > 0 {
		return
	}
	f.applying = true
	f.writes++
	a.draftQueue <- func() {
		expected := ""
		if f.record != nil {
			expected = f.record.Token
		}
		token := project.NewID("mapclear")
		err := a.service.ClearTimeMapDraft(expected, token)
		a.update(func() {
			f.writes--
			f.applying = false
			if err != nil {
				f.err = "清理失败，草稿仍保留：" + err.Error()
				return
			}
			a.savedMap = &app.TimeMapDraftRecord{Version: 1, Token: token}
			a.dismissMap()
		})
	}
}
func (a *Workspace) applyTimeMap(f *timeMapForm) {
	if f.writes > 0 || f.err != "" || f.applying || f.committed || !a.writable() {
		return
	}
	if f.snapshot.ProjectID != a.active.ID {
		f.err = "原工程尚未打开或已删除；草稿保留"
		return
	}
	ops, err := compileTimeMap(f.snapshot)
	if err != nil {
		f.err = err.Error()
		return
	}
	if len(ops) == 0 {
		a.clearMap(f)
		return
	}
	f.applying = true
	a.submitEdit(EditTarget{f.snapshot.ProjectID, f.snapshot.BaseRevision}, "edit", ops, func() { f.committed = true; f.applying = false; a.clearMap(f) })
}
func (a *Workspace) timeMapTools(c *ui.Context) {
	ui.Button(c, "速度 / 拍号").Disabled(!a.projectAvailable() || !a.mapReady || a.active.ID == "").OnClick(func() { a.openTimeMap(false) })
	if !a.mapReady && a.service != nil {
		ui.Button(c, "重试时间图检查").OnClick(a.checkTimeMap)
	}
	if a.savedMap != nil && a.savedMap.Payload != "" {
		ui.Button(c, "恢复时间图草稿").Disabled(!a.projectAvailable()).OnClick(func() { a.openTimeMap(true) })
	}
}
func (a *Workspace) timeMapDialog(c *ui.Context) {
	f := a.mapForm
	if f == nil || a.closeFrozen {
		return
	}
	if !f.open {
		f.open = true
		if f.writes == 0 && !f.saveFailed && !f.applying && !a.editBusy {
			a.dismissMap()
		}
		return
	}
	ui.Modal(c, &f.open, func() {
		ui.Text(c, "速度与拍号").FontSize(18).Bold()
		ui.Textf(c, "%s · 冻结修订 %d · 绝对整数 tick", f.snapshot.Title, f.snapshot.BaseRevision)
		ui.Text(c, "拍号变化开启新小节；未改 BPM 的导入速度保留原始微秒值。").MaxWidth(660).FontSize(11)
		ui.Column(c).Width(660).Gap(8).Disabled(f.applying || a.editBusy || f.writes > 32).Children(func() {
			paginate := func(label string, page *int, count int, add func()) {
				*page = min(*page, max(0, (count-1)/24))
				ui.Row(c.Key(label)).Gap(6).Children(func() {
					ui.Text(c, label).Bold()
					ui.Button(c, "上一页").Disabled(*page == 0).OnClick(func() { *page-- })
					ui.Textf(c, "%d/%d · %d 点", *page+1, max(1, (count+23)/24), count)
					ui.Button(c, "下一页").Disabled((*page+1)*24 >= count).OnClick(func() { *page++ })
					ui.Button(c, "添加").Disabled(f.writes > 32).OnClick(add)
				})
			}
			paginate("速度图", &f.tempoPage, len(f.snapshot.Draft.Tempo), func() {
				f.snapshot.Draft.Tempo = append(f.snapshot.Draft.Tempo, tempoInput{Key: f.nextKey, Tick: strconv.FormatInt(int64(f.snapshot.PPQ)*4, 10), BPM: "120", OriginalBPM: "120", OriginalUS: 500000})
				f.nextKey++
				f.tempoPage = (len(f.snapshot.Draft.Tempo) - 1) / 24
				a.checkpointMap(f)
			})
			ui.Scroll(c).Height(150).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					for i := f.tempoPage * 24; i < min(len(f.snapshot.Draft.Tempo), (f.tempoPage+1)*24); i++ {
						r := &f.snapshot.Draft.Tempo[i]
						ui.Row(c.Key(fmt.Sprint("tempo", r.Key))).Gap(6).Children(func() {
							ui.Text(c, "tick")
							ui.TextInput(c, &r.Tick).Width(160).Disabled(r.Key == 0).Label(fmt.Sprint("速度位置 ", r.Key)).OnChange(func() { a.checkpointMap(f) })
							ui.Text(c, "BPM")
							ui.TextInput(c, &r.BPM).Width(160).Label(fmt.Sprint("速度 BPM ", r.Key)).OnChange(func() { a.checkpointMap(f) })
							ui.Button(c, "删除点").Disabled(r.Key == 0).OnClick(func() {
								f.snapshot.Draft.Tempo = append(f.snapshot.Draft.Tempo[:i], f.snapshot.Draft.Tempo[i+1:]...)
								a.checkpointMap(f)
							})
						})
					}
				})
			})
			paginate("拍号图", &f.meterPage, len(f.snapshot.Draft.Meter), func() {
				f.snapshot.Draft.Meter = append(f.snapshot.Draft.Meter, meterInput{Key: f.nextKey, Tick: strconv.FormatInt(int64(f.snapshot.PPQ)*4, 10), Numerator: "4", Denominator: "4"})
				f.nextKey++
				f.meterPage = (len(f.snapshot.Draft.Meter) - 1) / 24
				a.checkpointMap(f)
			})
			ui.Scroll(c).Height(150).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					for i := f.meterPage * 24; i < min(len(f.snapshot.Draft.Meter), (f.meterPage+1)*24); i++ {
						r := &f.snapshot.Draft.Meter[i]
						ui.Row(c.Key(fmt.Sprint("meter", r.Key))).Gap(6).Children(func() {
							ui.Text(c, "tick")
							ui.TextInput(c, &r.Tick).Width(160).Disabled(r.Key == 0).Label(fmt.Sprint("拍号位置 ", r.Key)).OnChange(func() { a.checkpointMap(f) })
							ui.TextInput(c, &r.Numerator).Width(70).Label(fmt.Sprint("拍号分子 ", r.Key)).OnChange(func() { a.checkpointMap(f) })
							ui.Text(c, "/")
							ui.TextInput(c, &r.Denominator).Width(70).Label(fmt.Sprint("拍号分母 ", r.Key)).OnChange(func() { a.checkpointMap(f) })
							ui.Button(c, "删除点").Disabled(r.Key == 0).OnClick(func() {
								f.snapshot.Draft.Meter = append(f.snapshot.Draft.Meter[:i], f.snapshot.Draft.Meter[i+1:]...)
								a.checkpointMap(f)
							})
						})
					}
				})
			})
			if f.err != "" {
				ui.Text(c, f.err).TextColor(c.Theme().Warning).MaxWidth(640)
			}
			if f.writes > 0 {
				ui.Text(c, "正在保存原始输入…")
			}
			if f.committed {
				ui.Text(c, "工程已保存，仅待清理草稿；不会重复应用。")
			}
			ui.Row(c).Gap(6).Justify(ui.End).Children(func() {
				ui.Button(c, "保留草稿并返回").Disabled(f.writes > 0 || f.saveFailed).OnClick(a.dismissMap)
				ui.Button(c, "重试保存输入").Disabled(f.writes > 0 || f.committed).OnClick(func() { a.checkpointMap(f) })
				ui.Button(c, "丢弃 / 清理草稿").Disabled(f.writes > 0).OnClick(func() { a.clearMap(f) })
				ui.PrimaryButton(c, "应用时间图").Disabled(f.writes > 0 || f.err != "" || f.committed || !a.writable()).OnClick(func() { a.applyTimeMap(f) })
			})
		})
	})
}
