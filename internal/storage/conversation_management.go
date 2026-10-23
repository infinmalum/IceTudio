package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) RenameConversation(ctx context.Context, id, title string) (Conversation, error) {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 100 {
		return Conversation{}, errors.New("对话名称需为 1 到 100 个字")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET title=? WHERE id=? AND kind='normal'`, title, id)
	if err != nil {
		return Conversation{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Conversation{}, errors.New("找不到对话")
	}
	return s.GetConversation(ctx, id)
}

// Delete only this conversation and its private component sessions. The
// context, permissions and execution configs may be shared and are
// deliberately retained.
func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM sessions WHERE id=?`, id).Scan(&kind); err != nil {
			return err
		}
		if kind != "normal" {
			return errors.New("请从主对话管理执行记录")
		}
		tree := `WITH RECURSIVE tree(id) AS(SELECT id FROM runs WHERE session_id=? UNION ALL SELECT r.id FROM runs r JOIN tree t ON r.parent_run_id=t.id) `
		var active int
		if err := tx.QueryRowContext(ctx, tree+`SELECT count(*) FROM runs WHERE id IN tree AND status='running'`, id).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errors.New("请先停止正在进行的任务，再删除对话")
		}
		var unknown int
		if err := tx.QueryRowContext(ctx, tree+`SELECT count(*) FROM tool_calls WHERE run_id IN tree AND state='execution_unknown'`, id).Scan(&unknown); err != nil {
			return err
		}
		if unknown > 0 {
			return errors.New("请先核对未确认的操作结果，再删除对话")
		}
		var unknownSearch int
		if err := tx.QueryRowContext(ctx, tree+`SELECT count(*) FROM search_requests WHERE run_id IN tree AND state='execution_unknown'`, id).Scan(&unknownSearch); err != nil {
			return err
		}
		if unknownSearch > 0 {
			return errors.New("联网请求尚有未确认结果，请先保留此对话以便核对")
		}
		var published int
		for _, table := range []string{"profile_drafts", "skill_drafts"} {
			if err := tx.QueryRowContext(ctx, tree+`SELECT count(*) FROM `+table+` WHERE run_id IN tree AND state='applied'`, id).Scan(&published); err != nil {
				return err
			}
			if published > 0 {
				return errors.New("此对话包含已采用的助手或工作方法建议。请先在助手管理中撤销相关建议，再删除对话，以保留有效的来源记录")
			}
		}
		// Unadopted suggestions belong to the conversation being deleted.
		for _, table := range []string{"profile_drafts", "skill_drafts", "draft_evidence"} {
			if _, err := tx.ExecContext(ctx, tree+`DELETE FROM `+table+` WHERE run_id IN tree`, id); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, tree+`SELECT DISTINCT session_id FROM runs WHERE id IN tree UNION SELECT ?`, id, id)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
			return err
		}
		for _, session := range ids {
			if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, session); err != nil {
				return err
			}
		}
		return nil
	})
}
