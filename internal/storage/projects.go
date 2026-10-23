package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"icetudio/internal/project"
)

// Project is the stored project: content plus its current revision.
type Project struct {
	State     project.State `json:"state"`
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	CreatedAt string        `json:"createdAt"`
	UpdatedAt string        `json:"updatedAt"`
	PPQ       int           `json:"ppq"`
	Revision  int64         `json:"revision"`
}

// ProjectSummary is the list projection without the (potentially large)
// state blob.
type ProjectSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	PPQ       int    `json:"ppq"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

const stateTooLarge = "工程状态超过存储上限"

// CreateProject stores a new empty project (revision 0).
func (s *Store) CreateProject(ctx context.Context, title string, ppq int) (Project, error) {
	if len([]rune(title)) > 200 {
		return Project{}, errors.New("工程名过长")
	}
	if title == "" {
		title = "未命名工程"
	}
	state, err := project.Blank(title, ppq)
	if err != nil {
		return Project{}, err
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return Project{}, err
	}
	item := Project{ID: id, Title: title, PPQ: ppq, Revision: 0, State: state, CreatedAt: now(), UpdatedAt: now()}
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects (id, title, ppq, revision, state, created_at, updated_at) VALUES (?,?,?,0,?,?,?)`,
		id, title, ppq, string(raw), item.CreatedAt, item.UpdatedAt)
	return item, err
}

// CreateProjectFromState stores a project with pre-built content (MIDI
// import). The state's own PPQ and title are used.
func (s *Store) CreateProjectFromState(ctx context.Context, state project.State) (Project, error) {
	if state.PPQ < 1 || state.PPQ > project.MaxPPQ {
		return Project{}, fmt.Errorf("PPQ %d 超出支持范围 1–%d", state.PPQ, project.MaxPPQ)
	}
	title := state.Title
	if len([]rune(title)) > 200 {
		return Project{}, errors.New("工程名过长")
	}
	if title == "" {
		title = "未命名工程"
		state.Title = title
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return Project{}, err
	}
	if len(raw) > project.MaxStateJSONBytes {
		return Project{}, errors.New(stateTooLarge)
	}
	item := Project{ID: id, Title: title, PPQ: state.PPQ, Revision: 0, State: state, CreatedAt: now(), UpdatedAt: now()}
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects (id, title, ppq, revision, state, created_at, updated_at) VALUES (?,?,?,0,?,?,?)`,
		id, title, state.PPQ, string(raw), item.CreatedAt, item.UpdatedAt)
	return item, err
}

func (s *Store) ListProjects(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, ppq, revision, created_at, updated_at FROM projects ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProjectSummary{}
	for rows.Next() {
		var item ProjectSummary
		if err := rows.Scan(&item.ID, &item.Title, &item.PPQ, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	var item Project
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT id, title, ppq, revision, state, created_at, updated_at FROM projects WHERE id=?`, id).
		Scan(&item.ID, &item.Title, &item.PPQ, &item.Revision, &raw, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	if err := json.Unmarshal([]byte(raw), &item.State); err != nil {
		return Project{}, fmt.Errorf("工程状态损坏：%w", err)
	}
	return item, nil
}

// DeleteProject removes the project and clears session bindings in the same
// transaction; conversation history itself is untouched.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET project_id=NULL WHERE project_id=?`, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("工程不存在")
		}
		return nil
	})
}

// SetSessionProject binds or unbinds (empty projectID) a conversation to a
// project. The binding is explicit: nothing here imports or shares history.
func (s *Store) SetSessionProject(ctx context.Context, sessionID, projectID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if projectID != "" {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id=?`, projectID).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				return errors.New("工程不存在")
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE sessions SET project_id=NULLIF(?, '') WHERE id=? AND kind='normal'`, projectID, sessionID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("对话不存在")
		}
		return nil
	})
}

// SetConversationProject is the conversation-facing alias.
func (s *Store) SetConversationProject(ctx context.Context, conversationID, projectID string) error {
	return s.SetSessionProject(ctx, conversationID, projectID)
}

