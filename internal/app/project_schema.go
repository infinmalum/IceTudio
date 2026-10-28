package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"icetudio/internal/project"
)

// The agent-facing project__edit schema (docs/p3-agent-edit-schema.md). It
// mirrors the authoritative op contract in internal/project/ops.go as a
// discriminated oneOf on `kind`; the backend validator stays the final
// authority for cross-object rules the schema cannot express (clip fit,
// tick-0 anchors, batch-wide ID uniqueness, resource ceilings).
//
// Inverse-only payloads (Op.InvClips/InvNotes/InvControls/InvRaw) are filled
// only by the transaction engine and rejected from clients (P1 §10); they are
// deliberately absent from the schema AND from the decode whitelist below.
//
// Numeric bounds come from the exported project constants so the validator
// and this schema cannot drift apart. The decode whitelist is derived from
// the built schema itself, so a field declared here is exactly the field the
// boundary accepts — nothing more, nothing less.

var projectEditContract = sync.OnceValue(buildProjectEditContract)

type projectEditContractType struct {
	schema json.RawMessage
	kinds  []string            // supported op kinds, sorted
	fields map[string][]string // kind → allowed top-level fields, sorted
}

// idPattern mirrors project.ValidID: [a-z0-9_], 1–64 chars.
const idPattern = `^[a-z0-9_]{1,64}$`

func buildProjectEditContract() projectEditContractType {
	id := func() map[string]any { return map[string]any{"type": "string", "pattern": idPattern} }
	color := func() map[string]any {
		values := append([]string{""}, project.Palette...)
		return map[string]any{"type": "string", "enum": values} // "" = 未设置（P1 §11）
	}
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
	// idField exists so clients MAY pin stable ids; the service assigns one
	// otherwise (and reports it in the result).
	const idKey, nameKey, colorKey = "id", "name", "color"

	trackSpec := strict(map[string]any{
		idKey: id(), nameKey: map[string]any{"type": "string"}, colorKey: color(),
		"channel": bounded(0, project.MaxChannel), "program": bounded(0, project.MaxMIDI),
		"muted": map[string]any{"type": "boolean"}, "solo": map[string]any{"type": "boolean"},
	}, "channel", "program")
	trackPatch := map[string]any{
		"type": "object",
		"properties": map[string]any{
			nameKey: map[string]any{"type": "string"}, colorKey: color(),
			"channel": bounded(0, project.MaxChannel), "program": bounded(0, project.MaxMIDI),
			"muted": map[string]any{"type": "boolean"}, "solo": map[string]any{"type": "boolean"},
		},
		"minProperties":        1, // 后端拒绝全空补丁；schema 同步表达
		"additionalProperties": false,
	}
	clipSpec := strict(map[string]any{
		idKey: id(), "trackId": map[string]any{"type": "string"}, nameKey: map[string]any{"type": "string"},
		colorKey: color(), "start": bounded(0, project.MaxTick), "length": bounded(1, project.MaxTick),
	}, "trackId", "start", "length")
	clipPatch := map[string]any{
		"type": "object",
		"properties": map[string]any{
			nameKey: map[string]any{"type": "string"}, colorKey: color(),
			"start": bounded(0, project.MaxTick), "length": bounded(1, project.MaxTick),
		},
		"minProperties":        1,
		"additionalProperties": false,
	}
	noteSpec := strict(map[string]any{
		idKey: id(), "start": bounded(0, project.MaxTick), "duration": bounded(1, project.MaxTick),
		"pitch": bounded(0, project.MaxMIDI), "velocity": bounded(project.MinVelocity, project.MaxMIDI),
		"channel": bounded(0, project.MaxChannel),
	}, "start", "duration", "pitch", "velocity", "channel")
	noteDelta := strict(map[string]any{
		"id": id(), "start": bounded(0, project.MaxTick), "duration": bounded(1, project.MaxTick),
		"pitch": bounded(0, project.MaxMIDI), "velocity": bounded(project.MinVelocity, project.MaxMIDI),
	}, "id")
	// 通道不可经 note.update 修改（契约 §3）；schema 不声明 channel。

	tempoItem := strict(map[string]any{
		"tick": bounded(0, project.MaxTick), "usPerQuarter": bounded(1, project.MaxUSPerQuarter),
	}, "tick", "usPerQuarter")
	meterItem := strict(map[string]any{
		"tick":        bounded(0, project.MaxTick),
		"numerator":   bounded(1, project.MaxNumerator),
		"denominator": map[string]any{"type": "integer", "enum": meterDenominators(project.MaxMeterDenominator)},
	}, "tick", "numerator", "denominator")
	idList := func() map[string]any {
		return map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}
	}

	// control.add events: same-shape oneOf on the event kind. `value` is
	// required by every kind that carries one; `controller` only by cc.
	eventBase := func(kind string, required []string, extra map[string]any) map[string]any {
		properties := map[string]any{
			idKey: id(), "tick": bounded(0, project.MaxTick),
			"kind": map[string]any{"const": kind}, "channel": bounded(0, project.MaxChannel),
		}
		for k, v := range extra {
			properties[k] = v
		}
		return strict(properties, append([]string{"tick", "kind", "channel"}, required...)...)
	}
	sevenBit := bounded(0, project.MaxMIDI)
	eventBranches := []map[string]any{
		eventBase(project.KindCC, []string{"controller", "value"},
			map[string]any{"controller": sevenBit, "value": sevenBit}),
		eventBase(project.KindPitchBend, []string{"value"},
			map[string]any{"value": bounded(0, project.MaxPitchBend)}),
		eventBase(project.KindProgram, []string{"program"},
			map[string]any{"program": sevenBit}),
		eventBase(project.KindChannelAftertouch, []string{"value"},
			map[string]any{"value": sevenBit}),
		eventBase(project.KindPolyAftertouch, []string{"note", "value"},
			map[string]any{"note": sevenBit, "value": sevenBit}),
	}

	branch := func(kind string, required []string, extra map[string]any) map[string]any {
		properties := map[string]any{"kind": map[string]any{"const": kind}}
		for k, v := range extra {
			properties[k] = v
		}
		return strict(properties, append([]string{"kind"}, required...)...)
	}
	opBranches := []map[string]any{
		branch("project.rename", []string{"title"},
			map[string]any{"title": map[string]any{"type": "string", "maxLength": project.MaxTitleRunes}}),
		branch("tempo.set", []string{"tempo"},
			map[string]any{"tempo": map[string]any{"type": "array", "minItems": 1, "items": tempoItem}}),
		branch("meter.set", []string{"meter"},
			map[string]any{"meter": map[string]any{"type": "array", "minItems": 1, "items": meterItem}}),
		branch("track.add", []string{"track"}, map[string]any{"track": trackSpec}),
		branch("track.update", []string{"trackId", "trackPatch"},
			map[string]any{"trackId": map[string]any{"type": "string"}, "trackPatch": trackPatch}),
		branch("track.remove", []string{"trackId"},
			map[string]any{"trackId": map[string]any{"type": "string"}}),
		branch("clip.add", []string{"clip"}, map[string]any{"clip": clipSpec}),
		branch("clip.update", []string{"clipId", "clipPatch"},
			map[string]any{"clipId": map[string]any{"type": "string"}, "clipPatch": clipPatch}),
		branch("clip.remove", []string{"clipId"},
			map[string]any{"clipId": map[string]any{"type": "string"}}),
		branch("note.add", []string{"clipId", "notes"},
			map[string]any{"clipId": map[string]any{"type": "string"},
				"notes": map[string]any{"type": "array", "minItems": 1, "items": noteSpec}}),
		branch("note.update", []string{"clipId", "noteDeltas"},
			map[string]any{"clipId": map[string]any{"type": "string"},
				"noteDeltas": map[string]any{"type": "array", "minItems": 1, "items": noteDelta}}),
		branch("note.remove", []string{"clipId", "noteIds"},
			map[string]any{"clipId": map[string]any{"type": "string"}, "noteIds": idList()}),
		branch("control.add", []string{"clipId", "events"},
			map[string]any{"clipId": map[string]any{"type": "string"},
				"events": map[string]any{"type": "array", "minItems": 1,
					"items": map[string]any{"oneOf": eventBranches}}}),
		branch("control.remove", []string{"clipId", "eventIds"},
			map[string]any{"clipId": map[string]any{"type": "string"}, "eventIds": idList()}),
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"projectId":    map[string]any{"type": "string"},
			"opId":         map[string]any{"type": "string"},
			"baseRevision": map[string]any{"type": "integer", "minimum": 0},
			"ops": map[string]any{"type": "array", "minItems": 1,
				"items": map[string]any{"oneOf": opBranches}},
		},
		"required":             []string{"projectId", "opId", "baseRevision", "ops"},
		"additionalProperties": false,
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err) // 构建自纯 Go 值，不可能失败
	}

	contract := projectEditContractType{schema: raw, fields: map[string][]string{}}
	for _, b := range opBranches {
		kind := b["properties"].(map[string]any)["kind"].(map[string]any)["const"].(string)
		contract.kinds = append(contract.kinds, kind)
		properties := b["properties"].(map[string]any)
		fields := make([]string, 0, len(properties))
		for field := range properties {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		contract.fields[kind] = fields
	}
	sort.Strings(contract.kinds)
	return contract
}

