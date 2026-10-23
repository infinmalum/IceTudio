package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"icetudio/internal/model"
)

type SearchRequest struct {
	ID        string             `json:"id"`
	RunID     string             `json:"runId"`
	SessionID string             `json:"sessionId"`
	StepID    string             `json:"stepId"`
	Provider  string             `json:"provider"`
	State     string             `json:"state"`
	Reserved  int                `json:"reserved"`
	Report    model.SearchReport `json:"report"`
	Error     string             `json:"error"`
	CreatedAt string             `json:"createdAt"`
}

// Search reservations share the root's tool budget with all components. Unknown
// execution keeps the reservation; successful responses refund unused slots.
func (s *Store) BeginNativeSearch(ctx context.Context, run, provider string) (SearchRequest, error) {
	id, err := newID()
	if err != nil {
		return SearchRequest{}, err
	}
	item := SearchRequest{ID: id, RunID: run, Provider: provider, State: "executing", CreatedAt: now()}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		resources, err := resourcesInTx(ctx, tx, run)
		if err != nil {
			return err
		}
		if !resources.Options.WebSearch {
			return errors.New("本次任务未开启联网搜索")
		}
		root, err := rootInTx(ctx, tx, run)
		if err != nil {
			return err
		}
		b, err := budgetInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if err := checkBudget(b); err != nil {
			return err
		}
		var status, rootStatus string
		if err := tx.QueryRowContext(ctx, `SELECT session_id,status FROM runs WHERE id=?`, run).Scan(&item.SessionID, &status); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, root).Scan(&rootStatus); err != nil {
			return err
		}
		if status != "running" || rootStatus != "running" {
			return errors.New("任务已结束")
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM steps WHERE run_id=? AND kind='model' AND status='running' ORDER BY seq DESC LIMIT 1`, run).Scan(&item.StepID); err != nil {
			return err
		}
		maximum := 3
		if provider == "gemini" {
			maximum = 1
		}
		item.Report.BudgetUnit = "search_call"
		if provider == "gemini" {
			item.Report.BudgetUnit = "grounded_request"
		}
		item.Report.Events = []model.SearchEvent{}
		item.Reserved = min(maximum, b.Limits.MaxToolCalls-b.ToolCalls)
		if item.Reserved < 1 {
			return errors.New("根工具调用预算不足，不能启用原生搜索")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE root_budgets SET tool_calls=tool_calls+? WHERE root_run_id=?`, item.Reserved, root); err != nil {
			return err
		}
		payload, err := json.Marshal(item.Report)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO search_requests(id,run_id,session_id,step_id,provider,state,reserved,created_at,payload) VALUES(?,?,?,?,?,'executing',?,?,?)`, id, run, item.SessionID, item.StepID, provider, item.Reserved, item.CreatedAt, string(payload))
		return err
	})
	return item, err
}
func (s *Store) AppendSearchEvent(ctx context.Context, id string, event model.SearchEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE search_requests SET payload=json_insert(payload,'$.events[#]',json(?)) WHERE id=? AND state='executing' AND length(payload)<65536 AND COALESCE(json_array_length(payload,'$.events'),0)<256`, string(raw), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("搜索事件记录已结束或超过容量")
	}
	return nil
}
func (s *Store) FinishNativeSearch(ctx context.Context, id string, report model.SearchReport, requestErr error) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var run, state, previous string
		var reserved int
		if err := tx.QueryRowContext(ctx, `SELECT run_id,state,reserved,payload FROM search_requests WHERE id=?`, id).Scan(&run, &state, &reserved, &previous); err != nil {
			return err
		}
		if state != "executing" {
			return errors.New("搜索请求已经结束")
		}
		terminal, detail := "completed", ""
		if requestErr != nil {
			terminal = "execution_unknown"
			detail = requestErr.Error()
			report.Calls = nil
			report.BudgetUsed = nil
		}
		var earlier model.SearchReport
		if err := json.Unmarshal([]byte(previous), &earlier); err != nil {
			return err
		}
		if report.BudgetUnit == "" {
			report.BudgetUnit = earlier.BudgetUnit
		}
		if len(report.Events) == 0 {
			report.Events = earlier.Events
		}
		refund := 0
		used := report.Calls
		if report.BudgetUsed != nil {
			used = report.BudgetUsed
		}
		if used != nil {
			if *used < 0 || *used > reserved {
				return errors.New("服务商搜索次数超出预留上限")
			}
			refund = reserved - *used
		}
		raw, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if len(raw) > 512<<10 {
			return errors.New("搜索记录超过 512 KiB")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE search_requests SET state=?,payload=?,error=? WHERE id=?`, terminal, string(raw), detail, id); err != nil {
			return err
		}
		if refund > 0 {
			root, err := rootInTx(ctx, tx, run)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE root_budgets SET tool_calls=tool_calls-? WHERE root_run_id=?`, refund, root)
			return err
		}
		return nil
	})
}
func (s *Store) ListConversationSearch(ctx context.Context, session string) ([]SearchRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,session_id,step_id,provider,state,reserved,payload,error,created_at FROM search_requests WHERE session_id=? ORDER BY created_at,id`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchRequest{}
	for rows.Next() {
		var item SearchRequest
		var raw string
		if err := rows.Scan(&item.ID, &item.RunID, &item.SessionID, &item.StepID, &item.Provider, &item.State, &item.Reserved, &raw, &item.Error, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.Report); err != nil {
			return nil, fmt.Errorf("解析搜索记录: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
