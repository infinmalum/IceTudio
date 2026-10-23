package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// TaskSpec is a declared execution contract. Acceptance is a requested check,
// not a review verdict. Dependency results remain in their original messages.
type TaskSpec struct {
	InputPrompt string   `json:"inputPrompt,omitempty"`
	AccessMode  string   `json:"accessMode,omitempty"`
	Resources   []string `json:"resources,omitempty"`
	Replaces    string   `json:"replaces,omitempty"`
	ID          string   `json:"id"`
	Worker      string   `json:"worker"`
	Task        string   `json:"task"`
	Materials   string   `json:"materials,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
}
type TaskItem struct {
	InputMessageID string   `json:"inputMessageId,omitempty"`
	Input          string   `json:"input,omitempty"`
	Spec           TaskSpec `json:"spec"`
	Fingerprint    string   `json:"fingerprint"`
	State          string   `json:"state"`
}
type TaskAttempt struct {
	Error     string `json:"error,omitempty"`
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	RunID     string `json:"runId"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
type TaskGraph struct {
	Workflow      string             `json:"workflow"`
	WaitingTaskID string             `json:"waitingTaskId,omitempty"`
	WaitingPrompt string             `json:"waitingPrompt,omitempty"`
	RootRunID     string             `json:"rootRunId"`
	Goal          string             `json:"goal"`
	Revision      int                `json:"revision"`
	RunStatus     string             `json:"runStatus"`
	Coordination  CoordinationState  `json:"coordination"`
	Continuations []TaskContinuation `json:"continuations"`
	Budget        BudgetState        `json:"budget"`
	Artifacts     []Artifact         `json:"artifacts"`
	Tasks         []TaskItem         `json:"tasks"`
	Attempts      []TaskAttempt      `json:"attempts"`
}

func taskGraphInTx(ctx context.Context, tx *sql.Tx, root string) (TaskGraph, error) {
	g := TaskGraph{RootRunID: root, Tasks: []TaskItem{}, Attempts: []TaskAttempt{}}
	if err := tx.QueryRowContext(ctx, `SELECT t.goal,t.revision,CASE WHEN r.status='failed' AND c.state IN ('waiting','blocked') THEN c.state ELSE r.status END,CASE WHEN json_valid(r.config_snapshot) THEN COALESCE(json_extract(r.config_snapshot,'$.execution.workflow'),'') ELSE '' END FROM task_roots t JOIN runs r ON r.id=t.run_id JOIN task_coordination c ON c.root_run_id=t.run_id WHERE t.run_id=?`, root).Scan(&g.Goal, &g.Revision, &g.RunStatus, &g.Workflow); err != nil {
		return g, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.spec,i.state,COALESCE(m.id,''),COALESCE(m.content,'') FROM task_items i LEFT JOIN task_inputs u ON u.root_run_id=i.root_run_id AND u.task_id=i.id LEFT JOIN messages m ON m.id=u.message_id WHERE i.root_run_id=? ORDER BY i.position`, root)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var raw string
		var item TaskItem
		if err := rows.Scan(&raw, &item.State, &item.InputMessageID, &item.Input); err != nil {
			rows.Close()
			return g, err
		}
		if err := json.Unmarshal([]byte(raw), &item.Spec); err != nil {
			rows.Close()
			return g, err
		}
		item.Fingerprint = TaskFingerprint(item.Spec)
		g.Tasks = append(g.Tasks, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT a.id,a.task_id,a.run_id,a.status,a.created_at,a.updated_at,r.error FROM task_attempts a JOIN runs r ON r.id=a.run_id WHERE a.root_run_id=? ORDER BY a.created_at,a.id`, root)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var a TaskAttempt
		if err := rows.Scan(&a.ID, &a.TaskID, &a.RunID, &a.Status, &a.CreatedAt, &a.UpdatedAt, &a.Error); err != nil {
			rows.Close()
			return g, err
		}
		g.Attempts = append(g.Attempts, a)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		g.Coordination, err = coordinationInTx(ctx, tx, root)
	}
	if err == nil {
		g.Budget, err = budgetInTx(ctx, tx, root)
	}
	if err == nil {
		g.Artifacts, err = artifactsInTx(ctx, tx, root)
	}
	if err == nil {
		g.Continuations, err = continuationsInTx(ctx, tx, root)
	}
	if g.Workflow == "fixed" {
		if wait := WaitingInput(g); wait != nil {
			g.WaitingTaskID = wait.Spec.ID
			g.WaitingPrompt = wait.Spec.InputPrompt
		}
	}
	return g, err
}
func (s *Store) GetTaskGraph(ctx context.Context, root string) (TaskGraph, error) {
	var g TaskGraph
	err := s.withTx(ctx, func(tx *sql.Tx) error { var err error; g, err = taskGraphInTx(ctx, tx, root); return err })
	return g, err
}

