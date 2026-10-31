package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Feedback notes are per-conversation records of the user's own words. They
// are never written by the model, never leave their session, and die with the
// conversation (schema-level ON DELETE CASCADE).
type FeedbackNote struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Quote     string `json:"quote"`
	Note      string `json:"note"`
	RunID     string `json:"runId"`
	ProjectID string `json:"projectId"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

const feedbackNoteColumns = `id,session_id,message_id,quote,note,run_id,project_id,state,created_at,updated_at`

func scanFeedbackNote(row interface{ Scan(...any) error }) (FeedbackNote, error) {
	var n FeedbackNote
	err := row.Scan(&n.ID, &n.SessionID, &n.MessageID, &n.Quote, &n.Note, &n.RunID, &n.ProjectID, &n.State, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

// SaveFeedbackNote snapshots the user message verbatim. The message must be
// this session's own user message; task linkage prefers the message's own run
// and falls back to the caller-provided active run, and project linkage comes
// from the durable session binding — never from current UI state.
func (s *Store) SaveFeedbackNote(ctx context.Context, sessionID, messageID, activeRunID, note string) (FeedbackNote, error) {
	note = strings.TrimSpace(note)
	if len(note) > 2000 {
		return FeedbackNote{}, errors.New("反馈备注最多2000字节")
	}
	var out FeedbackNote
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var session, role, content, runID string
		err := tx.QueryRowContext(ctx, `SELECT session_id,role,content,run_id FROM messages WHERE id=?`, messageID).Scan(&session, &role, &content, &runID)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("消息不存在或已删除")
		}
		if err != nil {
			return err
		}
		if session != sessionID || role != RoleUser {
			return errors.New("只能记录本对话中自己发送的消息为反馈")
		}
		if strings.TrimSpace(content) == "" {
			return errors.New("消息没有可记录的内容")
		}
		if runID == "" && activeRunID != "" {
			var runSession string
			err = tx.QueryRowContext(ctx, `SELECT session_id FROM runs WHERE id=?`, activeRunID).Scan(&runSession)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && runSession != sessionID) {
				return errors.New("活跃任务不属于本对话")
			}
			if err != nil {
				return err
			}
			runID = activeRunID
		}
		var projectID string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(project_id,'') FROM sessions WHERE id=?`, sessionID).Scan(&projectID); err != nil {
			return err
		}
		id, err := newID()
		if err != nil {
			return err
		}
		created := now()
		out = FeedbackNote{ID: id, SessionID: sessionID, MessageID: messageID, Quote: content, Note: note, RunID: runID, ProjectID: projectID, State: "recorded", CreatedAt: created, UpdatedAt: created}
		_, err = tx.ExecContext(ctx, `INSERT INTO feedback_notes(`+feedbackNoteColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			out.ID, out.SessionID, out.MessageID, out.Quote, out.Note, out.RunID, out.ProjectID, out.State, out.CreatedAt, out.UpdatedAt)
		return err
	})
	if err != nil {
		return FeedbackNote{}, err
	}
	return out, nil
}

