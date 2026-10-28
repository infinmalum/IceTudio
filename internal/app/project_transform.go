package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// Agent-facing deterministic transform tools (docs/p3-agent-tools.md).
// project__select answers "which notes does this selection match"; it is a
// scoped read. project__transform compiles a declared transform into regular
// note.update ops and submits them through the same transaction path as
// project__edit — no new write path, per-note values computed by the program.
//
// Numeric bounds come from the project constants; schemas are built once and
// shared by the definitions below. Runtime validators in internal/project
// stay the final authority.

var projectTransformContract = sync.OnceValue(buildProjectTransformContract)

type projectTransformContractType struct {
	selectSchema    json.RawMessage
	transformSchema json.RawMessage
}

func buildProjectTransformContract() projectTransformContractType {
	bounded := func(min, max any) map[string]any {
		return map[string]any{"type": "integer", "minimum": min, "maximum": max}
	}
	strict := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		}
	}
	idItem := func() map[string]any { return map[string]any{"type": "string", "pattern": idPattern} }
	selection := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"trackIds":    map[string]any{"type": "array", "minItems": 1, "maxItems": project.MaxTracks, "items": idItem()},
			"clipIds":     map[string]any{"type": "array", "minItems": 1, "maxItems": project.MaxTracks, "items": idItem()},
			"startTick":   bounded(0, project.MaxTick),
			"endTick":     bounded(0, project.MaxTick),
			"pitchMin":    bounded(0, project.MaxMIDI),
			"pitchMax":    bounded(0, project.MaxMIDI),
			"channel":     bounded(0, project.MaxChannel),
			"velocityMin": bounded(project.MinVelocity, project.MaxMIDI),
			"velocityMax": bounded(project.MinVelocity, project.MaxMIDI),
		},
		"additionalProperties": false,
	}
	velocityOffset := -(project.MaxMIDI - 1)
	transform := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"transpose": strict(map[string]any{
				"semitones": bounded(-project.MaxMIDI, project.MaxMIDI),
			}, "semitones"),
			"quantize": strict(map[string]any{
				"unit":     map[string]any{"type": "string", "enum": []string{"1/4", "1/8", "1/16", "1/32", "1/8T", "1/16T"}},
				"strength": bounded(0, 100),
			}, "unit"),
			"velocity": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scale":  bounded(1, 300),
					"offset": bounded(velocityOffset, project.MaxMIDI-1),
					"set":    bounded(project.MinVelocity, project.MaxMIDI),
				},
				// "set 独占"由运行时校验（后端是最终权威，契约 §7）。
				"minProperties":        1,
				"additionalProperties": false,
			},
			"shift": strict(map[string]any{
				"ticks": bounded(-project.MaxTick, project.MaxTick),
			}, "ticks"),
		},
		// 恰好一种变换：一个属性且至少一个属性（比 oneOf 对 provider 更兼容）。
		"minProperties":        1,
		"maxProperties":        1,
		"additionalProperties": false,
	}
	selectArgs := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"projectId": map[string]any{"type": "string"},
			"selection": selection,
		},
		"required":             []string{"projectId"},
		"additionalProperties": false,
	}
	transformArgs := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"projectId":    map[string]any{"type": "string"},
			"opId":         map[string]any{"type": "string", "minLength": 1},
			"baseRevision": map[string]any{"type": "integer", "minimum": 0},
			"selection":    selection,
			"transform":    transform,
			"dryRun":       map[string]any{"type": "boolean"},
		},
		"required":             []string{"projectId", "opId", "baseRevision", "transform"},
		"additionalProperties": false,
	}
	must := func(v map[string]any) json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			panic(err) // 构建自纯 Go 值，不可能失败
		}
		return raw
	}
	return projectTransformContractType{
		selectSchema:    must(selectArgs),
		transformSchema: must(transformArgs),
	}
}

// maxReturnedNotes caps one select response; matched keeps the real count so
// the model can narrow windows or filters; there is no cursor pagination.
const maxReturnedNotes = 200

// maxSampleDeltas caps the dry-run sample list.
const maxSampleDeltas = 20

