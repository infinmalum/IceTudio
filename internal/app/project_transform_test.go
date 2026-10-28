package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"icetudio/internal/project"
)

// ---- Static schema consistency (契约 docs/p3-agent-tools.md §7) ------------

func transformSchemas(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	var sel, tr map[string]any
	for _, d := range projectDefinitions() {
		switch d.Function.Name {
		case "project__select":
			if err := json.Unmarshal(d.Function.Parameters, &sel); err != nil {
				t.Fatalf("select schema 不可解析：%v", err)
			}
		case "project__transform":
			if err := json.Unmarshal(d.Function.Parameters, &tr); err != nil {
				t.Fatalf("transform schema 不可解析：%v", err)
			}
		}
	}
	if sel == nil || tr == nil {
		t.Fatal("缺少 select/transform 工具定义")
	}
	return sel, tr
}

func TestProjectTransformSchemaShape(t *testing.T) {
	selSchema, trSchema := transformSchemas(t)
	// select：projectId 必填、selection 可选、无多余字段。
	if got := toKeySet(selSchema["properties"].(map[string]any)); !reflect.DeepEqual(got, toKeySetFromList([]string{"projectId", "selection"})) {
		t.Fatalf("select 顶层字段 %v", got)
	}
	if got := toStrings(selSchema["required"]); !reflect.DeepEqual(got, []string{"projectId"}) {
		t.Fatalf("select 必填 %v", got)
	}
	// transform：必填四件、selection/dryRun 可选。
	top := trSchema["properties"].(map[string]any)
	if got := toKeySet(top); !reflect.DeepEqual(got, toKeySetFromList([]string{"projectId", "opId", "baseRevision", "selection", "transform", "dryRun"})) {
		t.Fatalf("transform 顶层字段 %v", got)
	}
	if got := toStrings(trSchema["required"]); !reflect.DeepEqual(got, []string{"projectId", "opId", "baseRevision", "transform"}) {
		t.Fatalf("transform 必填 %v", got)
	}
	// transform 恰好一种：min/maxProperties + 仅四个声明属性。
	transform := top["transform"].(map[string]any)
	if transform["minProperties"] != float64(1) || transform["maxProperties"] != float64(1) {
		t.Fatalf("transform 必须表达恰好一种变换：%v", transform)
	}
	variants := toKeySet(transform["properties"].(map[string]any))
	if !reflect.DeepEqual(variants, toKeySetFromList([]string{"transpose", "quantize", "velocity", "shift"})) {
		t.Fatalf("变换种类 %v", variants)
	}
	// 数值界来自 project 常量。
	checkBound := func(node map[string]any, wantMin, wantMax float64) {
		t.Helper()
		if node["minimum"] != wantMin || node["maximum"] != wantMax {
			t.Fatalf("界错误：期望 %v–%v 实得 %v–%v", wantMin, wantMax, node["minimum"], node["maximum"])
		}
	}
	checkBound(transform["properties"].(map[string]any)["transpose"].(map[string]any)["properties"].(map[string]any)["semitones"].(map[string]any), -127, 127)
	checkBound(transform["properties"].(map[string]any)["shift"].(map[string]any)["properties"].(map[string]any)["ticks"].(map[string]any), -float64(project.MaxTick), float64(project.MaxTick))
	quant := transform["properties"].(map[string]any)["quantize"].(map[string]any)["properties"].(map[string]any)
	checkBound(quant["strength"].(map[string]any), 0, 100)
	if got := toStrings(quant["unit"].(map[string]any)["enum"]); len(got) != 6 || got[0] != "1/4" || got[len(got)-1] != "1/16T" {
		t.Fatalf("量化单位枚举 %v", got)
	}
	vel := transform["properties"].(map[string]any)["velocity"].(map[string]any)["properties"].(map[string]any)
	checkBound(vel["scale"].(map[string]any), 1, 300)
	checkBound(vel["offset"].(map[string]any), -126, 126)
	checkBound(vel["set"].(map[string]any), 1, 127)
	// selection 字段界。
	selProps := top["selection"].(map[string]any)["properties"].(map[string]any)
	checkBound(selProps["pitchMin"].(map[string]any), 0, 127)
	checkBound(selProps["channel"].(map[string]any), 0, project.MaxChannel)
	checkBound(selProps["velocityMin"].(map[string]any), 1, 127)
	checkBound(selProps["startTick"].(map[string]any), 0, float64(project.MaxTick))
}

