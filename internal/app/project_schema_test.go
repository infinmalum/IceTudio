package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// ---- Test-only JSON Schema validator ---------------------------------------
//
// Covers exactly the keyword subset projectEditContract emits (object /
// integer / string / array / boolean, properties, required,
// additionalProperties:false, minProperties, enum, const, minimum, maximum,
// maxLength, pattern, minItems, items, oneOf). Validating the re-parsed
// schema string keeps the checks pointed at the shipped artifact.

func schemaValidate(t *testing.T, schema map[string]any, raw string) error {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return fmt.Errorf("载荷不是合法 JSON：%w", err)
	}
	return schemaCheck(schema, value, "")
}

func schemaCheck(schema map[string]any, value any, path string) error {
	label := path
	if label == "" {
		label = "根"
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		matches := 0
		var firstErr error
		// 判别失败时优先报告 kind 相同分支的错误，避免被首个分支的
		// 无关缺失字段淹没。
		valueKind, _ := value.(map[string]any)["kind"].(string)
		for _, branch := range oneOf {
			branchMap := branch.(map[string]any)
			err := schemaCheck(branchMap, value, path)
			if err == nil {
				matches++
				continue
			}
			if firstErr == nil || branchConst(branchMap) == valueKind {
				firstErr = err
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s 匹配 oneOf 分支数 %d≠1（首错：%v）", label, matches, firstErr)
		}
		return nil
	}
	wantType, _ := schema["type"].(string)
	switch wantType {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s 应为 object，实际 %T", label, value)
		}
		if additional, ok := schema["additionalProperties"]; ok == true && additional == false {
			properties, _ := schema["properties"].(map[string]any)
			for key := range obj {
				if _, declared := properties[key]; !declared {
					return fmt.Errorf("%s 含未声明字段 %q", label, key)
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, key := range toStrings(schema["required"]) {
			if _, present := obj[key]; !present {
				return fmt.Errorf("%s 缺少必填字段 %q", label, key)
			}
		}
		if min, ok := schema["minProperties"].(float64); ok && float64(len(obj)) < min {
			return fmt.Errorf("%s 至少要有 %v 个字段", label, min)
		}
		if max, ok := schema["maxProperties"].(float64); ok && float64(len(obj)) > max {
			return fmt.Errorf("%s 至多要有 %v 个字段", label, max)
		}
		for key, sub := range properties {
			if v, present := obj[key]; present {
				if err := schemaCheck(sub.(map[string]any), v, path+"."+key); err != nil {
					return err
				}
			}
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("%s 应为整数，实际 %v", label, value)
		}
		if min, ok := schema["minimum"].(float64); ok && number < min {
			return fmt.Errorf("%s=%v 低于下限 %v", label, number, min)
		}
		if max, ok := schema["maximum"].(float64); ok && number > max {
			return fmt.Errorf("%s=%v 超出上限 %v", label, number, max)
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s 应为 string，实际 %T", label, value)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			ok, err := regexp.MatchString(pattern, s)
			if err != nil {
				return fmt.Errorf("模式 %q 不可用：%w", pattern, err)
			}
			if !ok {
				return fmt.Errorf("%s=%q 不匹配模式 %q", label, s, pattern)
			}
		}
		if max, ok := schema["maxLength"].(float64); ok && len([]rune(s)) > int(max) {
			return fmt.Errorf("%s 长度超过 %v", label, max)
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s 应为 array，实际 %T", label, value)
		}
		if min, ok := schema["minItems"].(float64); ok && len(items) < int(min) {
			return fmt.Errorf("%s 至少 %v 项", label, min)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range items {
				if err := schemaCheck(itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s 应为 boolean，实际 %T", label, value)
		}
	}
	if allowed, ok := schema["enum"]; ok {
		for _, candidate := range allowed.([]any) {
			if reflect.DeepEqual(candidate, value) {
				return nil
			}
		}
		return fmt.Errorf("%s=%v 不在枚举内", label, value)
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("%s 应为常量 %v，实际 %v", label, constant, value)
	}
	return nil
}

func toStrings(v any) []string {
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, item.(string))
		}
		return out
	}
	return nil
}

func branchConst(branch map[string]any) string {
	properties, _ := branch["properties"].(map[string]any)
	kindSchema, _ := properties["kind"].(map[string]any)
	name, _ := kindSchema["const"].(string)
	return name
}

// toNumbers normalizes JSON numbers (float64 after a schema round trip) and
// Go ints (meterDenominators) into one comparable shape.
func toNumbers(v any) []float64 {
	var raw []any
	switch list := v.(type) {
	case []any:
		raw = list
	case []int:
		out := make([]float64, 0, len(list))
		for _, n := range list {
			out = append(out, float64(n))
		}
		return out
	default:
		return nil
	}
	out := make([]float64, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(float64))
	}
	return out
}

// ---- Helpers ---------------------------------------------------------------

func editSchema(t *testing.T) map[string]any {
	t.Helper()
	for _, d := range projectDefinitions() {
		if d.Function.Name != "project__edit" {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(d.Function.Parameters, &schema); err != nil {
			t.Fatalf("schema 不可解析：%v", err)
		}
		return schema
	}
	t.Fatal("缺少 project__edit 定义")
	return nil
}

func opBranches(t *testing.T, schema map[string]any) map[string]map[string]any {
	t.Helper()
	items := schema["properties"].(map[string]any)["ops"].(map[string]any)["items"].(map[string]any)
	branches := map[string]map[string]any{}
	for _, raw := range items["oneOf"].([]any) {
		b := raw.(map[string]any)
		kind := b["properties"].(map[string]any)["kind"].(map[string]any)["const"].(string)
		if _, dup := branches[kind]; dup {
			t.Fatalf("kind %q 出现多个分支", kind)
		}
		branches[kind] = b
	}
	return branches
}

// walkKeys visits every object key in the schema tree.
func walkKeys(node any, visit func(key string) bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, sub := range v {
			if visit(k) {
				return
			}
			walkKeys(sub, visit)
		}
	case []any:
		for _, sub := range v {
			walkKeys(sub, visit)
		}
	}
}

// ---- Static schema consistency (契约 §6) -----------------------------------

func TestProjectEditSchemaShapeMatchesContract(t *testing.T) {
	schema := editSchema(t)
	top, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("顶层缺少 properties")
	}
	wantTop := []string{"projectId", "opId", "baseRevision", "ops"}
	if keys := toKeySet(top); !reflect.DeepEqual(keys, toKeySetFromList(wantTop)) {
		t.Fatalf("顶层字段 %v，期望 %v", keys, wantTop)
	}
	if schema["additionalProperties"] != false {
		t.Fatal("顶层必须 additionalProperties:false")
	}
	if got := toStrings(schema["required"]); !reflect.DeepEqual(got, wantTop) {
		t.Fatalf("顶层必填 %v，期望 %v", got, wantTop)
	}

	branches := opBranches(t, schema)
	wantKinds := []string{
		"clip.add", "clip.remove", "clip.update", "control.add", "control.remove",
		"meter.set", "note.add", "note.remove", "note.update", "project.rename",
		"tempo.set", "track.add", "track.remove", "track.update",
	}
	if len(branches) != len(wantKinds) {
		t.Fatalf("分支数 %d，期望 %d", len(branches), len(wantKinds))
	}
	for _, kind := range wantKinds {
		if branches[kind] == nil {
			t.Fatalf("缺少 kind %q 的分支", kind)
		}
	}

	// 每个 kind 的顶层字段集与必填集（契约 §3 表）。
	wantFields := map[string][2][]string{ // [0]=properties 键 [1]=required
		"project.rename": {[]string{"kind", "title"}, []string{"kind", "title"}},
		"tempo.set":      {[]string{"kind", "tempo"}, []string{"kind", "tempo"}},
		"meter.set":      {[]string{"kind", "meter"}, []string{"kind", "meter"}},
		"track.add":      {[]string{"kind", "track"}, []string{"kind", "track"}},
		"track.update":   {[]string{"kind", "trackId", "trackPatch"}, []string{"kind", "trackId", "trackPatch"}},
		"track.remove":   {[]string{"kind", "trackId"}, []string{"kind", "trackId"}},
		"clip.add":       {[]string{"kind", "clip"}, []string{"kind", "clip"}},
		"clip.update":    {[]string{"kind", "clipId", "clipPatch"}, []string{"kind", "clipId", "clipPatch"}},
		"clip.remove":    {[]string{"kind", "clipId"}, []string{"kind", "clipId"}},
		"note.add":       {[]string{"kind", "clipId", "notes"}, []string{"kind", "clipId", "notes"}},
		"note.update":    {[]string{"kind", "clipId", "noteDeltas"}, []string{"kind", "clipId", "noteDeltas"}},
		"note.remove":    {[]string{"kind", "clipId", "noteIds"}, []string{"kind", "clipId", "noteIds"}},
		"control.add":    {[]string{"kind", "clipId", "events"}, []string{"kind", "clipId", "events"}},
		"control.remove": {[]string{"kind", "clipId", "eventIds"}, []string{"kind", "clipId", "eventIds"}},
	}
	for kind, want := range wantFields {
		props := toKeySet(branches[kind]["properties"].(map[string]any))
		if !reflect.DeepEqual(props, toKeySetFromList(want[0])) {
			t.Fatalf("%s 字段 %v，期望 %v", kind, props, want[0])
		}
		if got := toStrings(branches[kind]["required"]); !reflect.DeepEqual(got, want[1]) {
			t.Fatalf("%s 必填 %v，期望 %v", kind, got, want[1])
		}
		if branches[kind]["additionalProperties"] != false {
			t.Fatalf("%s 分支必须 additionalProperties:false", kind)
		}
	}

	// 与 project 包常量的单一来源断言（数值界引用 ops.go 使用的同一常量）。
	// 反例方向由 TestProjectEditSchemaAndBoundaryRejections 行为覆盖。
	branchProps := func(kind string) map[string]any {
		return branches[kind]["properties"].(map[string]any)
	}
	assertBound(t, branchProps("project.rename")["title"], float64(project.MaxTitleRunes), "maxLength")
	tempoItem := branchProps("tempo.set")["tempo"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	assertBound(t, tempoItem["tick"], float64(project.MaxTick), "maximum")
	assertBound(t, tempoItem["usPerQuarter"], float64(project.MaxUSPerQuarter), "maximum")
	meterItem := branchProps("meter.set")["meter"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	assertBound(t, meterItem["numerator"], float64(project.MaxNumerator), "maximum")
	if got := toNumbers(meterItem["denominator"].(map[string]any)["enum"]); !reflect.DeepEqual(got,
		toNumbers(meterDenominators(project.MaxMeterDenominator))) {
		t.Fatalf("分母枚举漂移：%v", meterItem["denominator"])
	}
	noteItem := branchProps("note.add")["notes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	assertBound(t, noteItem["pitch"], float64(project.MaxMIDI), "maximum")
	assertBound(t, noteItem["velocity"], float64(project.MaxMIDI), "maximum")
	if min := noteItem["velocity"].(map[string]any)["minimum"].(float64); min != float64(project.MinVelocity) {
		t.Fatalf("力度下限 %v ≠ MinVelocity", min)
	}
	assertBound(t, noteItem["channel"], float64(project.MaxChannel), "maximum")
	deltaItem := branchProps("note.update")["noteDeltas"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, hasChannel := deltaItem["channel"]; hasChannel {
		t.Fatal("note.update 不得声明 channel（通道不可经 delta 修改）")
	}
	// 颜色枚举直接引用 project.Palette；事件 kind 枚举引用 Kind 常量。
	colorEnum := append([]string{""}, project.Palette...)
	trackSpec := branchProps("track.add")["track"].(map[string]any)["properties"].(map[string]any)
	if got := toStrings(trackSpec["color"].(map[string]any)["enum"]); !reflect.DeepEqual(got, colorEnum) {
		t.Fatalf("track 颜色枚举 %v，期望调色板 %v", got, colorEnum)
	}
	events := branchProps("control.add")["events"].(map[string]any)["items"].(map[string]any)["oneOf"].([]any)
	eventKinds := map[string]bool{}
	for _, raw := range events {
		props := raw.(map[string]any)["properties"].(map[string]any)
		kind := props["kind"].(map[string]any)["const"].(string)
		eventKinds[kind] = true
	}
	for _, want := range []string{project.KindCC, project.KindPitchBend, project.KindProgram, project.KindChannelAftertouch, project.KindPolyAftertouch} {
		if !eventKinds[want] {
			t.Fatalf("control.add 事件分支缺少 kind %q", want)
		}
	}
	// ID 模式与 ValidID 行为一致（镜像断言）。
	pattern := trackSpec["id"].(map[string]any)["pattern"].(string)
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("ID 模式不可编译：%v", err)
	}
	for _, id := range []string{"a", "abc_123", strings.Repeat("x", 64), "", "A", "a-b", strings.Repeat("x", 65), "音"} {
		if compiled.MatchString(id) != project.ValidID(id) {
			t.Fatalf("ID 模式与 ValidID 行为不一致：%q", id)
		}
	}
}

func assertBound(t *testing.T, schema any, want any, key string) {
	t.Helper()
	m, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("%s 约束载体不是对象：%T", key, schema)
	}
	if got := m[key]; !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v，期望 %v", key, got, want)
	}
}

func toKeySet(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}
func toKeySetFromList(list []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range list {
		out[k] = true
	}
	return out
}

func TestProjectEditSchemaOmitsInverseOnlyFields(t *testing.T) {
	schema := editSchema(t)
	leaked := []string{}
	walkKeys(schema, func(key string) bool {
		switch key {
		case "invClips", "invNotes", "invControls", "invRaw":
			leaked = append(leaked, key)
			return true
		}
		return false
	})
	if len(leaked) != 0 {
		t.Fatalf("schema 泄漏 inverse-only 字段：%v", leaked)
	}
	// 解码白名单同样不含（checkProjectOpFields 从 schema 分支派生）。
	contract := projectEditContract()
	for _, kind := range contract.kinds {
		for _, field := range contract.fields[kind] {
			switch field {
			case "invClips", "invNotes", "invControls", "invRaw":
				t.Fatalf("%s 白名单含 inverse-only 字段 %q", kind, field)
			}
		}
	}
}

// ---- Positive round trips: schema-valid → backend accepts (契约 §6) --------

func TestProjectEditSchemaPositiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, nil, nil)
	proj, err := svc.CreateProject("Schema 往返", 480)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(ctx, storage.DefaultConfigID, "", "对话")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(conv.ID, proj.ID); err != nil {
		t.Fatal(err)
	}
	profile, err := store.ResolveRunConfig(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	tools := bookkeepingTools{inner: emptyTools{}, svc: svc, profile: profile, sessionID: conv.ID, runID: projectToolRun(t, store, conv.ID)}
	schema := editSchema(t)

	opCount := 0
	edit := func(t *testing.T, revision int64, ops string) project.Result {
		t.Helper()
		opCount++
		payload := fmt.Sprintf(`{"projectId":%q,"opId":%q,"baseRevision":%d,"ops":[%s]}`, proj.ID, "op-round-trip-"+fmt.Sprint(opCount), revision, ops)
		if err := schemaValidate(t, schema, payload); err != nil {
			t.Fatalf("合法载荷未通过 schema：%v\n%s", err, payload)
		}
		raw, err := tools.Call(ctx, "project__edit", payload)
		if err != nil {
			t.Fatalf("后端拒绝合法载荷：%v\n%s", err, payload)
		}
		var result project.Result
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	rev := int64(0)
	tx := edit(t, rev, fmt.Sprintf(
		`{"kind":"project.rename","title":"Agent 修改的工程"},
		 {"kind":"tempo.set","tempo":[{"tick":0,"usPerQuarter":500000},{"tick":960,"usPerQuarter":400000}]},
		 {"kind":"meter.set","meter":[{"tick":0,"numerator":4,"denominator":4},{"tick":1920,"numerator":3,"denominator":8}]},
		 {"kind":"track.add","track":{"name":"旋律","color":"ice","channel":0,"program":73}}`))
	rev = tx.Revision
	trackID := tx.CreatedIDs[len(tx.CreatedIDs)-1]

	tx = edit(t, rev, fmt.Sprintf(
		`{"kind":"track.update","trackId":%q,"trackPatch":{"name":"主旋律","program":40}},
		 {"kind":"clip.add","clip":{"trackId":%q,"name":"A 段","color":"mint","start":0,"length":1920}}`, trackID, trackID))
	rev = tx.Revision
	clipID := tx.CreatedIDs[len(tx.CreatedIDs)-1]

	tx = edit(t, rev, fmt.Sprintf(
		`{"kind":"clip.update","clipId":%q,"clipPatch":{"length":3840}},
		 {"kind":"note.add","clipId":%q,"notes":[
		   {"start":0,"duration":480,"pitch":60,"velocity":90,"channel":0},
		   {"start":480,"duration":240,"pitch":64,"velocity":100,"channel":0}]}`,
		clipID, clipID))
	rev = tx.Revision
	noteA, noteB := tx.CreatedIDs[0], tx.CreatedIDs[1]

	tx = edit(t, rev, fmt.Sprintf(
		`{"kind":"note.update","clipId":%q,"noteDeltas":[{"id":%q,"velocity":110}]},
		 {"kind":"control.add","clipId":%q,"events":[
		   {"tick":0,"kind":"cc","channel":0,"controller":7,"value":100},
		   {"tick":120,"kind":"pitchBend","channel":0,"value":8192},
		   {"tick":240,"kind":"program","channel":0,"program":40},
		   {"tick":360,"kind":"channelAftertouch","channel":0,"value":64},
		   {"tick":480,"kind":"polyAftertouch","channel":0,"note":60,"value":32}]}`,
		clipID, noteA, clipID))
	rev = tx.Revision
	firstEvent := tx.CreatedIDs[0]

	tx = edit(t, rev, fmt.Sprintf(
		`{"kind":"control.remove","clipId":%q,"eventIds":[%q]},
		 {"kind":"clip.add","clip":{"trackId":%q,"start":3840,"length":480}}`, clipID, firstEvent, trackID))
	rev = tx.Revision
	tempClip := tx.CreatedIDs[len(tx.CreatedIDs)-1]

	tx = edit(t, rev, fmt.Sprintf(
		`{"kind":"clip.remove","clipId":%q},
		 {"kind":"note.remove","clipId":%q,"noteIds":[%q]}`, tempClip, clipID, noteB))
	rev = tx.Revision

	tx = edit(t, rev, `{"kind":"track.add","track":{"name":"备用轨","channel":1,"program":0}}`)
	rev = tx.Revision
	tempTrack := tx.CreatedIDs[0]

	edit(t, rev, fmt.Sprintf(`{"kind":"track.remove","trackId":%q}`, tempTrack))

	// 14 类 kind 全部经 schema 校验后被后端接受；终态可查询。
	query, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, proj.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "Agent 修改的工程") || !strings.Contains(query, "主旋律") {
		t.Fatalf("终态查询缺少期望内容：%s", query)
	}
}