// projectSelectCall implements project__select inside a run. Read-only tasks
// may use it; frozen scopes intersect the selection with their allowlists.
func (t bookkeepingTools) projectSelectCall(ctx context.Context, raw string, target string, scope ProjectScope, hasScope bool) (string, error) {
	var args struct {
		ProjectID string             `json:"projectId"`
		Selection *project.Selection `json:"selection"`
	}
	if err := decodeRequiredOperation(raw, &args, "projectId"); err != nil {
		return "", err
	}
	if args.ProjectID != target {
		if hasScope {
			return "", errors.New("只能查询任务冻结的工程")
		}
		return "", errors.New("只能查询当前对话绑定的工程")
	}
	item, err := t.svc.GetProject(args.ProjectID)
	if err != nil {
		return "", err
	}
	sel := project.Selection{}
	if args.Selection != nil {
		sel = *args.Selection
	}
	if hasScope && (len(scope.Tracks) > 0 || len(scope.Clips) > 0) {
		impossible := false
		sel.TrackIDs, impossible = intersectIDs(sel.TrackIDs, scope.Tracks)
		if !impossible {
			sel.ClipIDs, impossible = intersectIDs(sel.ClipIDs, scope.Clips)
		}
		if impossible {
			payload, err := json.Marshal(map[string]any{
				"projectId": item.ID, "revision": item.Revision,
				"matched": 0, "returned": 0, "clips": []project.SelectedClip{},
			})
			if err != nil {
				return "", err
			}
			return string(payload), nil
		}
	}
	groups, stats, err := project.SelectNotes(&item.State, sel)
	if err != nil {
		return "", err
	}
	returned := 0
	clips := []project.SelectedClip{}
	for _, g := range groups {
		if returned >= maxReturnedNotes {
			break
		}
		kept := g
		if remaining := maxReturnedNotes - returned; len(g.Notes) > remaining {
			kept.Notes = g.Notes[:remaining]
		}
		returned += len(kept.Notes)
		clips = append(clips, kept)
	}
	payload, err := json.Marshal(map[string]any{
		"projectId": item.ID, "revision": item.Revision,
		"matched": stats.Matched, "returned": returned, "clips": clips,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// intersectIDs ANDs a selection list with an allowlist; an empty allowlist
// keeps the selection, an empty selection takes the allowlist. A non-empty
// selection that intersects nothing reports impossible — an empty result
// list would otherwise mean "everything" downstream.
func intersectIDs(sel, allow []string) ([]string, bool) {
	if len(allow) == 0 {
		return sel, false
	}
	if len(sel) == 0 {
		return append([]string{}, allow...), false
	}
	set := make(map[string]bool, len(allow))
	for _, id := range allow {
		set[id] = true
	}
	out := []string{}
	for _, id := range sel {
		if set[id] {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, false
}

// projectTransformCall implements project__transform inside a run. The
// transform is compiled against the current state, then either reported as a
// dry-run preview or submitted through submitProjectOps like project__edit.
func (t bookkeepingTools) projectTransformCall(ctx context.Context, raw string, target string, scope ProjectScope, hasScope, readOnly bool) (string, error) {
	var args struct {
		ProjectID    string                 `json:"projectId"`
		OpID         string                 `json:"opId"`
		BaseRevision *int64                 `json:"baseRevision"`
		Selection    *project.Selection     `json:"selection"`
		Transform    *project.TransformSpec `json:"transform"`
		DryRun       bool                   `json:"dryRun"`
	}
	if err := decodeRequiredOperation(raw, &args, "projectId", "opId", "baseRevision", "transform"); err != nil {
		return "", err
	}
	if args.ProjectID != target {
		if hasScope {
			return "", errors.New("只能编辑任务冻结的工程")
		}
		return "", errors.New("只能编辑当前对话绑定的工程")
	}
	if hasScope && scope.AccessMode != "write" {
		return "", errors.New("本任务为只读，不能编辑工程")
	}
	// Replay short-circuit BEFORE compiling: a retry of an already committed
	// transform whose notes were later removed or moved must return the
	// stored result, not a "matched nothing" compile error.
	if stored, ok, err := t.svc.GetProjectTransaction(args.ProjectID, args.OpID); err != nil {
		return "", err
	} else if ok {
		out, err := json.Marshal(stored)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	item, err := t.svc.GetProject(args.ProjectID)
	if err != nil {
		return "", err
	}
	sel := project.Selection{}
	if args.Selection != nil {
		sel = *args.Selection
	}
	ops, stats, err := compileScopedTransform(item, sel, *args.Transform, scope, hasScope)
	if err != nil {
		return "", err
	}
	if args.DryRun {
		payload, err := json.Marshal(map[string]any{
			"dryRun": true, "matched": stats.Matched, "changed": stats.Changed,
			"skippedPitch": stats.SkippedPitch, "skippedTime": stats.SkippedTime,
			"clips": stats.Clips, "sample": transformSample(ops),
		})
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}
	source := "agent:" + t.runID
	result, err := t.svc.submitProjectOps(ctx, args.ProjectID, args.OpID, source, *args.BaseRevision, ops)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", err
	}
	payload["matched"] = stats.Matched
	payload["changed"] = stats.Changed
	payload["skippedPitch"] = stats.SkippedPitch
	payload["skippedTime"] = stats.SkippedTime
	payload["clips"] = stats.Clips
	out, err = json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// transformSample renders up to 20 compiled deltas with only the fields the
// transform would change, so the model can sanity-check the effect without a
// full note dump.
func transformSample(ops []project.Op) []map[string]any {
	sample := []map[string]any{}
	for _, op := range ops {
		for _, d := range op.NoteDeltas {
			if len(sample) >= maxSampleDeltas {
				return sample
			}
			entry := map[string]any{"clipId": op.ClipID, "noteId": d.ID}
			if d.Start != nil {
				entry["start"] = *d.Start
			}
			if d.Duration != nil {
				entry["duration"] = *d.Duration
			}
			if d.Pitch != nil {
				entry["pitch"] = *d.Pitch
			}
			if d.Velocity != nil {
				entry["velocity"] = *d.Velocity
			}
			sample = append(sample, entry)
		}
	}
	return sample
}

// transformDefinitions is merged into projectDefinitions; schemas come from
// the shared one-time builder.
func transformDefinitions() []model.Tool {
	contract := projectTransformContract()
	specs := []struct {
		name, description string
		schema            json.RawMessage
	}{
		{
			"project__select",
			"选择本任务冻结工程范围内的音符：轨/片段 ID、片段内局部起音 tick 窗口（不叠加 clip.start）、音高、通道、力度条件全部同时满足（AND）。返回匹配音符（按片段分组）与 revision；matched 超过返回数时可缩小窗口或条件；没有游标，完全同条件超过 200 枚时无法通过本工具遍历全量。音符按起音计入窗口（project__query 的区域按重叠计入）。批量变换前先用本工具确认选择器；变换工具要求选择器至少限定一项。",
			contract.selectSchema,
		},
		{
			"project__transform",
			"对选择器命中的音符执行一种确定性变换并作为事务提交：transpose（移调）/quantize（量化到网格）/velocity（力度 scale+offset 或 set）/shift（平移），恰好一种，由程序按参数计算，不逐音符生成。用法：先 project__select 或 project__query 确认选择器与 revision；选择器必须至少限定 clipIds、trackIds 或 tick 窗口之一；opId 用于幂等重放；baseRevision 携带最近一次查询的 revision，用户已改动时整组拒绝，须重新查询。先 dryRun:true 预览将发生的变更（不提交、不改修订）。移调/平移越界的音符跳过并计入 skippedPitch/skippedTime；量化只移动起始位置；力度结果收敛到 1–127。",
			contract.transformSchema,
		},
	}
	out := []model.Tool{}
	for _, s := range specs {
		d := model.Tool{Type: "function"}
		d.Function.Name = s.name
		d.Function.Description = s.description
		d.Function.Parameters = s.schema
		out = append(out, d)
	}
	return out
}

// Shared by actual execution and local candidate preview; scope checks must
// cover even skipped/unchanged matches before returning any musical evidence.
func compileScopedTransform(item storage.Project, sel project.Selection, spec project.TransformSpec, scope ProjectScope, hasScope bool) ([]project.Op, project.TransformStats, error) {
	// Validate every matched clip before compiling, including notes that a
	// transform would skip or leave unchanged. No preview or compile error
	// may disclose out-of-scope note data or counts.
	bounded := len(sel.TrackIDs) > 0 || len(sel.ClipIDs) > 0 || sel.StartTick != nil || sel.EndTick != nil
	if bounded && hasScope && (len(scope.Tracks) > 0 || len(scope.Clips) > 0) {
		groups, _, err := project.SelectNotes(&item.State, sel)
		if err != nil {
			return nil, project.TransformStats{}, err
		}
		selected := make([]project.Op, 0, len(groups))
		for _, group := range groups {
			selected = append(selected, project.Op{Kind: "note.update", ClipID: group.ClipID})
		}
		if err := checkScopeOps(scope, selected, &item.State); err != nil {
			// A broad time selector may match clips whose IDs the caller
			// cannot query. Do not echo those identifiers in the denial.
			return nil, project.TransformStats{}, errors.New("选择器命中了任务范围外的片段；请限定在已授权的轨道或片段内")
		}
	}
	ops, stats, err := project.CompileTransform(&item.State, sel, spec)
	if err != nil {
		return nil, project.TransformStats{}, err
	}
	if hasScope && (len(scope.Tracks) > 0 || len(scope.Clips) > 0) {
		if err := checkScopeOps(scope, ops, &item.State); err != nil {
			return nil, project.TransformStats{}, err
		}
	}
	return ops, stats, nil
}
