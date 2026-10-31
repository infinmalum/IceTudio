package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// SealPreparedMaterial records host evidence before the tool result returns.
// A manually resolved unknown call cannot manufacture a prepared snapshot.
func (s *Store) SealPreparedMaterial(ctx context.Context, callID, result string) error {
	seal, _ := json.Marshal(map[string]string{"preparedMaterialSHA256": hashArgs(result)})
	res, err := s.db.ExecContext(ctx, `UPDATE steps SET payload=? WHERE payload='' AND status='running' AND id IN
		(SELECT step_id FROM tool_calls WHERE id=? AND tool_name='project__prepare_material' AND state='executing')`, string(seal), callID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		err = errors.New("材料准备步骤已结束或已封存")
	}
	return err
}

func preparedMaterialInTx(ctx context.Context, tx *sql.Tx, runID, id string) (ToolCall, error) {
	call, err := scanToolCall(tx.QueryRowContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE id=? AND run_id=?`, id, runID))
	if err != nil {
		return call, err
	}
	var seal string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM steps WHERE id=?`, call.StepID).Scan(&seal); err != nil {
		return call, err
	}
	var evidence struct {
		Hash string `json:"preparedMaterialSHA256"`
	}
	if call.ToolName != "project__prepare_material" || call.State != CallSucceeded || json.Unmarshal([]byte(seal), &evidence) != nil || evidence.Hash != hashArgs(call.Result) {
		return call, errors.New("材料引用不是本任务成功且由宿主封存的准备结果")
	}
	return call, nil
}

func (s *Store) PreparedProjectMaterial(ctx context.Context, runID, id string) (ToolCall, error) {
	var call ToolCall
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		call, err = preparedMaterialInTx(ctx, tx, runID, id)
		return err
	})
	return call, err
}

// ClaimMaterialDispatch is durable and one-use, including across call IDs and
// restart. Preparing a new reference and approving a new call is explicit work.
func (s *Store) ClaimMaterialDispatch(ctx context.Context, runID, callID, preparationID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := preparedMaterialInTx(ctx, tx, runID, preparationID); err != nil {
			return err
		}
		call, err := scanToolCall(tx.QueryRowContext(ctx, `SELECT `+toolCallColumns+` FROM tool_calls WHERE id=? AND run_id=?`, callID, runID))
		if err != nil {
			return err
		}
		var args struct {
			ID string `json:"preparationId"`
		}
		if call.ToolName != "local__send_material" || call.State != CallExecuting || call.ApprovalSource != ApprovalUser || call.ArgsHash != hashArgs(call.Arguments) || json.Unmarshal([]byte(call.Arguments), &args) != nil || args.ID != preparationID {
			return errors.New("材料发送缺少原调用用户批准")
		}
		root, err := rootInTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		var ended int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE id IN (?,?) AND status<>'running'`, root, runID).Scan(&ended); err != nil {
			return err
		}
		if ended != 0 {
			return errors.New("任务已结束")
		}
		budget, err := budgetInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if err = checkBudget(budget); err != nil {
			return err
		}
		var prior int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM steps s JOIN tool_calls c ON c.step_id=s.id WHERE c.run_id=? AND c.tool_name='local__send_material' AND json_valid(s.payload) AND json_extract(s.payload,'$.materialDispatch')=?`, runID, preparationID).Scan(&prior); err != nil {
			return err
		}
		if prior > 0 {
			return errors.New("此材料已分派，结果可能未知；先核对外部服务，不自动重放")
		}
		payload, _ := json.Marshal(map[string]string{"materialDispatch": preparationID})
		res, err := tx.ExecContext(ctx, `UPDATE steps SET payload=? WHERE id=? AND payload='' AND status='running'`, string(payload), call.StepID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err == nil && n != 1 {
			return errors.New("材料发送步骤不可分派")
		}
		return err
	})
}
