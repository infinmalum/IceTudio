package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// Run-scoped project editing (docs/p3-agent-scope.md): a root run freezes the
// target project, base revision, access mode and track/clip allowlists at
// start; the agent's project tools are confined to that range for the whole
// run even if the user rebinds or re-selects later.

// ProjectScopeSelection is what a caller attaches to a send: the objects the
// user currently has in context. The service resolves it against the
// conversation's bound project into a frozen ProjectScope.
type ProjectScopeSelection struct {
	Tracks []string `json:"tracks,omitempty"`
	Clips  []string `json:"clips,omitempty"`
}

// ProjectScope is the frozen per-run edit range stored on runs.project_scope.
type ProjectScope = storage.ProjectScope

// buildRunScope resolves the send-time selection into the JSON stored on the
// run. A selection requires a bound project; a bound project without a
// selection freezes the whole project at the current revision. Unbound
// conversations get no scope and have no music tools during this run.
func (s *Service) buildRunScope(session storage.Session, selection *ProjectScopeSelection) (string, error) {
	if session.ProjectID == "" {
		if selection != nil && (len(selection.Tracks) > 0 || len(selection.Clips) > 0) {
			return "", errors.New("当前对话未绑定工程，不能携带编辑范围；请先绑定工程或取消范围")
		}
		return "", nil
	}
	item, err := s.store.GetProject(context.Background(), session.ProjectID)
	if err != nil {
		return "", fmt.Errorf("无法冻结任务范围：%w", err)
	}
	scope := ProjectScope{ProjectID: item.ID, BaseRevision: item.Revision, AccessMode: "write"}
	if selection != nil {
		seen := map[string]bool{}
		for _, id := range selection.Tracks {
			if id == "" || seen[id] {
				return "", errors.New("任务范围的轨道清单无效或重复")
			}
			seen[id] = true
		}
		clips := map[string]bool{}
		for _, id := range selection.Clips {
			if id == "" || seen[id] || clips[id] {
				return "", errors.New("任务范围的片段清单无效或重复")
			}
			clips[id] = true
		}
		for _, tr := range item.State.Tracks {
			delete(seen, tr.ID)
			for _, c := range tr.Clips {
				if clips[c.ID] {
					delete(clips, c.ID)
					if len(selection.Tracks) > 0 && !containsID(selection.Tracks, tr.ID) {
						return "", fmt.Errorf("片段 %s 不在任务范围的轨道清单内", c.ID)
					}
				}
			}
		}
		if len(seen) > 0 {
			return "", errors.New("任务范围引用了不存在的轨道；请刷新工程后重试")
		}
		if len(clips) > 0 {
			return "", errors.New("任务范围引用了不存在的片段；请刷新工程后重试")
		}
		scope.Tracks = append([]string(nil), selection.Tracks...)
		scope.Clips = append([]string(nil), selection.Clips...)
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func containsID(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// runProjectScope loads the frozen scope of the run backing a tool call.
// A missing/mismatched run is an error, never an implicit grant.
func (t bookkeepingTools) runProjectScope(ctx context.Context) (ProjectScope, bool, error) {
	if t.runID == "" {
		return ProjectScope{}, false, errors.New("音乐工具需要持久运行记录")
	}
	run, err := t.svc.store.GetRun(ctx, t.runID)
	if err != nil {
		return ProjectScope{}, false, err
	}
	if run.SessionID != t.sessionID {
		return ProjectScope{}, false, errors.New("任务不属于当前会话")
	}
	if run.ProjectScope == "" {
		return ProjectScope{}, false, nil
	}
	scope, err := storage.ParseProjectScope(run.ProjectScope)
	if err != nil {
		return ProjectScope{}, false, err
	}
	return scope, true, nil
}

// projectAccess keeps tool discovery and invocation aligned. Normal
// runs retain their target after rebinding. Delegated runs inherit the same
// ceiling without binding their hidden session to the project.
func (t bookkeepingTools) projectAccess(ctx context.Context) (string, ProjectScope, bool, error) {
	scope, frozen, err := t.runProjectScope(ctx)
	if err != nil {
		return "", ProjectScope{}, false, err
	}
	if !frozen {
		return "", ProjectScope{}, false, errors.New("本任务启动时没有音乐工程范围；请绑定工程后启动新任务")
	}
	return scope.ProjectID, scope, true, nil
}

// checkScopeOps rejects ops that touch objects outside the frozen range.
// The state is the current project state; objects it does not know are left
// to the backend's own existence checks (a concurrently removed clip must
// produce the backend's "片段不存在", not a scope error).
func checkScopeOps(scope ProjectScope, ops []project.Op, st *project.State) error {
	whole := len(scope.Tracks) == 0 && len(scope.Clips) == 0
	tracks := map[string]bool{}
	for _, id := range scope.Tracks {
		tracks[id] = true
	}
	clips := map[string]bool{}
	for _, id := range scope.Clips {
		clips[id] = true
	}
	projectLevel := ""
	for _, op := range ops {
		switch op.Kind {
		case "project.rename", "tempo.set", "meter.set", "track.add":
			if projectLevel == "" {
				projectLevel = op.Kind
			}
		case "track.update", "track.remove":
			if len(scope.Clips) > 0 || len(tracks) == 0 || !tracks[op.TrackID] {
				return fmt.Errorf("操作 %s 超出任务范围：只允许编辑所选轨道", op.Kind)
			}
		case "clip.add":
			if op.Clip == nil {
				continue // 交给后端报"缺少 clip"
			}
			if len(clips) > 0 {
				return fmt.Errorf("操作 %s 超出任务范围：片段清单任务不能新建片段", op.Kind)
			}
			if !tracks[op.Clip.TrackID] {
				return fmt.Errorf("操作 %s 超出任务范围：只能在所选轨道上新建片段", op.Kind)
			}
		case "clip.update", "clip.remove", "note.add", "note.update", "note.remove", "control.add", "control.remove":
			if len(clips) > 0 {
				if !clips[op.ClipID] {
					return fmt.Errorf("操作 %s 超出任务范围：只允许编辑所选片段", op.Kind)
				}
				continue
			}
			if len(tracks) == 0 {
				continue
			}
			trackID, ok := project.ClipTrack(st, op.ClipID)
			if !ok {
				continue // 后端按"片段不存在"拒绝
			}
			if !tracks[trackID] {
				return fmt.Errorf("操作 %s 超出任务范围：片段 %s 不在所选轨道内", op.Kind, op.ClipID)
			}
		default:
			continue // 未知 kind 由后端拒绝
		}
	}
	if projectLevel != "" && !whole {
		return fmt.Errorf("操作 %s 超出任务范围：工程级操作只在整工程任务中允许", projectLevel)
	}
	return nil
}