func scanHistory(rows *sql.Rows) ([]project.HistoryEntry, error) {
	defer rows.Close()
	items := []project.HistoryEntry{}
	for rows.Next() {
		var h project.HistoryEntry
		var ops, inverse, result string
		var undone int
		if err := rows.Scan(&h.Revision, &h.OpID, &h.Source, &h.BaseRevision, &h.Kind, &h.TargetRevision, &ops, &inverse, &result, &undone, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Undone = undone == 1
		if err := json.Unmarshal([]byte(ops), &h.Ops); err != nil {
			return nil, fmt.Errorf("历史操作损坏：%w", err)
		}
		if err := json.Unmarshal([]byte(inverse), &h.Inverse); err != nil {
			return nil, fmt.Errorf("历史逆操作损坏：%w", err)
		}
		if err := json.Unmarshal([]byte(result), &h.Result); err != nil {
			return nil, fmt.Errorf("历史结果损坏：%w", err)
		}
		items = append(items, h)
	}
	return items, rows.Err()
}

const historyColumns = `revision, op_id, source, base_revision, kind, target_revision, ops, inverse, result, undone, created_at`

func (s *Store) loadHistoryTx(ctx context.Context, tx *sql.Tx, projectID string) ([]project.HistoryEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+historyColumns+` FROM project_revisions WHERE project_id=? ORDER BY revision`, projectID)
	if err != nil {
		return nil, err
	}
	return scanHistory(rows)
}

// ProjectHistory returns the linear transaction history (newest last).
func (s *Store) ProjectHistory(ctx context.Context, projectID string) ([]project.HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+historyColumns+` FROM project_revisions WHERE project_id=? ORDER BY revision`, projectID)
	if err != nil {
		return nil, err
	}
	return scanHistory(rows)
}

// GetProjectTransaction returns the durable result of a committed
// transaction (found=true) so a caller that lost its response can confirm
// the real outcome without replaying.
func (s *Store) GetProjectTransaction(ctx context.Context, projectID, opID string) (project.Result, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT result FROM project_revisions WHERE project_id=? AND op_id=?`, projectID, opID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Result{}, false, nil
	}
	if err != nil {
		return project.Result{}, false, err
	}
	var result project.Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return project.Result{}, false, fmt.Errorf("历史结果损坏：%w", err)
	}
	return result, true, nil
}

// SubmitProjectOps commits a batch atomically: opID replay returns the
// original result; validation failures leave state and revision untouched.
// The revision and historical-identity checks below apply to every caller.
func (s *Store) SubmitProjectOps(ctx context.Context, projectID, opID, source string, baseRevision int64, ops []project.Op) (project.Result, error) {
	var result project.Result
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var stateRaw string
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT state, revision FROM projects WHERE id=?`, projectID).Scan(&stateRaw, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("工程不存在")
		}
		if err != nil {
			return err
		}
		// opID replay wins over everything: return the original result
		// without touching state (design §5.1)
		if done, err := replayTx(ctx, tx, projectID, opID); err != nil || done {
			if err != nil {
				return err
			}
			return errReplayed
		}
		if baseRevision != revision {
			return fmt.Errorf("工程已被其他操作修改（当前修订 %d，请求基于 %d）", revision, baseRevision)
		}
		var state project.State
		if err := json.Unmarshal([]byte(stateRaw), &state); err != nil {
			return fmt.Errorf("工程状态损坏：%w", err)
		}
		priorIDs, err := s.projectIdentityHistoryTx(ctx, tx, projectID, ops)
		if err != nil {
			return err
		}
		applied, inverse, res, err := project.ApplyNewTransaction(&state, ops, priorIDs)
		if err != nil {
			return err
		}
		newRaw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if len(newRaw) > project.MaxStateJSONBytes {
			return errors.New(stateTooLarge)
		}
		res.OpID, res.BaseRevision, res.Source, res.Revision = opID, baseRevision, source, revision+1
		opsJSON, err := json.Marshal(applied)
		if err != nil {
			return err
		}
		invJSON, err := json.Marshal(inverse)
		if err != nil {
			return err
		}
		resJSON, err := json.Marshal(res)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET title=?, state=?, revision=?, updated_at=? WHERE id=?`, state.Title, string(newRaw), revision+1, now(), projectID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_revisions (project_id, revision, op_id, source, base_revision, kind, target_revision, ops, inverse, result, undone, created_at) VALUES (?,?,?,?,?,'edit',0,?,?,?,0,?)`,
			projectID, revision+1, opID, source, baseRevision, string(opsJSON), string(invJSON), string(resJSON), now()); err != nil {
			return err
		}
		result = res
		return nil
	})
	if errors.Is(err, errReplayed) {
		stored, _, ferr := s.GetProjectTransaction(ctx, projectID, opID)
		return stored, ferr
	}
	if err != nil {
		return project.Result{}, err
	}
	return result, nil
}

