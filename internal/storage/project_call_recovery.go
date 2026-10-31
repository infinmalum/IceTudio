package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"icetudio/internal/project"
)

// ProjectCallRecovery reports a durable operation, not the current score.
// Missing history and conflicting provenance never settle an unknown call.
type ProjectCallRecovery struct {
	CallID          string          `json:"callId"`
	ProjectID       string          `json:"projectId"`
	Title           string          `json:"title"`
	Outcome         string          `json:"outcome"`
	RunStatus       string          `json:"runStatus"`
	CurrentRevision int64           `json:"currentRevision"`
	Result          *project.Result `json:"result,omitempty"`
}

func IsNativeProjectWrite(call ToolCall) bool {
	if call.ToolName == "project__edit" {
		return true
	}
	if call.ToolName != "project__transform" {
		return false
	}
	var args struct {
		DryRun bool `json:"dryRun"`
	}
	return json.Unmarshal([]byte(call.Arguments), &args) != nil || !args.DryRun
}

// nativeProjectIdentity deliberately reads only operation identity. The edit
// payload is never applied or recompiled during recovery; removed objects and
// later revisions cannot invalidate a previously committed result.
func nativeProjectIdentity(call ToolCall, scopeRaw string) (string, string, int64, error) {
	if !IsNativeProjectWrite(call) {
		return "", "", 0, errors.New("只有内置工程编辑可查询原生提交结果")
	}
	scope, err := ParseProjectScope(scopeRaw)
	if err != nil {
		return "", "", 0, err
	}
	var a struct {
		ProjectID    string `json:"projectId"`
		OpID         string `json:"opId"`
		BaseRevision *int64 `json:"baseRevision"`
		DryRun       bool   `json:"dryRun"`
	}
	if err = json.Unmarshal([]byte(call.Arguments), &a); err != nil {
		return "", "", 0, fmt.Errorf("原调用参数损坏：%w", err)
	}
	if a.ProjectID == "" || a.OpID == "" || a.BaseRevision == nil || *a.BaseRevision < 0 || a.DryRun {
		return "", "", 0, errors.New("原调用缺少有效操作身份或属于预演，不能核对提交")
	}
	if scope.ProjectID != a.ProjectID || scope.AccessMode != "write" {
		return "", "", 0, errors.New("原调用与任务冻结的可写工程范围不一致")
	}
	return a.ProjectID, a.OpID, *a.BaseRevision, nil
}

// projectCallOutcomeTx reads all evidence in the same snapshot as a possible
// lifecycle update. It never consults the conversation's current binding.
func projectCallOutcomeTx(ctx context.Context, tx *sql.Tx, call ToolCall) (ProjectCallRecovery, error) {
	out := ProjectCallRecovery{CallID: call.ID}
	var scopeRaw string
	if err := tx.QueryRowContext(ctx, `SELECT project_scope,status FROM runs WHERE id=?`, call.RunID).Scan(&scopeRaw, &out.RunStatus); err != nil {
		return out, err
	}
	projectID, opID, base, err := nativeProjectIdentity(call, scopeRaw)
	if err != nil {
		return out, err
	}
	out.ProjectID = projectID
	err = tx.QueryRowContext(ctx, `SELECT title,revision FROM projects WHERE id=?`, projectID).Scan(&out.Title, &out.CurrentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		out.Outcome = "project_deleted"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var raw, source string
	var storedBase, storedRevision int64
	err = tx.QueryRowContext(ctx, `SELECT result,source,base_revision,revision FROM project_revisions WHERE project_id=? AND op_id=?`, projectID, opID).Scan(&raw, &source, &storedBase, &storedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		out.Outcome = "not_found"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var result project.Result
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return out, fmt.Errorf("提交结果损坏：%w", err)
	}
	if result.OpID != opID || result.Source != source || result.Source == "" || result.BaseRevision < 0 || result.BaseRevision != storedBase || result.Revision != storedRevision || result.Revision <= result.BaseRevision || result.Revision != result.BaseRevision+1 || result.Applied < 1 {
		return out, errors.New("提交结果与历史元数据不一致或内容损坏")
	}
	if result.BaseRevision != base || result.Source != "agent:"+call.RunID {
		out.Outcome = "conflict"
		return out, nil
	}
	out.Outcome = "committed"
	out.Result = &result
	return out, nil
}

// ReconcileProjectToolCall is an explicit user query. Only positive matching
// evidence may promote execution_unknown; absence remains unknown. The call
// and step converge atomically, with no music write or task resumption.
func (s *Store) ReconcileProjectToolCall(ctx context.Context, callID string) (ProjectCallRecovery, error) {
	var out ProjectCallRecovery
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		call, err := scanToolCall(tx.QueryRowContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE id=?`, callID))
		if err != nil {
			return err
		}
		if call.State != CallExecutionUnknown && call.State != CallSucceeded {
			return errors.New("该调用不是待核对或已确认成功的原生编辑")
		}
		out, err = projectCallOutcomeTx(ctx, tx, call)
		if err != nil || out.Outcome != "committed" {
			return err
		}
		if call.State == CallSucceeded {
			var recorded project.Result
			if json.Unmarshal([]byte(call.Result), &recorded) != nil || !reflect.DeepEqual(recorded, *out.Result) {
				return errors.New("已保存的调用结果与工程提交记录不一致，请保留原记录并核查")
			}
			return nil
		}
		raw, err := json.Marshal(out.Result)
		if err != nil {
			return err
		}
		note := "用户查询原生提交记录：已核对原工程、操作、来源任务与基础修订；未重放"
		if _, err = tx.ExecContext(ctx, `UPDATE tool_calls SET state='succeeded',result=?,error=?,source_scope=?,updated_at=? WHERE id=?`, string(raw), note, ScopeWorkspace, now(), callID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE steps SET status='succeeded',error=?,updated_at=? WHERE id=?`, note, now(), call.StepID)
		return err
	})
	return out, err
}
