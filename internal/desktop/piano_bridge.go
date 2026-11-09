package desktop

import (
	"encoding/json"
	"errors"
	"fmt"

	"icetudio/internal/app"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

type PianoEdit struct {
	SessionID  string       `json:"sessionId"`
	Generation uint64       `json:"generation"`
	ProjectID  string       `json:"projectId"`
	Revision   int64        `json:"revision"`
	TrackID    string       `json:"trackId"`
	ClipID     string       `json:"clipId"`
	OpID       string       `json:"opId"`
	Kind       string       `json:"kind"`
	Ops        []project.Op `json:"ops"`
}
type PianoReceipt struct {
	OpID     string `json:"opId"`
	Revision int64  `json:"revision"`
}
type PianoCommand struct {
	Theme            string   `json:"theme,omitempty"`
	QuantizeKey      string   `json:"quantizeKey,omitempty"`
	DuplicateKey     string   `json:"duplicateKey,omitempty"`
	Step             float64  `json:"step,omitempty"`
	Tick             float64  `json:"tick,omitempty"`
	Playing          bool     `json:"playing"`
	Follow           string   `json:"follow,omitempty"`
	Generation       uint64   `json:"generation"`
	Name             string   `json:"name"`
	Unit             string   `json:"unit,omitempty"`
	Value            int      `json:"value,omitempty"`
	SelectionVersion uint64   `json:"selectionVersion"`
	NoteIDs          []string `json:"noteIds"`
}

func (p *PianoPreview) Attach() PianoContext {
	p.mu.Lock()
	p.session = project.NewID("editor")
	p.readyGeneration = 0
	p.mu.Unlock()
	return p.Context()
}
func (p *PianoPreview) ContextReady(session string, generation uint64) error {
	p.mu.Lock()
	if session == "" || session != p.session || generation != p.value.Generation {
		p.mu.Unlock()
		return errors.New("卷帘上下文已过期")
	}
	changed := p.readyGeneration != generation
	p.readyGeneration = generation
	p.mu.Unlock()
	if changed && p.notify != nil {
		p.notify()
	}
	if changed && p.ready != nil {
		p.ready()
	}
	return nil
}

func (p *PianoPreview) freeze(value bool) { p.mu.Lock(); p.frozen = value; p.mu.Unlock() }
func (p *PianoPreview) Submit(req PianoEdit) (PianoReceipt, error) {
	p.mu.Lock()
	valid := req.SessionID != "" && req.SessionID == p.session && req.Generation == p.value.Generation && p.readyGeneration == req.Generation && !p.frozen && req.ProjectID == p.value.ProjectID && req.TrackID == p.value.TrackID && req.ClipID == p.value.ClipID && req.Revision == p.value.Revision
	clip := p.value.Clip
	p.mu.Unlock()
	if !valid || p.edits == nil {
		return PianoReceipt{}, errors.New("卷帘目标已变化或处于关闭检查中；本次修改未发送")
	}
	if len(req.OpID) < 4 || len(req.OpID) > 128 || req.Kind != "edit" && req.Kind != "undo" && req.Kind != "redo" {
		return PianoReceipt{}, errors.New("无效的卷帘操作标识或类型")
	}
	if req.Kind != "edit" && len(req.Ops) > 0 {
		return PianoReceipt{}, errors.New("撤销/重做不能夹带编辑操作")
	}
	if req.Kind == "edit" {
		if err := validatePianoOps(req); err != nil {
			return PianoReceipt{}, err
		}
		if err := validatePianoControlTarget(req, clip); err != nil {
			return PianoReceipt{}, err
		}
	}
	result, err := p.edits.ExecutePending(EditTarget{req.ProjectID, req.Revision}, req.Kind, req.Ops, req.OpID)
	if p.changed != nil {
		p.changed(req.ProjectID)
	}
	if err != nil {
		return PianoReceipt{}, err
	}
	return PianoReceipt{result.OpID, result.Revision}, nil
}
func validatePianoOps(req PianoEdit) error {
	if len(req.Ops) == 0 || len(req.Ops) > 2 {
		return errors.New("卷帘仅接受一组音符操作及可选片段扩长")
	}
	for _, op := range req.Ops {
		if op.TrackID != req.TrackID || op.ClipID != req.ClipID {
			return errors.New("卷帘不能修改其他轨道或片段")
		}
		payload := ""
		switch op.Kind {
		case "note.add":
			payload = "notes"
		case "note.update":
			payload = "noteDeltas"
		case "note.remove":
			payload = "noteIds"
		case "control.add", "control.update":
			payload = "events"
		case "control.remove":
			payload = "eventIds"
		case "clip.update":
			payload = "clipPatch"
		default:
			return errors.New("卷帘不允许该操作类型")
		}
		raw, err := json.Marshal(op)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for key := range fields {
			if key != "kind" && key != "trackId" && key != "clipId" && key != payload {
				return fmt.Errorf("卷帘不允许字段 %s", key)
			}
		}
		if op.Kind == "clip.update" {
			patch := op.ClipPatch
			if patch == nil || patch.Length == nil || patch.Name != nil || patch.Color != nil || patch.Start != nil {
				return errors.New("卷帘只能调整所选片段长度")
			}
		}
	}
	return nil
}
func validatePianoControlTarget(req PianoEdit, clip project.Clip) error {
	isEditable := func(kind string) bool { return kind == project.KindCC || kind == project.KindPitchBend }
	has := func(id string) bool {
		for _, e := range clip.Controls {
			if e.ID == id && isEditable(e.Kind) {
				return true
			}
		}
		return false
	}
	for _, op := range req.Ops {
		switch op.Kind {
		case "control.add", "control.update":
			if len(req.Ops) != 1 || len(op.Events) < 1 || len(op.Events) > project.MaxControlEditPoints {
				return errors.New("卷帘一次可提交 1–128 个控制点")
			}
			for _, e := range op.Events {
				if !isEditable(e.Kind) || (op.Kind == "control.update" && !has(e.ID)) {
					return errors.New("卷帘只能修改所选片段的 CC 或弯音点")
				}
			}
		case "control.remove":
			if len(req.Ops) != 1 || len(op.EventIDs) < 1 || len(op.EventIDs) > project.MaxControlEditPoints {
				return errors.New("卷帘一次可删除 1–128 个控制点")
			}
			seen := map[string]bool{}
			for _, id := range op.EventIDs {
				if !has(id) || seen[id] {
					return errors.New("卷帘只能删除所选片段的 CC 或弯音点")
				}
				seen[id] = true
			}
		}
	}
	return nil
}

func (p *PianoPreview) Acknowledge(session, id, opID string) error {
	p.mu.Lock()
	valid := session != "" && session == p.session
	ackFault := p.ackFault
	p.mu.Unlock()
	if !valid || p.edits == nil {
		return errors.New("卷帘会话已失效，请从恢复入口核对原操作")
	}
	if ackFault {
		return errors.New("test: acknowledgement unavailable")
	}
	err := p.edits.Acknowledge(id, opID)
	if p.changed != nil {
		p.changed(id)
	}
	return err
}
func (p *PianoPreview) GetMusicInputs() (*app.MusicInputsRecord, error) {
	if p.service == nil {
		return nil, nil
	}
	if p.drafts != nil {
		return p.drafts.pianoRead()
	}
	return p.service.GetMusicInputs()
}
func (p *PianoPreview) SaveMusicInputs(expected, token, payload string) error {
	if p.service == nil {
		return errors.New("视觉样例不保存草稿")
	}
	if p.drafts != nil {
		return p.drafts.pianoSave(expected, token, payload)
	}
	return p.service.SaveMusicInputs(expected, token, payload)
}
func (p *PianoPreview) PrepareNoteTransform(id string, rev int64, clip string, ids []string, spec project.TransformSpec) ([]project.Op, error) {
	value := p.Context()
	if p.service == nil || !value.Writable || id != value.ProjectID || rev != value.Revision || clip != value.ClipID {
		return nil, errors.New("变换目标已过期或当前不可编辑")
	}
	ops, err := p.service.PrepareNoteTransform(id, rev, clip, ids, spec)
	if err != nil {
		return nil, err
	}
	// The core compiler addresses globally unique clips and omits trackId.
	// Add the verified UI scope to the bridge payload; Submit remains strict.
	for i := range ops {
		if ops[i].ClipID != value.ClipID || ops[i].TrackID != "" && ops[i].TrackID != value.TrackID {
			return nil, errors.New("变换超出所选片段")
		}
		ops[i].TrackID = value.TrackID
	}
	return ops, nil
}
func (p *PianoPreview) Audition(req playback.AuditionRequest) error {
	value := p.Context()
	if p.service == nil {
		return errors.New("视觉样例不播放音频")
	}
	bank := 0
	if value.Track.Channel == 9 {
		bank = 128
	}
	if req.Program != value.Track.Program || req.Bank != bank {
		return errors.New("音色上下文已变化")
	}
	if value.Sampled {
		return p.service.AuditionProjectSampler(value.ProjectID, value.Revision, value.TrackID, req)
	}
	return p.service.AuditionInstrument(req)
}

func (p *PianoPreview) Selection(session string, generation, version uint64, ids []string, interactive bool) error {
	p.mu.Lock()
	updated := false
	defer func() {
		p.mu.Unlock()
		if updated && p.invalidate != nil {
			p.invalidate()
		}
	}()
	if session != p.session || generation != p.value.Generation || version <= p.selectionVersion {
		return errors.New("选区报告已过期")
	}
	if len(ids) > project.MaxNotes {
		return errors.New("选区超过上限")
	}
	valid := make(map[string]bool, len(p.value.Clip.Notes))
	for _, n := range p.value.Clip.Notes {
		valid[n.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !valid[id] || seen[id] {
			return errors.New("选区包含无效音符")
		}
		seen[id] = true
	}
	p.interactive = interactive
	p.selectionVersion = version
	p.selectionIDs = append([]string(nil), ids...)
	updated = true
	return nil
}
func (p *PianoPreview) command(name string, value int) PianoCommand {
	p.mu.Lock()
	defer p.mu.Unlock()
	command := PianoCommand{Generation: p.value.Generation, Name: name, Value: value, SelectionVersion: p.selectionVersion}
	switch name {
	case "copy", "delete", "transpose", "quantize", "duplicate", "velocity":
		command.NoteIDs = append([]string{}, p.selectionIDs...)
	}
	return command
}

// toolsReady additionally waits for the actual page to leave its busy render.
// Backend writability alone can precede React's installation of that context.
func (p *PianoPreview) toolsReady() bool {
	p.mu.Lock()
	ready := p.interactive
	p.mu.Unlock()
	return ready && p.Context().Writable
}

// Navigate changes only transport/view state. It cannot submit music edits.
func (p *PianoPreview) Navigate(session string, generation uint64, name string, tick, end int64) error {
	p.mu.Lock()
	ctx := p.value
	handler := p.navigation
	valid := session != "" && session == p.session && generation == ctx.Generation && !p.frozen
	p.mu.Unlock()
	if !valid {
		return errors.New("导航上下文已过期")
	}
	if tick < 0 || end < 0 || tick > project.MaxTick*2 || end > project.MaxTick*2 {
		return errors.New("位置超出范围")
	}
	switch name {
	case "toggle", "home", "seek", "loop", "manual":
	default:
		return errors.New("不支持的导航命令")
	}
	if handler != nil {
		handler(ctx, name, tick, end)
	}
	return nil
}
