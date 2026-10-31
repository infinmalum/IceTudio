package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Tool call lifecycle states.
const (
	CallPending          = "pending"
	CallApproved         = "approved"
	CallExecuting        = "executing"
	CallSucceeded        = "succeeded"
	CallFailed           = "failed"
	CallCancelled        = "cancelled"
	CallExecutionUnknown = "execution_unknown"
)

// toolTransitions guards the tool call lifecycle. execution_unknown is
// terminal only through explicit user disposition or an explicit native
// project-result reconciliation; late engine responses never resolve it.
// approved can still fail (the final guard may reject a call whose policy or
// parameters no longer match) but never silently back to pending.
var toolTransitions = map[string][]string{
	CallPending:          {CallApproved, CallExecuting, CallFailed, CallCancelled},
	CallApproved:         {CallExecuting, CallFailed, CallCancelled},
	CallExecuting:        {CallSucceeded, CallFailed, CallCancelled, CallExecutionUnknown},
	CallExecutionUnknown: {CallSucceeded, CallFailed, CallCancelled},
}

// Approval sources recorded on tool_calls.approval_source.
const (
	ApprovalUser = "user"
	ApprovalAuto = "auto"
)

// hashArgs fingerprints the exact arguments a call was approved with. The
// executing transition re-verifies it, so a call whose arguments no longer
// match its approval cannot be dispatched.
func hashArgs(arguments string) string {
	sum := sha256.Sum256([]byte(arguments))
	return hex.EncodeToString(sum[:])
}

// Step records one lifecycle unit of a run: a model request (with its full
// request snapshot) or a tool call dispatch. Seq is unique within the run.
type Step struct {
	ID        string `json:"id"`
	RunID     string `json:"runId"`
	Seq       int    `json:"seq"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	Payload   string `json:"payload,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ToolCall is the persisted record of one model-requested tool invocation.
// ID is generated once at creation and doubles as the persistent operation
// ID, so a retry of the same logical call never mints a new key. Approval
// facts (source, time, and the argument hash the decision was bound to) are
// recorded on the row; source_scope tags the sensitivity of a successful
// result so the session's data-exfiltration scope can be reconstructed.
type ToolCall struct {
	Material      *MaterialApproval `json:"material,omitempty"`
	MaterialError string            `json:"materialError,omitempty"`
	// Derived by the app for pending isolated search approvals, never persisted
	// or accepted as authority from model arguments.
	ApprovalDestination string `json:"approvalDestination,omitempty"`
	ID                  string `json:"id"`
	RunID               string `json:"runId"`
	StepID              string `json:"stepId"`
	MessageID           string `json:"messageId"`
	CallID              string `json:"callId"`
	ToolName            string `json:"toolName"`
	Arguments           string `json:"arguments"`
	State               string `json:"state"`
	Result              string `json:"result"`
	Error               string `json:"error"`
	ApprovalSource      string `json:"approvalSource,omitempty"`
	ApprovedAt          string `json:"approvedAt,omitempty"`
	ArgsHash            string `json:"argsHash,omitempty"`
	SourceScope         string `json:"sourceScope,omitempty"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

// MaterialApproval is a derived view of a host-sealed preparation, not a grant.
type MaterialApproval struct {
	Title       string `json:"title"`
	Revision    int64  `json:"revision"`
	Destination string `json:"destination"`
	Tool        string `json:"tool"`
	Summary     string `json:"summary"`
	SHA256      string `json:"sha256"`
	Arguments   string `json:"arguments"`
}

const toolCallColumns = `id, run_id, step_id, message_id, call_id, tool_name, arguments, state, result, error, approval_source, approved_at, args_hash, source_scope, created_at, updated_at`

func scanToolCall(scanner interface{ Scan(...any) error }) (ToolCall, error) {
	var item ToolCall
	err := scanner.Scan(&item.ID, &item.RunID, &item.StepID, &item.MessageID, &item.CallID, &item.ToolName, &item.Arguments, &item.State, &item.Result, &item.Error, &item.ApprovalSource, &item.ApprovedAt, &item.ArgsHash, &item.SourceScope, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *Store) GetToolCall(ctx context.Context, id string) (ToolCall, error) {
	return scanToolCall(s.db.QueryRowContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE id = ?`, id))
}