// errReplayed marks an opID replay inside a transaction; the outer wrapper
// then reads the stored result.
var errReplayed = errors.New("操作已提交过")

// replayTx reports whether opID already has a committed result.
func replayTx(ctx context.Context, tx *sql.Tx, projectID, opID string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_revisions WHERE project_id=? AND op_id=?`, projectID, opID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// submitWithPlan is the shared body of UndoProject and RedoProject: the
// planner picks the ops, everything else is a normal transaction with the
// same opID replay semantics.
func (s *Store) submitWithPlan(ctx context.Context, projectID, opID, source string, baseRevision int64, plan func(history []project.HistoryEntry) ([]project.Op, project.HistoryEntry, error)) (project.Result, error) {
	var result project.Result
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var stateRaw string
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT state, revision FROM projects WHERE id=?`, projectID).Scan(&stateRaw, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("工程不存在")
		}
		if err != nil {
			return err
		}
		if done, err := replayTx(ctx, tx, projectID, opID); err != nil || done {
			if err != nil {
				return err
			}
			return errReplayed
		}
		history, err := s.loadHistoryTx(ctx, tx, projectID)
		if err != nil {
			return err
		}
		ops, target, err := plan(history)
		if err != nil {
			return err
		}
		if baseRevision != revision {
			return fmt.Errorf("工程已被其他操作修改（当前修订 %d，请求基于 %d）", revision, baseRevision)
		}
		var state project.State
		if err := json.Unmarshal([]byte(stateRaw), &state); err != nil {
			return fmt.Errorf("工程状态损坏：%w", err)
		}
		applied, _, res, err := project.ApplyTransactionWithHistory(&state, ops)
		if err != nil {
			return err
		}
		newRaw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if len(newRaw) > project.MaxStateJSONBytes {
			return errors.New(stateTooLarge)
		}
		res.OpID, res.BaseRevision, res.Source, res.Revision = opID, baseRevision, source, revision+1
		opsJSON, err := json.Marshal(applied)
		if err != nil {
			return err
		}
		resJSON, err := json.Marshal(res)
		if err != nil {
			return err
		}
		kind := project.RedoKind
		if target.Kind != project.UndoKind {
			kind = project.UndoKind
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET title=?, state=?, revision=?, updated_at=? WHERE id=?`, state.Title, string(newRaw), revision+1, now(), projectID); err != nil {
			return err
		}
		if kind == project.UndoKind {
			// the undone edit cannot be undone again until a redo consumes
			// this undo entry
			if _, err := tx.ExecContext(ctx, `UPDATE project_revisions SET undone=1 WHERE project_id=? AND revision=?`, projectID, target.Revision); err != nil {
				return err
			}
		} else {
			// redo re-opens the edit for a future undo (standard linear
			// undo/redo behavior)
			if _, err := tx.ExecContext(ctx, `UPDATE project_revisions SET undone=0 WHERE project_id=? AND revision=?`, projectID, target.TargetRevision); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_revisions (project_id, revision, op_id, source, base_revision, kind, target_revision, ops, inverse, result, undone, created_at) VALUES (?,?,?,?,?,?,?,?,'[]',?,0,?)`,
			projectID, revision+1, opID, source, baseRevision, kind, target.Revision, string(opsJSON), string(resJSON), now()); err != nil {
			return err
		}
		result = res
		return nil
	})
	if errors.Is(err, errReplayed) {
		stored, _, ferr := s.GetProjectTransaction(ctx, projectID, opID)
		return stored, ferr
	}
	if err != nil {
		return project.Result{}, err
	}
	return result, nil
}

// UndoProject reverses the newest not-yet-undone edit as a fresh revision.
func (s *Store) UndoProject(ctx context.Context, projectID, opID, source string, baseRevision int64) (project.Result, error) {
	return s.submitWithPlan(ctx, projectID, opID, source, baseRevision, func(history []project.HistoryEntry) ([]project.Op, project.HistoryEntry, error) {
		target, ops, err := project.PlanUndo(history)
		return ops, target, err
	})
}

// RedoProject re-applies the target of the last undo.
func (s *Store) RedoProject(ctx context.Context, projectID, opID, source string, baseRevision int64) (project.Result, error) {
	return s.submitWithPlan(ctx, projectID, opID, source, baseRevision, func(history []project.HistoryEntry) ([]project.Op, project.HistoryEntry, error) {
		target, ops, err := project.PlanRedo(history)
		return ops, target, err
	})
}