func (s *Store) SetTaskGraph(ctx context.Context, root string, expected int, specs []TaskSpec) (TaskGraph, error) {
	var result TaskGraph
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		g, err := taskGraphInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if g.RunStatus != "running" {
			return errors.New("根任务已经结束，不能修改执行计划")
		}
		if g.Revision != expected {
			raw, _ := json.Marshal(g)
			return fmt.Errorf("执行计划修订冲突；当前状态 %s", raw)
		}
		resources, err := resourcesInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if resources.Root.Data.Execution.Workflow == "fixed" {
			return errors.New("固定流程阶段由起始配置初始化，不能改写")
		}
		if !resources.Root.Data.PlanEnabled || !resources.Root.Data.Delegation.Enabled {
			return errors.New("任务起始配置未启用规划和 worker 委派")
		}
		if err := validateTaskContracts(ctx, tx, g, resources, specs); err != nil {
			return err
		}
		steps := make([]PlanStep, 0, len(specs))
		seen := map[string]TaskSpec{}
		total := 0
		for _, spec := range specs {
			if spec.ID == "" || strings.TrimSpace(spec.Task) == "" || len(spec.Task) > 10000 || len(spec.Materials) > 65536 {
				return errors.New("任务需要稳定 ID，描述最多 10000 字节，材料最多 65536 字节")
			}
			if _, ok := resources.Workers[spec.Worker]; !ok {
				return errors.New("执行任务必须选择起始快照中的 worker 槽位")
			}
			total += len(spec.Task) + len(spec.Materials) + len(spec.Acceptance)
			if total > 1024*1024 {
				return errors.New("执行计划材料总量超过 1 MiB")
			}
			seen[spec.ID] = spec
			steps = append(steps, PlanStep{ID: spec.ID, Title: spec.ID, Status: PlanPending, DependsOn: spec.DependsOn, Acceptance: spec.Acceptance})
		}
		if err := ValidatePlanSteps(steps); err != nil {
			return err
		}
		states := map[string]string{}
		for _, old := range g.Tasks {
			states[old.Spec.ID] = old.State
			if old.State == "pending" {
				continue
			}
			n, ok := seen[old.Spec.ID]
			if !ok || !sameTaskSpec(old.Spec, n) {
				return errors.New("不能删除或改写已有执行尝试的任务；返工请使用新的任务 ID")
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_items WHERE root_run_id=? AND state='pending'`, root); err != nil {
			return err
		}
		for i, spec := range specs {
			raw, err := json.Marshal(spec)
			if err != nil {
				return err
			}
			state := states[spec.ID]
			if state == "" {
				state = "pending"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_items(root_run_id,id,position,spec,state) VALUES(?,?,?,?,?) ON CONFLICT(root_run_id,id) DO UPDATE SET position=excluded.position`, root, spec.ID, i, string(raw), state); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(specs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_plan_revisions(root_run_id,revision,plan,created_at) VALUES(?,?,?,?)`, root, g.Revision+1, string(raw), now()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE task_roots SET revision=revision+1 WHERE run_id=?`, root); err != nil {
			return err
		}
		result, err = taskGraphInTx(ctx, tx, root)
		return err
	})
	return result, err
}
func sameTaskSpec(a, b TaskSpec) bool {
	return a.AccessMode == b.AccessMode && a.Replaces == b.Replaces && samePlanRefs(a.Resources, b.Resources) && a.ID == b.ID && a.Worker == b.Worker && a.Task == b.Task && a.Materials == b.Materials && a.Acceptance == b.Acceptance && samePlanRefs(a.DependsOn, b.DependsOn)
}

// claimTaskInTx is part of CreateRunWithOptions, so there is no persisted
// claim without a Run, or Run without its claim. Tasks are never auto-retried.
func claimTaskInTx(ctx context.Context, tx *sql.Tx, root string, spec TaskSpec, runID, worker string) error {
	g, err := taskGraphInTx(ctx, tx, root)
	if err != nil {
		return err
	}
	if g.RunStatus != "running" {
		return errors.New("根任务已结束")
	}
	states := map[string]string{}
	var selected *TaskItem
	for i := range g.Tasks {
		item := &g.Tasks[i]
		states[item.Spec.ID] = item.State
		if item.Spec.ID == spec.ID {
			selected = item
		}
	}
	if selected == nil || !sameTaskSpec(selected.Spec, spec) || selected.Spec.Worker != worker {
		return errors.New("任务不存在或执行条件已改变，请重新读取计划")
	}
	if selected.State != "pending" {
		return errors.New("任务已被领取或结束，禁止重复执行")
	}
	for _, dep := range selected.Spec.DependsOn {
		if !taskSatisfied(g, dep) {
			return errors.New("前置执行任务尚未成功")
		}
	}
	if err := validateClaim(ctx, tx, g, spec, runID); err != nil {
		return err
	}
	resources, err := resourcesInTx(ctx, tx, root)
	if err != nil {
		return err
	}
	limit := resources.Root.Data.Delegation.MaxConcurrency
	if limit <= 0 {
		limit = DefaultDelegationConcurrency
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE parent_run_id=? AND status='running' AND id<>? AND component='worker'`, root, runID).Scan(&active); err != nil {
		return err
	}
	if active >= limit {
		return errors.New("并发子任务已达上限")
	}
	id, err := newID()
	if err != nil {
		return err
	}
	ts := now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_attempts(id,root_run_id,task_id,run_id,status,created_at,updated_at) VALUES(?,?,?,?,'running',?,?)`, id, root, spec.ID, runID, ts, ts); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_items SET state='running' WHERE root_run_id=? AND id=? AND state='pending'`, root, spec.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_roots SET revision=revision+1 WHERE run_id=?`, root)
	return err
}

