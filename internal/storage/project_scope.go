package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"icetudio/internal/project"
)

// ProjectScope is a run's immutable project permission ceiling. Empty stored
// JSON means no project access, never permission to consult a later binding.
type ProjectScope struct {
	ProjectID    string   `json:"projectId"`
	BaseRevision int64    `json:"baseRevision"`
	AccessMode   string   `json:"accessMode"`
	Tracks       []string `json:"tracks,omitempty"`
	Clips        []string `json:"clips,omitempty"`
}

func ParseProjectScope(raw string) (ProjectScope, error) {
	var scope ProjectScope
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&scope); err != nil {
		return scope, fmt.Errorf("任务工程范围损坏：%w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return scope, errors.New("任务工程范围包含多余内容")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return scope, err
	}
	for _, name := range []string{"projectId", "baseRevision", "accessMode"} {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			return scope, fmt.Errorf("任务工程范围缺少 %s", name)
		}
	}
	if scope.ProjectID == "" || scope.BaseRevision < 0 || (scope.AccessMode != "read" && scope.AccessMode != "write") {
		return scope, errors.New("任务工程范围缺少有效工程、修订或访问模式")
	}
	seen := map[string]bool{}
	for _, ids := range [][]string{scope.Tracks, scope.Clips} {
		for _, id := range ids {
			if !project.ValidID(id) || seen[id] {
				return scope, errors.New("任务范围的轨道或片段清单无效或重复")
			}
			seen[id] = true
		}
	}
	return scope, nil
}

// freezeProjectScopeInTx also covers low-level, inspection and improver root
// entry points. An explicit scope is a ceiling captured by the app, not a way
// to choose a project different from the binding read in this transaction.
func freezeProjectScopeInTx(ctx context.Context, tx *sql.Tx, sessionID, requested, component string, inspection bool) (string, error) {
	var bound, kind string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(project_id,''),kind FROM sessions WHERE id=?`, sessionID).Scan(&bound, &kind); err != nil {
		return "", err
	}
	if kind == SessionKindDelegation {
		return "", errors.New("子任务会话不能创建独立根运行")
	}
	if bound == "" {
		if requested != "" {
			return "", errors.New("对话工程绑定已改变，无法冻结任务范围；请刷新后重试")
		}
		return "", nil
	}
	var revision int64
	var stateRaw string
	if err := tx.QueryRowContext(ctx, `SELECT revision,state FROM projects WHERE id=?`, bound).Scan(&revision, &stateRaw); err != nil {
		return "", fmt.Errorf("无法冻结任务工程：%w", err)
	}
	scope := ProjectScope{ProjectID: bound, BaseRevision: revision, AccessMode: "write"}
	if requested != "" {
		var err error
		scope, err = ParseProjectScope(requested)
		if err != nil {
			return "", err
		}
		if scope.ProjectID != bound || scope.BaseRevision > revision {
			return "", errors.New("任务范围与当前工程绑定或修订不一致；请刷新后重试")
		}
		var state project.State
		if err := json.Unmarshal([]byte(stateRaw), &state); err != nil {
			return "", err
		}
		tracks, clips := map[string]bool{}, map[string]bool{}
		for _, id := range scope.Tracks {
			tracks[id] = true
		}
		for _, id := range scope.Clips {
			clips[id] = true
		}
		for _, tr := range state.Tracks {
			for _, c := range tr.Clips {
				if clips[c.ID] {
					if len(scope.Tracks) > 0 && !tracks[tr.ID] {
						return "", errors.New("任务片段不在允许的轨道内")
					}
					delete(clips, c.ID)
				}
			}
		}
		for _, tr := range state.Tracks {
			delete(tracks, tr.ID)
		}
		if len(tracks) > 0 || len(clips) > 0 {
			return "", errors.New("任务范围引用了不存在的轨道或片段；请刷新工程后重试")
		}
	}
	if component == "improver" || inspection {
		scope.AccessMode = "read"
	}
	out, err := json.Marshal(scope)
	return string(out), err
}

func inheritProjectScope(parentScope, component, accessMode string) (string, error) {
	if parentScope == "" {
		return "", nil
	}
	scope, err := ParseProjectScope(parentScope)
	if err != nil {
		return "", err
	}
	if accessMode == "read" || component == "planner" || component == "reviewer" || component == "improver" {
		scope.AccessMode = "read"
	}
	out, err := json.Marshal(scope)
	return string(out), err
}

// All worker admission, including direct delegations with no task graph,
// happens in the run-creation transaction. Only declared read tasks overlap.
func checkWorkerConcurrencyInTx(ctx context.Context, tx *sql.Tx, parent string, spec *TaskSpec) error {
	read := spec != nil && spec.AccessMode == "read"
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(i.spec,'') FROM runs r
		LEFT JOIN task_attempts a ON a.run_id=r.id
		LEFT JOIN task_items i ON i.root_run_id=a.root_run_id AND i.id=a.task_id
		WHERE r.parent_run_id=? AND r.component='worker' AND r.status='running'`, parent)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var active TaskSpec
		if !read || raw == "" {
			return errors.New("写入或未知副作用任务需要串行")
		}
		if err := json.Unmarshal([]byte(raw), &active); err != nil {
			return err
		}
		if active.AccessMode != "read" {
			return errors.New("写入或未知副作用任务需要串行")
		}
	}
	return rows.Err()
}