func TestProjectTransformSchemaValidation(t *testing.T) {
	_, trSchema := transformSchemas(t)
	valid := func(transform string) string {
		return fmt.Sprintf(`{"projectId":"p","opId":"op-1","baseRevision":0,"selection":{"clipIds":["c1"]},"transform":%s}`, transform)
	}
	cases := []struct {
		name    string
		payload string
		wantErr string
	}{
		{"移调", valid(`{"transpose":{"semitones":-12}}`), ""},
		{"量化带强度", valid(`{"quantize":{"unit":"1/8T","strength":60}}`), ""},
		{"力度 set", valid(`{"velocity":{"set":100}}`), ""},
		{"力度 scale+offset", valid(`{"velocity":{"scale":150,"offset":-10}}`), ""},
		{"平移", valid(`{"shift":{"ticks":480}}`), ""},
		{"零变换", valid(`{}`), "至少要有 1 个字段"},
		{"两种变换", valid(`{"transpose":{"semitones":1},"shift":{"ticks":1}}`), "至多要有 1 个字段"},
		{"未知变换", valid(`{"invert":{}}`), "未声明字段"},
		{"未知单位", valid(`{"quantize":{"unit":"1/7"}}`), "不在枚举内"},
		{"半音越界", valid(`{"transpose":{"semitones":128}}`), "超出上限"},
		{"shift 越界", valid(`{"shift":{"ticks":2147483648}}`), "超出上限"},
		{"velocity 全空", valid(`{"velocity":{}}`), "至少要有 1 个字段"},
		{"顶层未知字段", `{"projectId":"p","opId":"op-1","baseRevision":0,"transform":{"shift":{"ticks":1}},"extra":1}`, "未声明字段"},
		{"缺 transform", `{"projectId":"p","opId":"op-1","baseRevision":0}`, "缺少必填字段"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := schemaValidate(t, trSchema, tc.payload)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过：%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("应报 %q，实得 %v", tc.wantErr, err)
			}
		})
	}
}

// ---- Tool-level behavior ----------------------------------------------------

func selectPayload(projectID string, selection string) string {
	if selection == "" {
		return fmt.Sprintf(`{"projectId":%q}`, projectID)
	}
	return fmt.Sprintf(`{"projectId":%q,"selection":%s}`, projectID, selection)
}

func transformPayload(projectID string, revision int64, opID, selection, transform string, dryRun bool) string {
	sel := ""
	if selection != "" {
		sel = fmt.Sprintf(`"selection":%s,`, selection)
	}
	return fmt.Sprintf(`{"projectId":%q,"opId":%q,"baseRevision":%d,%s"transform":%s,"dryRun":%v}`,
		projectID, opID, revision, sel, transform, dryRun)
}

func selectResult(t *testing.T, raw string) (matched, returned int, revision int64) {
	t.Helper()
	var parsed struct {
		Matched  int   `json:"matched"`
		Returned int   `json:"returned"`
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("select 结果不可解析：%v\n%s", err, raw)
	}
	return parsed.Matched, parsed.Returned, parsed.Revision
}

