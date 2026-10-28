package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// scopeIDs are the stable handles a scope builder may reference.
type scopeIDs struct {
	Project, T1, T2, C1, C2 string
}

// scopeFixture builds a bound conversation and a two-track project: t1 has
// clip c1 (one note), t2 has clip c2. Each run row is closed immediately so
// a session can host several runs; the returned run is frozen to
// scopeFor(ids) (the whole bound project is captured when nil).
func scopeFixture(t *testing.T, scopeFor func(ids scopeIDs) string) (context.Context, scopeIDs, bookkeepingTools) {
	t.Helper()
	ctx := context.Background()
	svc, store := newTestService(t, nil, nil)
	proj, err := svc.CreateProject("范围工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := svc.SubmitProjectOps(proj.ID, "setup-1", "gui", 0, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{Name: "轨1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t1 := tx.CreatedIDs[0]
	tx, err = svc.SubmitProjectOps(proj.ID, "setup-2", "gui", tx.Revision, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{Name: "轨2"}},
		{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: t1, Start: 0, Length: 960}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t2 := tx.CreatedIDs[0]
	c1 := tx.CreatedIDs[1]
	tx, err = svc.SubmitProjectOps(proj.ID, "setup-3", "gui", tx.Revision, []project.Op{
		{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: t2, Start: 0, Length: 960}},
		{Kind: "note.add", ClipID: c1, Notes: []project.NewNote{{Start: 0, Duration: 480, Pitch: 60, Velocity: 90, Channel: 0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c2 := tx.CreatedIDs[0]
	conv, err := store.CreateConversation(ctx, storage.DefaultConfigID, "", "范围对话")
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
	ids := scopeIDs{Project: proj.ID, T1: t1, T2: t2, C1: c1, C2: c2}
	scope := ""
	if scopeFor != nil {
		scope = scopeFor(ids)
	}
	run, _, err := store.CreateRunWithOptions(ctx, conv.ID, storage.CreateRunOptions{ProjectScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunStatus(ctx, run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	tools := bookkeepingTools{inner: emptyTools{}, svc: svc, profile: profile, sessionID: conv.ID, runID: run.ID}
	return ctx, ids, tools
}

func editPayload(projectID string, revision int64, opID, ops string) string {
	return fmt.Sprintf(`{"projectId":%q,"opId":%q,"baseRevision":%d,"ops":[%s]}`, projectID, opID, revision, ops)
}

func currentRevision(t *testing.T, tools bookkeepingTools, projectID string) int64 {
	t.Helper()
	raw, err := tools.Call(context.Background(), "project__query", fmt.Sprintf(`{"projectId":%q}`, projectID))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.Revision
}

func TestProjectScopeCaptureValidation(t *testing.T) {
	ctx := context.Background()
	chat := &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "好的"}}, nil
		},
		func([]model.Message) (model.ChatResponse, error) {
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "好的"}}, nil
		},
	}}
	svc, store := newTestService(t, chat, &scriptTools{})
	proj, err := svc.CreateProject("捕获工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := svc.SubmitProjectOps(proj.ID, "cap-1", "gui", 0, []project.Op{
		{Kind: "track.add", Track: &project.TrackSpec{Name: "轨1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	trackID := tx.CreatedIDs[0]
	conv, err := store.CreateConversation(ctx, storage.DefaultConfigID, "", "捕获对话")
	if err != nil {
		t.Fatal(err)
	}

	// 未绑定 + 携带选择 → 明确拒绝。
	if _, err := svc.StartConversationTask(conv.ID, "编辑片段", &ProjectScopeSelection{Clips: []string{"c_x"}}); err == nil {
		t.Fatal("未绑定对话携带范围应被拒绝")
	} else if !strings.Contains(err.Error(), "未绑定") {
		t.Fatalf("错误应说明未绑定：%v", err)
	}
	// 未绑定 + 无选择 → 允许（无范围 run，保持既有行为）。
	if _, err := svc.StartConversationTask(conv.ID, "普通聊天", nil); err != nil {
		t.Fatal(err)
	}
	svc.WaitIdle()
	if err := svc.SetConversationProject(conv.ID, proj.ID); err != nil {
		t.Fatal(err)
	}

	// 引用不存在的片段/轨 → 拒绝且不产生工程变更。
	if _, err := svc.StartConversationTask(conv.ID, "编辑片段", &ProjectScopeSelection{Clips: []string{"c_missing"}}); err == nil {
		t.Fatal("引用不存在的片段应被拒绝")
	}
	if _, err := svc.StartConversationTask(conv.ID, "编辑轨", &ProjectScopeSelection{Tracks: []string{"t_missing"}}); err == nil {
		t.Fatal("引用不存在的轨应被拒绝")
	}
	// 合法轨清单 → 放行；run 记录冻结范围。
	run, err := svc.StartConversationTask(conv.ID, "编辑轨", &ProjectScopeSelection{Tracks: []string{trackID}})
	if err != nil {
		t.Fatalf("合法轨清单应放行：%v", err)
	}
	defer svc.CancelTask(run.ID)
	if run.ProjectScope == "" || !strings.Contains(run.ProjectScope, trackID) {
		t.Fatalf("run 未记录冻结范围：%q", run.ProjectScope)
	}
	var scope ProjectScope
	if err := json.Unmarshal([]byte(run.ProjectScope), &scope); err != nil {
		t.Fatal(err)
	}
	item, err := store.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scope.BaseRevision != item.Revision || scope.AccessMode != "write" || scope.ProjectID != proj.ID {
		t.Fatalf("冻结范围不完整：%+v", scope)
	}
	svc.WaitIdle()
}

func TestProjectScopeEnforcementMatrix(t *testing.T) {
	cases := []struct {
		name   string
		write  bool
		whole  bool
		tracks bool
		clips  bool
		scope  func(ids scopeIDs) string
	}{
		{"整工程", true, true, false, false, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write"}`, ids.Project)
		}},
		{"轨清单", true, false, true, false, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","tracks":[%q]}`, ids.Project, ids.T1)
		}},
		{"片段清单", true, false, false, true, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","clips":[%q]}`, ids.Project, ids.C1)
		}},
		{"只读", false, true, false, false, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"read"}`, ids.Project)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, ids, tools := scopeFixture(t, tc.scope)

			// 工程级操作：仅整工程写任务放行。
			rev := currentRevision(t, tools, ids.Project)
			_, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "m-rename", `{"kind":"project.rename","title":"改名"}`))
			if tc.write && tc.whole && err != nil {
				t.Fatalf("整工程应允许改名：%v", err)
			}
			if (!tc.whole || !tc.write) && err == nil {
				t.Fatal("工程级操作应被范围拒绝")
			}

			// 音符编辑：范围内片段放行，范围外片段拒绝。
			if rev = currentRevision(t, tools, ids.Project); tc.write {
				if _, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "m-note1",
					fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C1))); err != nil {
					t.Fatalf("范围内音符应放行：%v", err)
				}
			}
			rev = currentRevision(t, tools, ids.Project)
			_, err = tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "m-note2",
				fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C2)))
			if tc.write && tc.whole && err != nil {
				t.Fatalf("整工程应允许任意片段：%v", err)
			}
			if (!tc.whole || !tc.write) && err == nil {
				t.Fatal("范围外片段应被拒绝")
			}

			// 新建片段：轨清单允许清单轨内；片段清单拒绝；整工程允许。
			rev = currentRevision(t, tools, ids.Project)
			_, err = tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "m-clip-add",
				fmt.Sprintf(`{"kind":"clip.add","clip":{"trackId":%q,"start":960,"length":480}}`, ids.T1)))
			if !tc.write {
				if err == nil {
					t.Fatal("只读任务不能新建片段")
				}
			} else if tc.tracks && err != nil {
				t.Fatalf("轨清单应允许轨内新建片段：%v", err)
			} else if tc.clips && err == nil {
				t.Fatal("片段清单不能新建片段")
			} else if tc.whole && err != nil {
				t.Fatalf("整工程应允许新建片段：%v", err)
			}

			// 轨属性：清单外轨拒绝。
			rev = currentRevision(t, tools, ids.Project)
			_, err = tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "m-track",
				fmt.Sprintf(`{"kind":"track.update","trackId":%q,"trackPatch":{"name":"轨2改名"}}`, ids.T2)))
			if tc.write && tc.whole && err != nil {
				t.Fatalf("整工程应允许改轨：%v", err)
			}
			if tc.write && !tc.whole && err == nil {
				t.Fatal("范围任务不应改清单外轨")
			}
			if !tc.write && err == nil {
				t.Fatal("只读任务不能改轨")
			}
		})
	}
}

func TestProjectScopeReplayShortCircuit(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, func(ids scopeIDs) string {
		return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","clips":[%q]}`, ids.Project, ids.C1)
	})
	svc := tools.svc
	rev := currentRevision(t, tools, ids.Project)
	payload := editPayload(ids.Project, rev, "replay-1",
		fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C1))
	first, err := tools.Call(ctx, "project__edit", payload)
	if err != nil {
		t.Fatal(err)
	}
	// 删除该片段后重放同一 opId：幂等短路必须返回原结果，而不是范围错误。
	if _, err := svc.SubmitProjectOps(ids.Project, "remove-clip", "gui", currentRevision(t, tools, ids.Project), []project.Op{
		{Kind: "clip.remove", ClipID: ids.C1},
	}); err != nil {
		t.Fatal(err)
	}
	again, err := tools.Call(ctx, "project__edit", payload)
	if err != nil {
		t.Fatalf("重放已成功结果被范围检查吞掉：%v", err)
	}
	if again != first {
		t.Fatal("重放应返回原结果")
	}
}