// ---- Negative: schema / decode boundary rejections (契约 §6) ---------------

func TestProjectEditSchemaAndBoundaryRejections(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, nil, nil)
	proj, err := svc.CreateProject("拒绝边界", 480)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(ctx, storage.DefaultConfigID, "", "对话")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(conv.ID, proj.ID); err != nil {
		t.Fatal(err)
	}
	profile, err := store.ResolveRunConfig(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	tools := bookkeepingTools{inner: emptyTools{}, svc: svc, profile: profile, sessionID: conv.ID, runID: projectToolRun(t, store, conv.ID)}
	schema := editSchema(t)

	// 建立锚点对象：track t1 + clip c1。
	tx, err := svc.SubmitProjectOps(proj.ID, "setup", "gui", 0, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{Name: "轨1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	trackID := tx.CreatedIDs[0]
	tx, err = svc.SubmitProjectOps(proj.ID, "setup2", "gui", tx.Revision, []project.Op{
		{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: trackID, Start: 0, Length: 960}},
	})
	if err != nil {
		t.Fatal(err)
	}
	clipID := tx.CreatedIDs[0]
	base := tx.Revision

	// schemaValid=false：先断言迷你校验器以 wantSchemaErr 拒绝，再走工具调用
	// 断言边界/后端同样拒绝（wantToolErr）；schemaValid=true：schema 放行，
	// 断言后端以 wantToolErr 拒绝。两类拒绝都不得产生工程变更（函数末尾）。
	type rejection struct {
		name          string
		payload       string
		schemaValid   bool // 载荷是否符合 schema（越界值由 schema 拦，错字段名由边界拦）
		wantSchemaErr string
		wantToolErr   string
	}
	oneNote := func(pitch, velocity, channel int) string {
		return fmt.Sprintf(`{"start":0,"duration":10,"pitch":%d,"velocity":%d,"channel":%d}`, pitch, velocity, channel)
	}
	cases := []rejection{
		{"错字段名 note", fmt.Sprintf(`{"projectId":%q,"opId":"neg-1","baseRevision":%d,"ops":[{"kind":"note.add","clipId":%q,"note":[%s]}]}`, proj.ID, base, clipID, oneNote(60, 100, 0)), false, "未声明字段", "不支持字段"},
		{"inverse-only 字段 invNotes", fmt.Sprintf(`{"projectId":%q,"opId":"neg-2","baseRevision":%d,"ops":[{"kind":"clip.add","clip":{"trackId":%q,"start":0,"length":10},"invNotes":[%s]}]}`, proj.ID, base, trackID, oneNote(60, 100, 0)), false, "未声明字段", "不支持字段"},
		{"未知 kind", fmt.Sprintf(`{"projectId":%q,"opId":"neg-3","baseRevision":%d,"ops":[{"kind":"note.move","clipId":%q}]}`, proj.ID, base, clipID), false, "oneOf", "不支持的操作类型"},
		{"缺少 kind", fmt.Sprintf(`{"projectId":%q,"opId":"neg-4","baseRevision":%d,"ops":[{"clipId":%q}]}`, proj.ID, base, clipID), false, "oneOf", "缺少 kind"},
		{"pitch 越界", fmt.Sprintf(`{"projectId":%q,"opId":"neg-5","baseRevision":%d,"ops":[{"kind":"note.add","clipId":%q,"notes":[%s]}]}`, proj.ID, base, clipID, oneNote(128, 100, 0)), false, "超出上限", ""},
		{"velocity 越界", fmt.Sprintf(`{"projectId":%q,"opId":"neg-6","baseRevision":%d,"ops":[{"kind":"note.add","clipId":%q,"notes":[%s]}]}`, proj.ID, base, clipID, oneNote(60, 0, 0)), false, "低于下限", ""},
		{"channel 越界", fmt.Sprintf(`{"projectId":%q,"opId":"neg-7","baseRevision":%d,"ops":[{"kind":"note.add","clipId":%q,"notes":[%s]}]}`, proj.ID, base, clipID, oneNote(60, 100, 16)), false, "超出上限", ""},
		{"空 ops", fmt.Sprintf(`{"projectId":%q,"opId":"neg-8","baseRevision":%d,"ops":[]}`, proj.ID, base), false, "至少", "事务不能为空"},
		{"顶层未知字段", fmt.Sprintf(`{"projectId":%q,"opId":"neg-9","baseRevision":%d,"ops":[{"kind":"project.rename","title":"x"}],"extra":1}`, proj.ID, base), false, "未声明字段", "unknown field"},
		{"tempo 不以 tick 0 开始（schema 无法表达，后端拒绝）", fmt.Sprintf(`{"projectId":%q,"opId":"neg-10","baseRevision":%d,"ops":[{"kind":"tempo.set","tempo":[{"tick":480,"usPerQuarter":500000}]}]}`, proj.ID, base), true, "", "tick 0"},
		{"音符超出片段长度（schema 无法表达，后端拒绝）", fmt.Sprintf(`{"projectId":%q,"opId":"neg-11","baseRevision":%d,"ops":[{"kind":"note.add","clipId":%q,"notes":[{"start":960,"duration":10,"pitch":60,"velocity":90,"channel":0}]}]}`, proj.ID, base, clipID), true, "", "超出片段"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schemaErr := schemaValidate(t, schema, tc.payload)
			if tc.schemaValid && schemaErr != nil {
				t.Fatalf("标注 schema 合法但被拒：%v", schemaErr)
			}
			if !tc.schemaValid {
				if schemaErr == nil {
					t.Fatalf("载荷应被 schema 拒绝：%s", tc.payload)
				}
				if tc.wantSchemaErr != "" && !strings.Contains(schemaErr.Error(), tc.wantSchemaErr) {
					t.Fatalf("schema 错误消息 %q 缺少 %q", schemaErr.Error(), tc.wantSchemaErr)
				}
			}
			if _, callErr := tools.Call(ctx, "project__edit", tc.payload); callErr == nil {
				t.Fatalf("工具调用应失败：%s", tc.payload)
			} else if tc.wantToolErr != "" && !strings.Contains(callErr.Error(), tc.wantToolErr) {
				t.Fatalf("工具错误消息 %q 缺少 %q", callErr.Error(), tc.wantToolErr)
			}
		})
	}

	query, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, proj.ID))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(query), &state); err != nil {
		t.Fatal(err)
	}
	if state.Revision != base {
		t.Fatalf("被拒绝的批次不得产生工程变更：修订 %d ≠ %d", state.Revision, base)
	}
}