// DismissFeedbackNote keeps the row for audit but removes it from future
// refinement selection. Dismissing is terminal; promoted evidence keeps its
// own copy on the draft.
func (s *Store) DismissFeedbackNote(ctx context.Context, sessionID, noteID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE feedback_notes SET state='dismissed',updated_at=? WHERE id=? AND session_id=? AND state='recorded'`, now(), noteID, sessionID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("反馈不存在或已取消记录")
	}
	return err
}

func (s *Store) ListFeedbackNotes(ctx context.Context, sessionID string) ([]FeedbackNote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+feedbackNoteColumns+` FROM feedback_notes WHERE session_id=? ORDER BY created_at,id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeedbackNote{}
	for rows.Next() {
		n, e := scanFeedbackNote(rows)
		if e != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SkillEvaluation is one user-recorded outcome of actually running a music
// task with the skill enabled. It never gates or automates anything.
type SkillEvaluation struct {
	ID        string `json:"id"`
	SkillName string `json:"skillName"`
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	Outcome   string `json:"outcome"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
}

func (s *Store) SaveSkillEvaluation(ctx context.Context, skillName, sessionID, runID, outcome, note string) (SkillEvaluation, error) {
	if strings.TrimSpace(skillName) == "" || len(skillName) > 128 {
		return SkillEvaluation{}, errors.New("缺少 Skill 名称")
	}
	if outcome != "worked" && outcome != "partial" && outcome != "failed" {
		return SkillEvaluation{}, errors.New("评估结论必须是 worked、partial 或 failed")
	}
	note = strings.TrimSpace(note)
	if len(note) > 8000 {
		return SkillEvaluation{}, errors.New("评估备注最多8000字节")
	}
	var out SkillEvaluation
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id=?`, sessionID).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return errors.New("会话不存在或已删除")
		}
		if runID != "" {
			var runSession string
			if err := tx.QueryRowContext(ctx, `SELECT session_id FROM runs WHERE id=?`, runID).Scan(&runSession); err != nil {
				return errors.New("关联任务不存在")
			}
			if runSession != sessionID {
				return errors.New("关联任务不属于本对话")
			}
		}
		id, err := newID()
		if err != nil {
			return err
		}
		out = SkillEvaluation{ID: id, SkillName: skillName, SessionID: sessionID, RunID: runID, Outcome: outcome, Note: note, CreatedAt: now()}
		_, err = tx.ExecContext(ctx, `INSERT INTO skill_evaluations(id,skill_name,session_id,run_id,outcome,note,created_at) VALUES(?,?,?,?,?,?,?)`,
			out.ID, out.SkillName, out.SessionID, out.RunID, out.Outcome, out.Note, out.CreatedAt)
		return err
	})
	if err != nil {
		return SkillEvaluation{}, err
	}
	return out, nil
}

func (s *Store) ListSkillEvaluations(ctx context.Context, skillName string) ([]SkillEvaluation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,skill_name,session_id,run_id,outcome,note,created_at FROM skill_evaluations WHERE skill_name=? ORDER BY created_at DESC,id`, skillName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillEvaluation{}
	for rows.Next() {
		var e SkillEvaluation
		if err = rows.Scan(&e.ID, &e.SkillName, &e.SessionID, &e.RunID, &e.Outcome, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetSkillState persists the reversible disable switch. Whether the name is
// actually installed is validated by the app layer against the live catalog.
func (s *Store) SetSkillState(ctx context.Context, name string, disabled bool) error {
	if strings.TrimSpace(name) == "" || len(name) > 128 {
		return errors.New("缺少 Skill 名称")
	}
	flag := 0
	if disabled {
		flag = 1
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO skill_states(name,disabled,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET disabled=excluded.disabled,updated_at=excluded.updated_at`, name, flag, now())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("停用状态未写入")
	}
	return err
}

func (s *Store) ListSkillStates(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,disabled FROM skill_states WHERE disabled<>0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var flag int
		if err = rows.Scan(&name, &flag); err != nil {
			return nil, err
		}
		out[name] = flag != 0
	}
	return out, rows.Err()
}

// validateFeedbackIDsInTx binds refinement to this conversation's recorded
// notes. The draft stores the ids the service selected, never model-declared
// ones; notes stay recorded so one note can seed several candidates.
func validateFeedbackIDsInTx(ctx context.Context, tx *sql.Tx, sessionID string, ids []string) ([]string, error) {
	if len(ids) > 8 {
		return nil, errors.New("反馈最多选择8条")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var noteSession, state string
		err := tx.QueryRowContext(ctx, `SELECT session_id,state FROM feedback_notes WHERE id=?`, id).Scan(&noteSession, &state)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && noteSession != sessionID) {
			return nil, errors.New("反馈不属于本对话")
		}
		if err != nil {
			return nil, err
		}
		if state != "recorded" {
			return nil, errors.New("反馈已取消记录，不能用于提炼")
		}
		out = append(out, id)
	}
	return out, nil
}
