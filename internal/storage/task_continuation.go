package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"icetudio/internal/materials"
)

type TaskContinuation struct {
	ID             string `json:"id"`
	PreviousStatus string `json:"previousStatus"`
	PreviousError  string `json:"previousError"`
	TaskID         string `json:"taskId,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	CreatedAt      string `json:"createdAt"`
}

func continuationsInTx(ctx context.Context, tx *sql.Tx, root string) ([]TaskContinuation, error) {
	items := []TaskContinuation{}
	rows, err := tx.QueryContext(ctx, `SELECT id,previous_status,previous_error,task_id,COALESCE(message_id,''),created_at FROM task_continuations WHERE root_run_id=? ORDER BY created_at,id`, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item TaskContinuation
		if err := rows.Scan(&item.ID, &item.PreviousStatus, &item.PreviousError, &item.TaskID, &item.MessageID, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// WaitingInput is deterministic: configuration order decides between ready
// gates. Independent executable work drains before a run pauses for input.
func WaitingInput(g TaskGraph) *TaskItem {
	for _, item := range ReadyTasks(g) {
		if item.Spec.InputPrompt != "" && item.InputMessageID == "" {
			copy := item
			return &copy
		}
	}
	return nil
}

// ContinueFixedRun atomically binds an answer and reopens the existing root.
// It never resets an attempt, an approval, a successful stage or a budget counter.
func (s *Store) ContinueFixedRun(ctx context.Context, root, taskID, input string, files []materials.File) (RunConfigSnapshot, time.Duration, error) {
	var profile RunConfigSnapshot
	var remaining time.Duration
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var session, status, parent, stopped, previousError string
		if err := tx.QueryRowContext(ctx, `SELECT session_id,status,COALESCE(parent_run_id,''),updated_at,error FROM runs WHERE id=?`, root).Scan(&session, &status, &parent, &stopped, &previousError); err != nil {
			return err
		}
		if parent != "" {
			return errors.New("只能续跑根任务")
		}
		g, err := taskGraphInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if g.Workflow != "fixed" {
			return errors.New("当前仅支持固定流程显式续跑")
		}
		if status != "interrupted" && !(status == "failed" && g.Coordination.State == "waiting") {
			return errors.New("只能续跑等待输入或宿主中断的固定流程")
		}
		// New tasks may change the conversation's meaning. Never reopen an older
		// task over newer work, even if that work already finished.
		var newer, active, unknown int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE session_id=? AND id<>? AND created_at>(SELECT created_at FROM runs WHERE id=?)`, session, root, root).Scan(&newer); err != nil {
			return err
		}
		if newer > 0 {
			return errors.New("会话已有后续任务，不能续跑历史任务")
		}
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE tree(id) AS(SELECT ? UNION ALL SELECT r.id FROM runs r JOIN tree ON r.parent_run_id=tree.id) SELECT count(*) FROM runs WHERE id IN tree AND status='running'`, root).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errors.New("任务仍有运行中的组件")
		}
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE tree(id) AS(SELECT ? UNION ALL SELECT r.id FROM runs r JOIN tree ON r.parent_run_id=tree.id) SELECT count(*) FROM tool_calls WHERE run_id IN tree AND state IN ('pending','approved','executing','execution_unknown')`, root).Scan(&unknown); err != nil {
			return err
		}
		var unknownSearch int
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE tree(id) AS(SELECT ? UNION ALL SELECT r.id FROM runs r JOIN tree ON r.parent_run_id=tree.id) SELECT count(*) FROM search_requests WHERE run_id IN tree AND state IN ('executing','execution_unknown')`, root).Scan(&unknownSearch); err != nil {
			return err
		}
		if unknownSearch > 0 {
			return errors.New("原生联网请求结果未知，不能续跑该任务")
		}
		if unknown > 0 {
			return errors.New("任务仍有未处置或结果未知的工具调用")
		}
		for _, item := range g.Tasks {
			if item.State != "pending" && item.State != "succeeded" {
				return errors.New("阶段已有失败、中断或未知执行尝试，不能重放；请核对记录后新建任务")
			}
		}
		wait := WaitingInput(g)
		if status == "failed" && (wait == nil || taskID != wait.Spec.ID) {
			return errors.New("请回答当前等待的阶段，阶段已变化或输入已提交")
		}
		if taskID != "" && (wait == nil || taskID != wait.Spec.ID) {
			return errors.New("输入阶段不匹配")
		}
		input = strings.TrimSpace(input)
		if len(input) > 10000 {
			return errors.New("补充内容最多 10000 字节")
		}
		if taskID != "" && input == "" && len(files) == 0 {
			return errors.New("请提供阶段所需信息或附件")
		}
		if taskID == "" && (input != "" || len(files) > 0) {
			return errors.New("当前没有选择输入阶段")
		}
		paused, err := time.Parse(time.RFC3339Nano, stopped)
		if err != nil {
			return err
		}
		deadline, err := time.Parse(time.RFC3339Nano, g.Budget.Deadline)
		if err != nil {
			return err
		}
		remaining = deadline.Sub(paused)
		if remaining <= 0 || g.Budget.ModelCalls >= g.Budget.Limits.MaxModelCalls || g.Budget.ToolCalls >= g.Budget.Limits.MaxToolCalls || (g.Budget.Limits.MaxTokens > 0 && g.Budget.Tokens >= g.Budget.Limits.MaxTokens) {
			return errors.New("原任务预算已耗尽，不能通过续跑重置预算")
		}
		resources, err := resourcesInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if resources.RootContextID == "" {
			return errors.New("缺少冻结的运行资源")
		}
		scopes := append([]string(nil), resources.Root.Scopes...)
		for _, w := range resources.Workers {
			scopes = append(scopes, w.Scopes...)
			for _, skill := range w.Skills {
				scopes = append(scopes, skill.Scopes...)
			}
		}
		var contextName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM contexts WHERE id=?`, resources.RootContextID).Scan(&contextName); err != nil {
			return err
		}
		profile = RunConfigSnapshot{ContextID: resources.RootContextID, ContextName: contextName, ConfigID: resources.Root.ID, ConfigName: resources.Root.Name, ConfigRevision: resources.Root.Revision, RuntimeConfigData: resources.Root.Data, Policy: resources.RootPolicy, ResourcesFrozen: true, Resources: &resources, FrozenSkills: resources.Root.Skills, ConfigScopes: scopes}
		var messageID any
		if taskID != "" {
			id, err := newID()
			if err != nil {
				return err
			}
			if input == "" {
				input = "请结合补充附件继续本阶段"
			}
			msg := Message{ID: id, SessionID: session, RunID: root, Role: RoleUser, Content: input, CreatedAt: now()}
			if err = addMessageInTx(ctx, tx, &msg, files); err != nil {
				return err
			}
			// Across all continuations keep the same whole-task material limits.
			rows, err := tx.QueryContext(ctx, `SELECT f.payload FROM message_files f JOIN messages m ON m.id=f.message_id WHERE m.run_id=? AND m.role='user'`, root)
			if err != nil {
				return err
			}
			count, total := 0, 0
			for rows.Next() {
				var raw string
				if err = rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				var fs []materials.File
				if err = json.Unmarshal([]byte(raw), &fs); err != nil {
					rows.Close()
					return err
				}
				for _, f := range fs {
					count++
					total += f.Size
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if count > materials.MaxFiles || total > materials.MaxTotalBytes {
				return errors.New("续跑附件累计超过每任务 8 个 / 20 MiB 上限")
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO task_inputs(root_run_id,task_id,message_id) VALUES(?,?,?)`, root, taskID, id); err != nil {
				return err
			}
			messageID = id
		}
		continuationID, err := newID()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO task_continuations(id,root_run_id,previous_status,previous_error,task_id,message_id,created_at) VALUES(?,?,?,?,?,?,?)`, continuationID, root, g.RunStatus, previousError, taskID, messageID, now()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE runs SET status='running',error='',updated_at=? WHERE id=?`, now(), root); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE root_budgets SET deadline=? WHERE root_run_id=?`, time.Now().UTC().Add(remaining).Format(time.RFC3339Nano), root); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE task_coordination SET state='pending',reason='',updated_at=? WHERE root_run_id=?`, now(), root); err != nil {
			return err
		}
		return signalCoordination(ctx, tx, root, "user.continued")
	})
	return profile, remaining, err
}