func TestProjectSelectMatchesAndPages(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	// 再加两枚音符到 c1，共 3 枚（0/240/480）。
	rev := currentRevision(t, tools, ids.Project)
	if _, err := tools.svc.SubmitProjectOps(ids.Project, "sel-setup", "gui", rev, []project.Op{
		{Kind: "note.add", ClipID: ids.C1, Notes: []project.NewNote{
			{Start: 240, Duration: 100, Pitch: 64, Velocity: 50, Channel: 0},
			{Start: 480, Duration: 100, Pitch: 67, Velocity: 70, Channel: 0},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := tools.Call(ctx, "project__select", selectPayload(ids.Project, fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1)))
	if err != nil {
		t.Fatal(err)
	}
	if matched, returned, _ := selectResult(t, raw); matched != 3 || returned != 3 {
		t.Fatalf("选择应匹配 3 枚：%s", raw)
	}
	// 起音窗口 [0,300) 只含 0 与 240 两枚（480 起音在窗外）。
	raw, err = tools.Call(ctx, "project__select", selectPayload(ids.Project, fmt.Sprintf(`{"clipIds":[%q],"startTick":0,"endTick":300}`, ids.C1)))
	if err != nil {
		t.Fatal(err)
	}
	if matched, _, _ := selectResult(t, raw); matched != 2 {
		t.Fatalf("起音窗口应匹配 2 枚：%s", raw)
	}
	// 力度过滤 AND。
	raw, err = tools.Call(ctx, "project__select", selectPayload(ids.Project, fmt.Sprintf(`{"clipIds":[%q],"velocityMin":60}`, ids.C1)))
	if err != nil {
		t.Fatal(err)
	}
	if matched, _, rev := selectResult(t, raw); matched != 2 {
		t.Fatalf("力度过滤应匹配 2 枚：%s", raw)
	} else if rev == 0 {
		t.Fatal("select 结果必须携带 revision")
	}
	// 空选择 = 全部（select 不要求限定）。
	raw, err = tools.Call(ctx, "project__select", selectPayload(ids.Project, ""))
	if err != nil {
		t.Fatal(err)
	}
	if matched, _, _ := selectResult(t, raw); matched != 3 {
		t.Fatalf("空选择应匹配全部：%s", raw)
	}
	// 未知片段零匹配而非报错。
	raw, err = tools.Call(ctx, "project__select", selectPayload(ids.Project, `{"clipIds":["c_none"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if matched, _, _ := selectResult(t, raw); matched != 0 {
		t.Fatalf("未知片段应零匹配：%s", raw)
	}
}

func TestProjectTransformDryRunThenCommit(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	rev := currentRevision(t, tools, ids.Project)
	sel := fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1)
	// dry-run：返回样例，不改修订、不建事务。
	raw, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "dry-1", sel, `{"transpose":{"semitones":1}}`, true))
	if err != nil {
		t.Fatal(err)
	}
	var dry struct {
		DryRun  bool             `json:"dryRun"`
		Matched int              `json:"matched"`
		Changed int              `json:"changed"`
		Sample  []map[string]any `json:"sample"`
	}
	if err := json.Unmarshal([]byte(raw), &dry); err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.Matched != 1 || dry.Changed != 1 || len(dry.Sample) != 1 || dry.Sample[0]["pitch"] != float64(61) {
		t.Fatalf("dry-run 预览错误：%s", raw)
	}
	if now := currentRevision(t, tools, ids.Project); now != rev {
		t.Fatalf("dry-run 改了修订：%d→%d", rev, now)
	}
	if _, found, err := tools.svc.GetProjectTransaction(ids.Project, "dry-1"); err != nil || found {
		t.Fatalf("dry-run 不应建事务行：%v", err)
	}
	// 真实提交：修订 +1，音符实际移调。
	raw, err = tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "tr-1", sel, `{"transpose":{"semitones":1}}`, false))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Revision int64          `json:"revision"`
		Matched  int            `json:"matched"`
		Changed  int            `json:"changed"`
		Summary  map[string]int `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != rev+1 || result.Matched != 1 || result.Changed != 1 || result.Summary["note.update"] != 1 {
		t.Fatalf("变换结果错误：%s", raw)
	}
	item, err := tools.svc.GetProject(ids.Project)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range item.State.Tracks {
		for _, c := range tr.Clips {
			if c.ID != ids.C1 {
				continue
			}
			if c.Notes[0].Pitch != 61 {
				t.Fatalf("音符应移调为 61：%+v", c.Notes[0])
			}
		}
	}
	if _, found, err := tools.svc.GetProjectTransaction(ids.Project, "tr-1"); err != nil || !found {
		t.Fatalf("变换应可按 opId 查询：%v", err)
	}
}

func TestProjectTransformRejections(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	rev := currentRevision(t, tools, ids.Project)
	cases := []struct {
		name     string
		payload  string
		wantErrs []string
	}{
		{"无限定选择", transformPayload(ids.Project, rev, "rj-1", `{}`, `{"shift":{"ticks":1}}`, false),
			[]string{"变换必须限定选择范围"}},
		{"零匹配", transformPayload(ids.Project, rev, "rj-2", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C2), `{"shift":{"ticks":1}}`, false),
			[]string{"未匹配任何音符"}},
		{"两种变换", transformPayload(ids.Project, rev, "rj-3", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{"transpose":{"semitones":1},"shift":{"ticks":1}}`, false),
			[]string{"恰好指定一种"}},
		{"零变换", transformPayload(ids.Project, rev, "rj-4", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{}`, false),
			[]string{"恰好指定一种"}},
		{"set 与 scale 同给", transformPayload(ids.Project, rev, "rj-5", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{"velocity":{"set":100,"scale":120}}`, false),
			[]string{"不能与 scale/offset 同时使用"}},
		{"未知顶层字段", fmt.Sprintf(`{"projectId":%q,"opId":"rj-6","baseRevision":%d,"selection":{"clipIds":[%q]},"transform":{"shift":{"ticks":1}},"bogus":1}`, ids.Project, rev, ids.C1),
			[]string{"unknown field"}},
		{"缺 baseRevision", fmt.Sprintf(`{"projectId":%q,"opId":"rj-7","selection":{"clipIds":[%q]},"transform":{"shift":{"ticks":1}}}`, ids.Project, ids.C1),
			[]string{"缺少必需参数：baseRevision"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tools.Call(ctx, "project__transform", tc.payload)
			if err == nil {
				t.Fatal("应被拒绝")
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("错误应含 %q：%v", want, err)
				}
			}
		})
	}
	// 被拒批次零工程变更。
	if now := currentRevision(t, tools, ids.Project); now != rev {
		t.Fatalf("被拒调用改了修订：%d→%d", rev, now)
	}
}

func TestProjectTransformStaleBaseRevisionRejected(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	rev := currentRevision(t, tools, ids.Project)
	sel := fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1)
	// 用户先手改（GUI 路径）。
	if _, err := tools.svc.SubmitProjectOps(ids.Project, "user-first", "gui", rev, []project.Op{
		{Kind: "track.update", TrackID: ids.T1, TrackPatch: &project.TrackPatch{Name: ptr("用户改名")}},
	}); err != nil {
		t.Fatal(err)
	}
	stale := currentRevision(t, tools, ids.Project)
	_, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "stale-1", sel, `{"transpose":{"semitones":2}}`, false))
	if err == nil || !strings.Contains(err.Error(), "修订") {
		t.Fatalf("过时基准应被冲突拒绝：%v", err)
	}
	if now := currentRevision(t, tools, ids.Project); now != stale {
		t.Fatalf("被拒变换改了修订：%d→%d", stale, now)
	}
	// 重新查询后用新基准成功。
	raw, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, stale, "fresh-1", sel, `{"transpose":{"semitones":2}}`, false))
	if err != nil {
		t.Fatalf("新基准应放行：%v", err)
	}
	_ = raw
}

func ptr(s string) *string { return &s }

func TestProjectTransformReplayShortCircuit(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	rev := currentRevision(t, tools, ids.Project)
	sel := fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1)
	payload := transformPayload(ids.Project, rev, "replay-tr", sel, `{"transpose":{"semitones":-3}}`, false)
	first, err := tools.Call(ctx, "project__transform", payload)
	if err != nil {
		t.Fatal(err)
	}
	// 之后音符被删除；重放同一 opId 必须返回存储结果，而不是编译错误。
	item, err := tools.svc.GetProject(ids.Project)
	if err != nil {
		t.Fatal(err)
	}
	noteID := ""
	for _, tr := range item.State.Tracks {
		for _, c := range tr.Clips {
			if c.ID == ids.C1 && len(c.Notes) > 0 {
				noteID = c.Notes[0].ID
			}
		}
	}
	if _, err := tools.svc.SubmitProjectOps(ids.Project, "remove-note", "gui", currentRevision(t, tools, ids.Project), []project.Op{
		{Kind: "note.remove", ClipID: ids.C1, NoteIDs: []string{noteID}},
	}); err != nil {
		t.Fatal(err)
	}
	again, err := tools.Call(ctx, "project__transform", payload)
	if err != nil {
		t.Fatalf("重放被编译错误吞掉：%v", err)
	}
	// 首次响应叠加变换统计；重放返回存储的 project.Result。核心字段必须一致。
	type core struct {
		OpID         string         `json:"opId"`
		Revision     int64          `json:"revision"`
		BaseRevision int64          `json:"baseRevision"`
		Applied      int            `json:"applied"`
		Summary      map[string]int `json:"summary"`
	}
	var a, b core
	if err := json.Unmarshal([]byte(first), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(again), &b); err != nil {
		t.Fatal(err)
	}
	if a.OpID != b.OpID || a.Revision != b.Revision || a.BaseRevision != b.BaseRevision || a.Applied != b.Applied || !reflect.DeepEqual(a.Summary, b.Summary) {
		t.Fatalf("重放核心结果不一致：%+v vs %+v", a, b)
	}
}

func TestProjectTransformScopeMatrix(t *testing.T) {
	// 每个用例先给 c2 补一枚音符（fixture 的 c2 为空），保证范围外拒绝
	// 走 checkScopeOps 而不是零匹配。
	cases := []struct {
		name  string
		scope func(ids scopeIDs) string
	}{
		{"只读", func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"read"}`, ids.Project)
		}},
		{"片段清单", func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","clips":[%q]}`, ids.Project, ids.C1)
		}},
		{"轨清单", func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","tracks":[%q]}`, ids.Project, ids.T1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, ids, tools := scopeFixture(t, tc.scope)
			rev := currentRevision(t, tools, ids.Project)
			if _, err := tools.svc.SubmitProjectOps(ids.Project, "seed-c2", "gui", rev, []project.Op{
				{Kind: "note.add", ClipID: ids.C2, Notes: []project.NewNote{{Start: 0, Duration: 240, Pitch: 55, Velocity: 80, Channel: 0}}},
			}); err != nil {
				t.Fatal(err)
			}
			selC1 := fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1)
			selC2 := fmt.Sprintf(`{"clipIds":[%q]}`, ids.C2)
			selWhole := `{}`

			// select：只读可用；片段清单把无选择交集到清单内。
			raw, err := tools.Call(ctx, "project__select", selectPayload(ids.Project, selWhole))
			if err != nil {
				t.Fatal(err)
			}
			matched, _, _ := selectResult(t, raw)
			switch tc.name {
			case "只读":
				if matched != 2 {
					t.Fatalf("%s 全选应见 2 枚（c1+c2）：%s", tc.name, raw)
				}
			case "片段清单", "轨清单":
				// 清单交集后只可见 c1 的一枚音符。
				if matched != 1 {
					t.Fatalf("%s 全选应交集为 c1 一枚：%s", tc.name, raw)
				}
			}
			// select 引用清单外片段：交集为空 → 零匹配结果而非报错。
			if tc.name == "片段清单" {
				raw, err = tools.Call(ctx, "project__select", selectPayload(ids.Project, selC2))
				if err != nil {
					t.Fatal(err)
				}
				if matched, _, _ := selectResult(t, raw); matched != 0 {
					t.Fatalf("清单外片段选择应零匹配：%s", raw)
				}
			}

			// transform：范围内放行。
			rev = currentRevision(t, tools, ids.Project)
			if tc.name == "只读" {
				if _, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "ro-tr", selC1, `{"transpose":{"semitones":1}}`, false)); err == nil {
					t.Fatal("只读任务不能变换")
				}
				// 只读连 dry-run 也拒绝（契约 §1）。
				if _, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "ro-dry", selC1, `{"transpose":{"semitones":1}}`, true)); err == nil {
					t.Fatal("只读任务不能 dry-run")
				}
				return
			}
			if _, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "in-tr", selC1, `{"transpose":{"semitones":1}}`, false)); err != nil {
				t.Fatalf("范围内变换应放行：%v", err)
			}
			// 范围外拒绝（c2 有音符，走 checkScopeOps）。
			rev = currentRevision(t, tools, ids.Project)
			for _, transform := range []string{`{"transpose":{"semitones":1}}`, `{"transpose":{"semitones":0}}`, `{"transpose":{"semitones":127}}`} {
				for _, selection := range []string{selC2, fmt.Sprintf(`{"clipIds":[%q,%q]}`, ids.C1, ids.C2), `{"startTick":0,"endTick":480}`} {
					for _, dry := range []bool{false, true} {
						raw, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "scope-probe", selection, transform, dry))
						if err == nil || !strings.Contains(err.Error(), "范围") || raw != "" {
							t.Fatalf("out-of-scope selection must fail before exposing deltas/counts: dry=%v transform=%s raw=%s err=%v", dry, transform, raw, err)
						}
						if strings.Contains(err.Error(), ids.C2) {
							t.Fatalf("denial disclosed an out-of-scope clip ID: %v", err)
						}
					}
				}
				if now := currentRevision(t, tools, ids.Project); now != rev {
					t.Fatal("denied selection changed revision")
				}
			}
			_, err = tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "out-tr", selC2, `{"transpose":{"semitones":1}}`, false))
			if tc.name == "片段清单" && (err == nil || !strings.Contains(err.Error(), "范围")) {
				t.Fatalf("片段清单外的变换应被范围拒绝：%v", err)
			}
			if tc.name == "轨清单" {
				// 轨清单允许 t2 上片段？不允许——清单只含 t1，c2 在 t2。
				if err == nil || !strings.Contains(err.Error(), "范围") {
					t.Fatalf("轨清单外变换应被范围拒绝：%v", err)
				}
			}
			// 无限定选择被运行时拒绝（即便范围本可兜底）。
			rev = currentRevision(t, tools, ids.Project)
			if _, err := tools.Call(ctx, "project__transform", transformPayload(ids.Project, rev, "unb-tr", selWhole, `{"transpose":{"semitones":1}}`, false)); err == nil || !strings.Contains(err.Error(), "变换必须限定选择范围") {
				t.Fatalf("无限定选择应拒绝：%v", err)
			}
		})
	}
}

func TestProjectTransformWrongProjectRejected(t *testing.T) {
	ctx, _, tools := scopeFixture(t, nil)
	other, err := tools.svc.store.CreateProject(ctx, "别的工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Call(ctx, "project__select", selectPayload(other.ID, "")); err == nil {
		t.Fatal("未绑定工程的 select 应拒绝")
	}
	if _, err := tools.Call(ctx, "project__transform", transformPayload(other.ID, 0, "wrong-1", `{}`, `{"shift":{"ticks":1}}`, false)); err == nil {
		t.Fatal("未绑定工程的 transform 应拒绝")
	}
}
