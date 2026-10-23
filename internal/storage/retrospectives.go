package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"icetudio/internal/config"
	"icetudio/internal/materials"
	"strings"
)

type RetrospectiveObservation struct {
	Reference   string `json:"reference"`
	Description string `json:"description"`
}
type RetrospectiveResult struct {
	Summary         string                     `json:"summary"`
	Observations    []RetrospectiveObservation `json:"observations"`
	Recommendations []string                   `json:"recommendations"`
	Limitations     []string                   `json:"limitations"`
}

func (r RetrospectiveResult) Validate() error {
	if strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 16000 || len(r.Observations) > 64 || len(r.Recommendations) > 32 || len(r.Limitations) > 32 {
		return errors.New("无效复盘结论")
	}
	for _, o := range r.Observations {
		if strings.TrimSpace(o.Reference) == "" || len(o.Reference) > 500 || strings.TrimSpace(o.Description) == "" || len(o.Description) > 8000 {
			return errors.New("无效复盘观察")
		}
	}
	for _, list := range [][]string{r.Recommendations, r.Limitations} {
		for _, text := range list {
			if strings.TrimSpace(text) == "" || len(text) > 8000 {
				return errors.New("无效复盘建议或限制")
			}
		}
	}
	return nil
}

type TaskRetrospective struct {
	ID            string              `json:"id"`
	RootRunID     string              `json:"rootRunId"`
	ReviewerRunID string              `json:"reviewerRunId"`
	Binding       string              `json:"binding"`
	Result        RetrospectiveResult `json:"result"`
	Current       bool                `json:"current"`
	CreatedAt     string              `json:"createdAt"`
}
type RetrospectiveRun struct {
	ParentRunID    string       `json:"parentRunId,omitempty"`
	ConfigID       string       `json:"profileId"`
	ConfigRevision int          `json:"profileRevision"`
	Model          config.Model `json:"model"`
	CreatedAt      string       `json:"createdAt"`
	UpdatedAt      string       `json:"updatedAt"`

	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	Component  string `json:"component"`
	WorkerName string `json:"workerName"`
	Status     string `json:"status"`
	Error      string `json:"error"`
}
type RetrospectiveMessage struct {
	Attachments []materials.File `json:"attachments,omitempty"`
	ID          string           `json:"id"`
	RunID       string           `json:"runId"`
	Role        string           `json:"role"`
	Content     string           `json:"content"`
}
type RetrospectiveSnapshot struct {
	Binding  string                 `json:"binding"`
	Contract json.RawMessage        `json:"contract"`
	Graph    TaskGraph              `json:"graph"`
	Runs     []RetrospectiveRun     `json:"runs"`
	Steps    []Step                 `json:"steps"`
	Messages []RetrospectiveMessage `json:"messages"`
	Calls    []ToolCall             `json:"calls"`
}

const retrospectiveTree = `WITH RECURSIVE tree(id) AS(SELECT ? UNION ALL SELECT r.id FROM runs r JOIN tree ON r.parent_run_id=tree.id) `