func (s *Store) ListRunToolCalls(ctx context.Context, runID string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE run_id = ? ORDER BY created_at, id`, runID)
	return s.scanToolCalls(rows, err)
}

func (s *Store) ListSessionToolCalls(ctx context.Context, sessionID string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.run_id, c.step_id, c.message_id, c.call_id, c.tool_name, c.arguments, c.state, c.result, c.error, c.approval_source, c.approved_at, c.args_hash, c.source_scope, c.created_at, c.updated_at
		FROM tool_calls c JOIN runs r ON r.id = c.run_id WHERE r.session_id = ? ORDER BY c.created_at, c.id`, sessionID)
	return s.scanToolCalls(rows, err)
}

func (s *Store) scanToolCalls(rows *sql.Rows, err error) ([]ToolCall, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ToolCall{}
	for rows.Next() {
		item, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// FindRunToolCall resolves a model-provided call ID within one run to its
// persisted record.
func (s *Store) FindRunToolCall(ctx context.Context, runID, callID string) (ToolCall, error) {
	return scanToolCall(s.db.QueryRowContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE run_id = ? AND call_id = ?`, runID, callID))
}

// HasUnresolvedToolCalls reports whether the session still carries tool calls
// whose outcome is not persisted. A session in this state must not start a
// new run: the model could otherwise continue a task whose side effects are
// unaccounted for.
func (s *Store) HasUnresolvedToolCalls(ctx context.Context, sessionID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_calls c JOIN runs r ON r.id = c.run_id
		WHERE r.session_id = ? AND c.state IN ('pending','approved','executing','execution_unknown')`, sessionID).Scan(&n)
	return n > 0, err
}

// BeginModelStep records a model request step with its full snapshot (model
// parameters, prompt history and tool definitions) before the request is
// sent. The step is closed by CompleteModelStep once the response arrives.
func (s *Store) BeginModelStep(ctx context.Context, runID, payload string) (Step, error) {
	id, err := newID()
	if err != nil {
		return Step{}, err
	}
	created := now()
	item := Step{ID: id, RunID: runID, Seq: 0, Kind: "model", Status: "running", Payload: payload, CreatedAt: created, UpdatedAt: created}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `INSERT INTO steps (id, run_id, seq, kind, status, error, payload, created_at, updated_at)
			SELECT ?, ?, COALESCE((SELECT MAX(seq) FROM steps WHERE run_id = ?), 0) + 1, 'model', 'running', '', ?, ?, ?
			RETURNING seq`, item.ID, runID, runID, payload, created, created).Scan(&item.Seq)
		if err != nil {
			return err
		}
		return reserveModel(ctx, tx, runID, item.ID, payload)
	})
	if err != nil {
		return Step{}, err
	}
	return item, nil
}