func TestProjectScopeRebindDoesNotRetarget(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, func(ids scopeIDs) string {
		return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","tracks":[%q]}`, ids.Project, ids.T1)
	})
	svc, store := tools.svc, tools.svc.store
	// 任务运行中把对话重绑到另一工程；冻结范围仍治理工具行为。
	other, err := store.CreateProject(ctx, "另一工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(tools.sessionID, other.ID); err != nil {
		t.Fatal(err)
	}
	// 原工程仍在范围内。
	rev := currentRevision(t, tools, ids.Project)
	if _, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "rebind-1",
		fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C1))); err != nil {
		t.Fatalf("重绑后原工程编辑应继续放行：%v", err)
	}
	// 新绑定工程不在冻结范围内（查询与编辑都被冻结范围拒绝）。
	if _, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, other.ID)); err == nil {
		t.Fatal("重绑后的新工程查询不应放行")
	}
	if _, err := tools.Call(ctx, "project__edit", editPayload(other.ID, 0, "rebind-2",
		`{"kind":"project.rename","title":"越权"}`)); err == nil {
		t.Fatal("重绑后的新工程不应被在途任务编辑")
	}
}

func TestProjectScopeQueryRegion(t *testing.T) {
	t.Run("轨清单", func(t *testing.T) {
		ctx, ids, tools := scopeFixture(t, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"read","tracks":[%q]}`, ids.Project, ids.T1)
		})
		raw, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, ids.Project))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(raw, ids.T1) || strings.Contains(raw, ids.T2) {
			t.Fatalf("轨清单查询应只返回 t1：%s", raw)
		}
	})
	t.Run("片段清单", func(t *testing.T) {
		ctx, ids, tools := scopeFixture(t, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"read","clips":[%q]}`, ids.Project, ids.C1)
		})
		raw, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, ids.Project))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(raw, ids.C1) || strings.Contains(raw, ids.C2) {
			t.Fatalf("片段清单查询应只返回 c1：%s", raw)
		}
	})
	t.Run("冻结工程之外拒绝", func(t *testing.T) {
		ctx, _, tools := scopeFixture(t, func(ids scopeIDs) string {
			return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"read"}`, ids.Project)
		})
		other, err := tools.svc.store.CreateProject(ctx, "别的工程", 480)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, other.ID)); err == nil {
			t.Fatal("冻结范围外的工程查询应被拒绝")
		}
	})
}

func TestProjectScopeUnbindRetainsFrozenAccess(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, func(ids scopeIDs) string {
		return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","clips":[%q]}`, ids.Project, ids.C1)
	})
	if err := tools.svc.SetConversationProject(tools.sessionID, ""); err != nil {
		t.Fatal(err)
	}
	defs, err := tools.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, def := range defs {
		found[def.Function.Name] = true
	}
	for _, name := range []string{"project__query", "project__edit", "project__select", "project__transform"} {
		if !found[name] {
			t.Fatalf("unbinding removed frozen tool %s", name)
		}
	}
	rev := currentRevision(t, tools, ids.Project)
	if _, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "unbound-in-scope",
		fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C1))); err != nil {
		t.Fatalf("unbinding blocked frozen edit: %v", err)
	}
	if _, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, rev+1, "unbound-outside",
		fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C2))); err == nil {
		t.Fatal("unbinding expanded the frozen scope")
	}

}