func retrospectiveSnapshotInTx(ctx context.Context, tx *sql.Tx, root string) (RetrospectiveSnapshot, error) {
	out := RetrospectiveSnapshot{Runs: []RetrospectiveRun{}, Steps: []Step{}, Messages: []RetrospectiveMessage{}, Calls: []ToolCall{}}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT config_snapshot FROM runs WHERE id=? AND parent_run_id IS NULL AND status<>'running'`, root).Scan(&raw); err != nil {
		return out, err
	}
	out.Contract = json.RawMessage(raw)
	var err error
	out.Graph, err = taskGraphInTx(ctx, tx, root)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, retrospectiveTree+`SELECT id,session_id,component,worker_name,status,error,COALESCE(parent_run_id,''),config_id,config_revision,config_snapshot,created_at,updated_at FROM runs WHERE id IN tree ORDER BY created_at,id`, root)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r RetrospectiveRun
		var profileRaw string
		if err = rows.Scan(&r.ID, &r.SessionID, &r.Component, &r.WorkerName, &r.Status, &r.Error, &r.ParentRunID, &r.ConfigID, &r.ConfigRevision, &profileRaw, &r.CreatedAt, &r.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		var frozen RuntimeConfigData
		if err = json.Unmarshal([]byte(profileRaw), &frozen); err != nil {
			rows.Close()
			return out, err
		}
		r.Model = frozen.Model
		if r.Status == "running" {
			rows.Close()
			return out, errors.New("任务组件仍在结束中，请稍后复盘")
		}
		out.Runs = append(out.Runs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Model request payloads repeat the complete context. Use their actual
	// lifecycle metadata and the original messages/tools instead of duplication.
	rows, err = tx.QueryContext(ctx, retrospectiveTree+`SELECT id,run_id,seq,kind,status,error,created_at,updated_at FROM steps WHERE run_id IN tree ORDER BY created_at,run_id,seq`, root)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var step Step
		if err = rows.Scan(&step.ID, &step.RunID, &step.Seq, &step.Kind, &step.Status, &step.Error, &step.CreatedAt, &step.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Steps = append(out.Steps, step)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, retrospectiveTree+`SELECT m.id,m.run_id,m.role,m.content,COALESCE((SELECT payload FROM message_files WHERE message_id=m.id),'[]') FROM messages m JOIN runs r ON r.id=m.run_id WHERE m.run_id IN tree AND m.role IN ('user','assistant') AND NOT (m.role='user' AND r.component IN ('planner','reviewer')) ORDER BY m.created_at,m.seq`, root)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m RetrospectiveMessage
		var filesRaw string
		if err = rows.Scan(&m.ID, &m.RunID, &m.Role, &m.Content, &filesRaw); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(filesRaw), &m.Attachments); err != nil {
			rows.Close()
			return out, err
		}
		for i, f := range m.Attachments {
			m.Attachments[i] = f.Metadata()
		}
		out.Messages = append(out.Messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, retrospectiveTree+`SELECT `+toolCallColumns+` FROM tool_calls WHERE run_id IN tree ORDER BY created_at,id`, root)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		c, e := scanToolCall(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Calls = append(out.Calls, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rawSnapshot, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(rawSnapshot)
	out.Binding = hex.EncodeToString(sum[:])
	return out, nil
}
func (s *Store) RetrospectiveSnapshot(ctx context.Context, root string) (RetrospectiveSnapshot, error) {
	var snap RetrospectiveSnapshot
	err := s.withTx(ctx, func(tx *sql.Tx) error { var e error; snap, e = retrospectiveSnapshotInTx(ctx, tx, root); return e })
	return snap, err
}
func (s *Store) SaveTaskRetrospective(ctx context.Context, root, reviewer, binding string, result RetrospectiveResult) (TaskRetrospective, error) {
	id, err := newID()
	if err != nil {
		return TaskRetrospective{}, err
	}
	out := TaskRetrospective{ID: id, RootRunID: root, ReviewerRunID: reviewer, Binding: binding, Result: result, CreatedAt: now()}
	if err = result.Validate(); err != nil {
		return out, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var target string
		if err := tx.QueryRowContext(ctx, `SELECT q.target_run_id FROM runs r JOIN retrospective_requests q ON q.run_id=r.parent_run_id JOIN runs p ON p.id=q.run_id WHERE r.id=? AND r.component='reviewer' AND r.status='running' AND p.status='running'`, reviewer).Scan(&target); err != nil {
			return err
		}
		if target != root {
			return errors.New("复盘目标不匹配")
		}
		snap, err := retrospectiveSnapshotInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if snap.Binding != binding {
			return errors.New("复盘过程记录已变化，请重新复盘")
		}
		refs := map[string]bool{}
		for _, r := range snap.Runs {
			refs["run:"+r.ID] = true
		}
		for _, s := range snap.Steps {
			refs["step:"+s.ID] = true
		}
		for _, m := range snap.Messages {
			refs["message:"+m.ID] = true
		}
		for _, c := range snap.Calls {
			refs["call:"+c.ID] = true
		}
		for _, t := range snap.Graph.Tasks {
			refs["task:"+t.Spec.ID] = true
		}
		for _, a := range snap.Graph.Artifacts {
			refs["artifact:"+a.ID] = true
		}
		for _, o := range result.Observations {
			if !refs[o.Reference] {
				return errors.New("复盘观察引用不存在")
			}
		}
		raw, _ := json.Marshal(result)
		_, err = tx.ExecContext(ctx, `INSERT INTO task_retrospectives(id,root_run_id,reviewer_run_id,binding,result,created_at) VALUES(?,?,?,?,?,?)`, out.ID, root, reviewer, binding, string(raw), out.CreatedAt)
		out.Current = err == nil
		return err
	})
	return out, err
}
func (s *Store) ListTaskRetrospectives(ctx context.Context, root string) ([]TaskRetrospective, error) {
	out := []TaskRetrospective{}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var target string
		e := tx.QueryRowContext(ctx, `SELECT target_run_id FROM retrospective_requests WHERE run_id=?`, root).Scan(&target)
		if e == nil {
			root = target
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		snap, snapErr := retrospectiveSnapshotInTx(ctx, tx, root)
		rows, err := tx.QueryContext(ctx, `SELECT id,root_run_id,reviewer_run_id,binding,result,created_at FROM task_retrospectives WHERE root_run_id=? ORDER BY created_at,id`, root)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r TaskRetrospective
			var raw string
			if err = rows.Scan(&r.ID, &r.RootRunID, &r.ReviewerRunID, &r.Binding, &raw, &r.CreatedAt); err != nil {
				return err
			}
			if err = json.Unmarshal([]byte(raw), &r.Result); err != nil {
				return err
			}
			r.Current = snapErr == nil && r.Binding == snap.Binding
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}