// CompleteModelStep closes the model request step. The snapshot is kept even
// on failure so diagnostics can show what was actually sent.
func (s *Store) CompleteModelStep(ctx context.Context, stepID string, chatErr error) error {
	status, detail := "succeeded", ""
	if chatErr != nil {
		status, detail = "failed", chatErr.Error()
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE steps SET status = ?, error = ?, updated_at = ? WHERE id = ? AND status = 'running'`, status, detail, now(), stepID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("模型请求步骤不存在或已结束")
		}
		return settleUnknownUsage(ctx, tx, stepID)
	})
}

// BeginToolCall moves a pending or approved call to executing in its own
// transaction. A non-nil error means the dispatch must not proceed: the run
// either lost its persistence guarantee, the call is not dispatchable, or
// (for an approved call) the arguments no longer match the approval.
func (s *Store) BeginToolCall(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var current, arguments, argsHash string
		if err := tx.QueryRowContext(ctx, `SELECT state, arguments, args_hash FROM tool_calls WHERE id = ?`, id).Scan(&current, &arguments, &argsHash); err != nil {
			return err
		}
		if current != CallPending && current != CallApproved {
			return fmt.Errorf("工具调用状态 %q 不能开始执行", current)
		}
		if argsHash != "" && argsHash != hashArgs(arguments) {
			return errors.New("工具参数与批准时不一致，需要重新确认")
		}
		if err := reserveTool(ctx, tx, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = 'executing', updated_at = ? WHERE id = ?`, now(), id)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("工具调用不存在或不在待执行状态")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = 'running', updated_at = ? WHERE id = (SELECT step_id FROM tool_calls WHERE id = ?)`, now(), id); err != nil {
			return err
		}
		return nil
	})
}

// ApproveToolCall records a decision to let the call proceed, either granted
// automatically by policy or explicitly by the user. The approval is bound to
// this row and to the exact arguments via a content hash.
func (s *Store) ApproveToolCall(ctx context.Context, id, source string) error {
	if source != ApprovalUser && source != ApprovalAuto {
		return fmt.Errorf("无效的审批来源 %q", source)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var current, arguments string
		if err := tx.QueryRowContext(ctx, `SELECT state, arguments FROM tool_calls WHERE id = ?`, id).Scan(&current, &arguments); err != nil {
			return err
		}
		if current != CallPending {
			return fmt.Errorf("工具调用状态 %q 不在待审批状态", current)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = 'approved', approval_source = ?, approved_at = ?, args_hash = ?, updated_at = ? WHERE id = ?`, source, now(), hashArgs(arguments), now(), id); err != nil {
			return err
		}
		return nil
	})
}

// DenyToolCall rejects a pending call on the user's behalf. The denial is a
// terminal failed outcome the model will see as a tool error.
func (s *Store) DenyToolCall(ctx context.Context, id, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		note = "未说明原因"
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM tool_calls WHERE id = ?`, id).Scan(&current); err != nil {
			return err
		}
		if current != CallPending {
			return fmt.Errorf("工具调用状态 %q 不在待审批状态", current)
		}
		errText := "用户拒绝执行（" + note + "）"
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = 'failed', error = ?, updated_at = ? WHERE id = ?`, errText, now(), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = 'failed', error = ?, updated_at = ? WHERE id = (SELECT step_id FROM tool_calls WHERE id = ?)`, errText, now(), id); err != nil {
			return err
		}
		return nil
	})
}

// CancelPendingCall expires a still-pending call, e.g. when its approval
// window timed out. Only pending calls can expire this way; anything already
// approved or dispatched keeps its own lifecycle.
func (s *Store) CancelPendingCall(ctx context.Context, id, errText string) error {
	if strings.TrimSpace(errText) == "" {
		errText = "审批超时，调用未执行"
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = 'cancelled', error = ?, updated_at = ? WHERE id = ? AND state = 'pending'`, errText, now(), id)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("工具调用不存在或不在待审批状态")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = 'cancelled', error = ?, updated_at = ? WHERE id = (SELECT step_id FROM tool_calls WHERE id = ?)`, errText, now(), id); err != nil {
			return err
		}
		return nil
	})
}

// FinishToolCall records the outcome of one call. state must be a terminal
// state reachable from the call's current state per toolTransitions. For a
// successful result sourceScope records the data sensitivity the result
// carries, so the session's exfiltration scope can be reconstructed without
// re-reading the content. When a run finishes, sweepRunArtifacts handles
// anything left behind, so an engine crash between execution and persistence
// still converges to a defined state.
func (s *Store) FinishToolCall(ctx context.Context, id, state, result, errText, sourceScope string) error {
	switch state {
	case CallSucceeded, CallFailed, CallCancelled, CallExecutionUnknown:
	default:
		return fmt.Errorf("无效的工具调用结果状态 %q", state)
	}
	if state == CallSucceeded && errText != "" {
		return errors.New("成功的工具调用不能携带错误信息")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM tool_calls WHERE id = ?`, id).Scan(&current); err != nil {
			return err
		}
		if current == CallExecutionUnknown {
			return errors.New("未知结果只能由显式用户处置，迟到响应不能改写")
		}
		allowed, ok := toolTransitions[current]
		if !ok {
			return fmt.Errorf("工具调用状态 %q 是终态，不能再次变更", current)
		}
		valid := false
		for _, next := range allowed {
			if next == state {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("工具调用状态 %q 不能变更为 %q", current, state)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = ?, result = ?, error = ?, source_scope = ?, updated_at = ? WHERE id = ?`, state, result, errText, sourceScope, now(), id); err != nil {
			return err
		}
		stepStatus := map[string]string{CallSucceeded: "succeeded", CallFailed: "failed", CallCancelled: "cancelled", CallExecutionUnknown: "paused"}[state]
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = ?, error = ?, updated_at = ? WHERE id = (SELECT step_id FROM tool_calls WHERE id = ?)`, stepStatus, errText, now(), id); err != nil {
			return err
		}
		return nil
	})
}