func TestProjectScopeOmittedCapturesBinding(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	run, err := tools.svc.store.GetRun(ctx, tools.runID)
	if err != nil || run.ProjectScope == "" {
		t.Fatalf("low-level creation did not freeze binding: %+v %v", run, err)
	}
	rev := currentRevision(t, tools, ids.Project)
	if _, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, rev, "captured-1",
		fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, ids.C2))); err != nil {
		t.Fatalf("自动冻结的范围应允许编辑：%v", err)
	}
}

func TestProjectScopeWorkerInheritance(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, nil, nil)
	data := storage.BlankRuntimeConfig()
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 1}
	data.AllMCPServers = true
	data.PlanEnabled = true
	data.Execution.Workflow = "dynamic"
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{Name: "worker", SystemPrompt: "完成交给你的子任务。", ContextBudget: 8192}}}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(ctx, p.ID, "", "继承对话")
	if err != nil {
		t.Fatal(err)
	}
	close := func(id string) {
		t.Helper()
		if err := store.SetRunStatus(ctx, id, "completed", ""); err != nil {
			t.Fatal(err)
		}
	}
	proj, err := svc.CreateProject("继承工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := svc.SubmitProjectOps(proj.ID, "inherit-setup", "gui", 0, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t1", Name: "轨1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(conv.ID, proj.ID); err != nil {
		t.Fatal(err)
	}
	scope := fmt.Sprintf(`{"projectId":%q,"baseRevision":%d,"accessMode":"write","tracks":["t1"]}`, proj.ID, setup.Revision)
	root, _, err := store.CreateRunWithOptions(ctx, conv.ID, storage.CreateRunOptions{ProjectScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetTaskGraph(ctx, root.ID, 0, []storage.TaskSpec{
		{ID: "task-r", Worker: "worker", Task: "只读", AccessMode: "read"},
		{ID: "task-w", Worker: "worker", Task: "写入", AccessMode: "write"},
	}); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if root.ProjectScope != scope {
		t.Fatalf("根 run 范围未写入：%s", root.ProjectScope)
	}
	// worker 子运行在 delegation 子会话上（与真实委派一致）；root 保持 running。
	childSession, err := store.CreateDelegationSession(ctx, conv.ContextID, conv.ConfigID, "子任务")
	if err != nil {
		t.Fatal(err)
	}
	// read 任务收紧；write 任务继承。
	readSpec := storage.TaskSpec{ID: "task-r", Worker: "worker", Task: "只读", AccessMode: "read"}
	child, _, err := store.CreateRunWithOptions(ctx, childSession.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker", TaskSpec: &readSpec})
	if err != nil {
		t.Fatal(err)
	}
	close(child.ID)
	if !strings.Contains(child.ProjectScope, `"accessMode":"read"`) || !strings.Contains(child.ProjectScope, `"tracks":["t1"]`) {
		t.Fatalf("read 任务未正确继承收紧：%s", child.ProjectScope)
	}
	writeSpec := storage.TaskSpec{ID: "task-w", Worker: "worker", Task: "写入", AccessMode: "write"}
	child2, _, err := store.CreateRunWithOptions(ctx, childSession.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker", TaskSpec: &writeSpec})
	if err != nil {
		t.Fatal(err)
	}
	close(child2.ID)
	var inherited ProjectScope
	if err := json.Unmarshal([]byte(child2.ProjectScope), &inherited); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inherited, ProjectScope{ProjectID: proj.ID, BaseRevision: setup.Revision, AccessMode: "write", Tracks: []string{"t1"}}) {
		t.Fatalf("write 任务应原样继承：%s", child2.ProjectScope)
	}
	// A hidden delegation session uses the inherited ceiling without binding.
	profile, err := store.ResolveRunConfig(ctx, childSession.ID)
	if err != nil {
		t.Fatal(err)
	}
	tools := bookkeepingTools{inner: emptyTools{}, svc: svc, profile: profile, sessionID: childSession.ID, runID: child2.ID, delegated: true}
	if _, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, proj.ID)); err != nil {
		t.Fatalf("worker cannot query inherited project: %v", err)
	}
	defs, err := tools.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, def := range defs {
		found[def.Function.Name] = true
	}
	if !found["project__query"] || !found["project__edit"] {
		t.Fatal("worker did not discover inherited project tools")
	}
	// planner/reviewer 组件强制只读。
	planner, _, err := store.CreateRunWithOptions(ctx, childSession.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "@planner", Component: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	close(planner.ID)
	if !strings.Contains(planner.ProjectScope, `"accessMode":"read"`) {
		t.Fatalf("planner 应强制只读：%s", planner.ProjectScope)
	}
	// 子运行不能自带范围。
	if _, _, err := store.CreateRunWithOptions(ctx, childSession.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker", ProjectScope: scope}); err == nil {
		t.Fatal("子运行自带范围应被拒绝")
	}
	// Unbound roots and their children have no project access.
	close(root.ID)
	if err := svc.SetConversationProject(conv.ID, ""); err != nil {
		t.Fatal(err)
	}
	legacyRoot2, _, err := store.CreateRunWithOptions(ctx, conv.ID, storage.CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	legacyChild, _, err := store.CreateRunWithOptions(ctx, childSession.ID, storage.CreateRunOptions{ParentRunID: legacyRoot2.ID, Depth: 1, WorkerName: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if legacyChild.ProjectScope != "" {
		t.Fatalf("未绑定的根任务范围应保持空：%s", legacyChild.ProjectScope)
	}
	close(legacyChild.ID)
	close(legacyRoot2.ID)
	// 根运行范围必须是合法 JSON。
	if _, _, err := store.CreateRunWithOptions(ctx, conv.ID, storage.CreateRunOptions{ProjectScope: "{bad"}); err == nil {
		t.Fatal("非法范围 JSON 应被拒绝")
	}
}