func settleTaskAttempt(ctx context.Context, tx *sql.Tx, runID string) error {
	var root, task, status string
	err := tx.QueryRowContext(ctx, `SELECT a.root_run_id,a.task_id,r.status FROM task_attempts a JOIN runs r ON r.id=a.run_id WHERE a.run_id=? AND a.status='running'`, runID).Scan(&root, &task, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var unknown int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tool_calls WHERE run_id=? AND state='execution_unknown'`, runID).Scan(&unknown); err != nil {
		return err
	}
	if unknown > 0 {
		status = "execution_unknown"
	} else if status == "completed" {
		status = "succeeded"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_attempts SET status=?,updated_at=? WHERE run_id=? AND status='running'`, status, now(), runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_items SET state=? WHERE root_run_id=? AND id=?`, status, root, task); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_roots SET revision=revision+1 WHERE run_id=?`, root)
	if err == nil {
		err = recordArtifact(ctx, tx, root, task, runID, status)
	}
	if err == nil {
		err = signalCoordination(ctx, tx, root, "task.finished")
	}
	return err
}

func settleTaskRoot(ctx context.Context, tx *sql.Tx, root string) error {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT r.status FROM task_roots t JOIN runs r ON r.id=t.run_id WHERE t.run_id=?`, root).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status == "running" {
		return nil
	}
	if status == "completed" {
		g, err := taskGraphInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		incomplete := false
		for _, item := range g.Tasks {
			if !taskSatisfied(g, item.Spec.ID) {
				incomplete = true
			}
		}
		if incomplete {
			if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='failed',error='执行计划仍有未成功任务，不能标记完成' WHERE id=?`, root); err != nil {
				return err
			}
		}
	}

	preserve := false
	g, err := taskGraphInTx(ctx, tx, root)
	if err != nil {
		return err
	}
	if g.Workflow == "fixed" && (status == "interrupted" || (status == "failed" && g.Coordination.State == "waiting")) {
		preserve = true
		for _, item := range g.Tasks {
			if item.State != "pending" && item.State != "succeeded" {
				preserve = false
			}
		}
	}
	if !preserve {
		if _, err := tx.ExecContext(ctx, `UPDATE task_items SET state='cancelled' WHERE root_run_id=? AND state='pending'`, root); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE task_roots SET revision=revision+1 WHERE run_id=?`, root); err != nil {
		return err
	}
	var actual, detail, coordState string
	if err := tx.QueryRowContext(ctx, `SELECT r.status,r.error,c.state FROM runs r JOIN task_coordination c ON c.root_run_id=r.id WHERE r.id=?`, root).Scan(&actual, &detail, &coordState); err != nil {
		return err
	}
	next := actual
	if actual == "completed" {
		next = "complete"
	}
	if actual == "interrupted" {
		next = "blocked"
		detail = "宿主中断，保留事件和尝试；处置未知结果后明确继续"
	}
	if actual == "failed" && (coordState == "waiting" || coordState == "blocked") {
		next = coordState
		detail = ""
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_coordination SET state=?,reason=CASE WHEN ?='' THEN reason ELSE ? END,updated_at=? WHERE root_run_id=?`, next, detail, detail, now(), root); err != nil {
		return err
	}
	return signalCoordination(ctx, tx, root, "root."+actual)
}

// Recovery follows existing tool disposition. It never releases a claim or
// starts a fresh attempt, even when the interruption happened before dispatch.
func recoverTaskAttempts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT run_id FROM task_attempts WHERE status='running'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := settleTaskAttempt(ctx, tx, id); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT t.run_id FROM task_roots t JOIN runs r ON r.id=t.run_id WHERE r.status='interrupted' AND EXISTS(SELECT 1 FROM task_items WHERE root_run_id=t.run_id AND state='pending') AND NOT EXISTS(SELECT 1 FROM task_events e WHERE e.root_run_id=t.run_id AND e.kind='root.interrupted' AND e.created_at>=r.updated_at)`)
	if err != nil {
		return err
	}
	ids = nil
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := settleTaskRoot(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func TaskFingerprint(spec TaskSpec) string {
	spec.DependsOn = append([]string(nil), spec.DependsOn...)
	slices.Sort(spec.DependsOn)
	spec.Resources = append([]string(nil), spec.Resources...)
	slices.Sort(spec.Resources)
	raw, _ := json.Marshal(spec)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