// ResolveToolCall records an explicit user disposition for a call whose
// outcome is unknown (or that was never dispatched). The chosen state and a
// note explaining the decision are persisted together; the note is required
// so the record never silently claims certainty.
func (s *Store) ResolveToolCall(ctx context.Context, id, state, result, note string) error {
	switch state {
	case CallSucceeded, CallFailed, CallCancelled:
	default:
		return fmt.Errorf("无效的处置状态 %q", state)
	}
	if strings.TrimSpace(note) == "" {
		return errors.New("处置结果未知调用时必须说明依据")
	}
	if state == CallSucceeded && result == "" {
		return errors.New("处置为成功时必须提供经核对的结果")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		call, err := scanToolCall(tx.QueryRowContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE id = ?`, id))
		if err != nil {
			return err
		}
		current := call.State
		if current != CallExecutionUnknown && current != CallPending {
			return fmt.Errorf("工具调用状态 %q 无需处置", current)
		}
		if IsNativeProjectWrite(call) {
			if state == CallSucceeded {
				return errors.New("原生工程成功结果必须查询实际提交记录，不能手工填写")
			}
			out, err := projectCallOutcomeTx(ctx, tx, call)
			if err != nil {
				return err
			}
			if out.Outcome == "committed" || out.Outcome == "conflict" {
				return errors.New("该操作已有提交记录，不能记录为未生效；请查询实际提交结果")
			}
		}
		errText := "用户处置：" + note
		if state == CallFailed {
			errText = "用户处置（未确认生效）：" + note
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = ?, result = ?, error = ?, updated_at = ? WHERE id = ?`, state, result, errText, now(), id); err != nil {
			return err
		}
		stepStatus := map[string]string{CallSucceeded: "succeeded", CallFailed: "failed", CallCancelled: "cancelled"}[state]
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = ?, error = ?, updated_at = ? WHERE id = (SELECT step_id FROM tool_calls WHERE id = ?)`, stepStatus, errText, now(), id); err != nil {
			return err
		}
		return nil
	})
}

// RecoverInterrupted converges every record left behind by an abrupt exit
// into an explicit state. Runs become interrupted; calls that were executing
// may have taken effect, so they become execution_unknown and block the
// session until a user disposition; calls persisted but not yet dispatched
// were never sent, so they become cancelled. Successful results are kept.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	ts := now()
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE search_requests SET state='execution_unknown',error='应用在原生搜索请求期间退出，结果与用量未知' WHERE state='executing'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET status = 'interrupted', error = '应用在任务运行期间退出', updated_at = ? WHERE status = 'running'`, ts); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = 'execution_unknown', error = '应用在工具执行期间退出，结果未知', updated_at = ? WHERE state = 'executing'`, ts); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tool_calls SET state = 'cancelled', error = '应用在分派前退出，调用未执行', updated_at = ? WHERE state IN ('pending','approved')`, ts); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = 'failed', error = '应用退出，模型请求未完成', updated_at = ? WHERE status = 'running' AND kind = 'model'`, ts); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = 'paused', error = '结果未知，等待处置', updated_at = ? WHERE status = 'running' AND kind = 'tool'`, ts); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE steps SET status = 'cancelled', error = '应用在分派前退出，调用未执行', updated_at = ? WHERE status IN ('pending') AND kind = 'tool'`, ts); err != nil {
			return err
		}
		if err := recoverTaskAttempts(ctx, tx); err != nil {
			return err
		}
		return recoverCoordination(ctx, tx)
	})
	return err
}