func meterDenominators(max int) []int {
	out := []int{}
	for d := 1; d <= max; d *= 2 {
		out = append(out, d)
	}
	return out
}

// checkProjectOpFields enforces the decode boundary for project__edit ops:
// every element must carry a supported kind and only fields that kind's
// schema branch declares. This catches misspelled payloads with the legal
// field list in the message (plain json decoding would produce a generic
// error or, for struct-known inverse-only fields, defer to the backend).
func checkProjectOpFields(raw string) error {
	var probe struct {
		Ops []json.RawMessage `json:"ops"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil // 形状问题交给 decodeRequiredOperation 报规范错误
	}
	contract := projectEditContract()
	for _, elem := range probe.Ops {
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(elem, &head); err != nil {
			return fmt.Errorf("操作元素必须是对象：%w", err)
		}
		if head.Kind == "" {
			return fmt.Errorf("操作缺少 kind；支持的操作类型：%s", strings.Join(contract.kinds, ", "))
		}
		allowed, ok := contract.fields[head.Kind]
		if !ok {
			return fmt.Errorf("不支持的操作类型 %q；支持的操作类型：%s", head.Kind, strings.Join(contract.kinds, ", "))
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(elem, &fields); err != nil {
			return fmt.Errorf("操作元素必须是对象：%w", err)
		}
		for field := range fields {
			if !slices.Contains(allowed, field) {
				return fmt.Errorf("操作 %s 不支持字段 %q；该操作的合法字段：%s", head.Kind, field, strings.Join(allowed, ", "))
			}
		}
	}
	return nil
}
