package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"icetudio/internal/materials"
	"strings"
	"time"
)

type ReviewPolicy struct {
	Mode           string   `json:"mode"`
	RequiredChecks []string `json:"requiredChecks"`
}

func (p ReviewPolicy) Validate() error {
	if p.Mode != "" && p.Mode != "off" && p.Mode != "on_demand" && p.Mode != "required" {
		return errors.New("审查模式必须为 off/on_demand/required")
	}
	if len(p.RequiredChecks) > 32 {
		return errors.New("必要检查最多 32 项")
	}
	seen := map[string]bool{}
	for _, name := range p.RequiredChecks {
		if strings.TrimSpace(name) != name || name == "" || len(name) > 200 || seen[name] {
			return errors.New("必要检查应为唯一的工具名称")
		}
		seen[name] = true
	}
	return nil
}

type ReviewDefect struct {
	Reference   string `json:"reference"`
	Description string `json:"description"`
}
type ReviewResult struct {
	Status          string         `json:"status"`
	Summary         string         `json:"summary"`
	Defects         []ReviewDefect `json:"defects"`
	EvidenceCallIDs []string       `json:"evidenceCallIds"`
}

func (r ReviewResult) Validate() error {
	if r.Status != "passed" && r.Status != "changes_requested" && r.Status != "inconclusive" {
		return errors.New("无效审查结论")
	}
	if strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 16000 || len(r.Defects) > 64 || len(r.EvidenceCallIDs) > 64 {
		return errors.New("审查结论为空或过长")
	}
	if r.Status == "changes_requested" && len(r.Defects) == 0 {
		return errors.New("要求修改时必须给出缺陷引用")
	}
	if r.Status == "passed" && len(r.Defects) > 0 {
		return errors.New("含未解决缺陷不能通过")
	}
	for _, d := range r.Defects {
		if strings.TrimSpace(d.Reference) == "" || strings.TrimSpace(d.Description) == "" || len(d.Reference) > 500 || len(d.Description) > 8000 {
			return errors.New("缺陷引用无效")
		}
	}
	return nil
}

type TaskReview struct {
	ID            string       `json:"id"`
	RootRunID     string       `json:"rootRunId"`
	ReviewerRunID string       `json:"reviewerRunId"`
	Binding       string       `json:"binding"`
	Result        ReviewResult `json:"result"`
	Current       bool         `json:"current"`
	CreatedAt     string       `json:"createdAt"`
}

type StageInput struct {
	TaskID    string `json:"taskId"`
	Prompt    string `json:"prompt"`
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
}

// Snapshot includes original records, never model-invented evidence. The binding
// covers goal, immutable execution contract, delivered message, artifacts and
// complete tool evidence, including resolution of previously unknown effects.
type ReviewSnapshot struct {
	Inputs      []StageInput     `json:"inputs,omitempty"`
	Attachments []materials.File `json:"attachments,omitempty"`
	Binding     string           `json:"binding"`
	ChecksAfter string           `json:"checksAfter,omitempty"`
	Goal        string           `json:"goal"`
	Tasks       []TaskSpec       `json:"tasks"`
	Delivery    Message          `json:"delivery"`
	Artifacts   []Artifact       `json:"artifacts"`
	Calls       []ToolCall       `json:"calls"`
	Policy      ReviewPolicy     `json:"policy"`
}

func reviewSnapshotInTx(ctx context.Context, tx *sql.Tx, root string) (ReviewSnapshot, error) {
	var out ReviewSnapshot
	var raw, parent string
	if err := tx.QueryRowContext(ctx, `SELECT config_snapshot,COALESCE(parent_run_id,'') FROM runs WHERE id=?`, root).Scan(&raw, &parent); err != nil {
		return out, err
	}
	if parent != "" {
		return out, errors.New("只能审查根任务")
	}
	var profile RuntimeConfigData
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		return out, err
	}
	out.Policy = profile.Execution.Review
	g, err := taskGraphInTx(ctx, tx, root)
	if err != nil {
		return out, err
	}
	out.Goal = g.Goal
	out.Artifacts = g.Artifacts
	if out.Goal == "" {
		if err := tx.QueryRowContext(ctx, `SELECT content FROM messages WHERE run_id=? AND role='user' ORDER BY seq LIMIT 1`, root).Scan(&out.Goal); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	fileRows, err := tx.QueryContext(ctx, `SELECT f.payload FROM message_files f JOIN messages m ON m.id=f.message_id WHERE m.run_id=? AND m.role='user' ORDER BY m.seq`, root)
	if err != nil {
		return out, err
	}
	for fileRows.Next() {
		var raw string
		if err = fileRows.Scan(&raw); err != nil {
			fileRows.Close()
			return out, err
		}
		var files []materials.File
		if err = json.Unmarshal([]byte(raw), &files); err != nil {
			fileRows.Close()
			return out, err
		}
		for _, f := range files {
			out.Attachments = append(out.Attachments, f.Metadata())
		}
	}
	err = fileRows.Err()
	fileRows.Close()
	if err != nil {
		return out, err
	}
	out.Tasks = []TaskSpec{}
	for _, task := range g.Tasks {
		out.Tasks = append(out.Tasks, task.Spec)
		if task.InputMessageID != "" {
			out.Inputs = append(out.Inputs, StageInput{TaskID: task.Spec.ID, Prompt: task.Spec.InputPrompt, MessageID: task.InputMessageID, Content: task.Input})
		}
	}
	// Final assistant messages with no tool calls are the delivery candidate.
	rows, err := tx.QueryContext(ctx, `SELECT id,session_id,(SELECT context_id FROM sessions WHERE id=messages.session_id),run_id,seq,role,content,tool_calls,created_at FROM messages WHERE run_id=? AND role='assistant' AND NOT EXISTS(SELECT 1 FROM tool_calls WHERE message_id=messages.id) ORDER BY seq DESC LIMIT 1`, root)
	if err != nil {
		return out, err
	}
	if !rows.Next() {
		rows.Close()
		return out, errors.New("没有可审查的交付消息")
	}
	var callsRaw string
	err = rows.Scan(&out.Delivery.ID, &out.Delivery.SessionID, &out.Delivery.ContextID, &out.Delivery.RunID, &out.Delivery.Seq, &out.Delivery.Role, &out.Delivery.Content, &callsRaw, &out.Delivery.CreatedAt)
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Calls = []ToolCall{}
	rows, err = tx.QueryContext(ctx, `WITH RECURSIVE tree(id) AS(SELECT ? UNION ALL SELECT r.id FROM runs r JOIN tree ON r.parent_run_id=tree.id) SELECT `+toolCallColumns+` FROM tool_calls WHERE run_id IN tree ORDER BY created_at,id`, root)
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
	payload, err := json.Marshal(struct {
		Profile     string
		Inputs      []StageInput
		Attachments []materials.File
		Goal        string
		Tasks       []TaskSpec
		Delivery    Message
		Artifacts   []Artifact
		Calls       []ToolCall
	}{raw, out.Inputs, out.Attachments, out.Goal, out.Tasks, out.Delivery, out.Artifacts, out.Calls})
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(payload)
	out.Binding = hex.EncodeToString(sum[:])
	// Earlier reviews apply to earlier deliveries. Rework must provide fresh
	// machine evidence; inserting the current review cannot invalidate itself.
	err = tx.QueryRowContext(ctx, `SELECT v.created_at FROM task_reviews v JOIN runs r ON r.id=v.reviewer_run_id WHERE v.root_run_id=? AND r.parent_run_id=? AND v.binding<>? ORDER BY v.created_at DESC,v.id DESC LIMIT 1`, root, root, out.Binding).Scan(&out.ChecksAfter)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}

	return out, nil
}
func (s *Store) ReviewSnapshot(ctx context.Context, root string) (ReviewSnapshot, error) {
	var out ReviewSnapshot
	err := s.withTx(ctx, func(tx *sql.Tx) error { var err error; out, err = reviewSnapshotInTx(ctx, tx, root); return err })
	return out, err
}

// MissingReviewChecks validates successful, machine-readable and fresh evidence.
func MissingReviewChecks(snap ReviewSnapshot) []string {
	missing := []string{}
	for _, name := range snap.Policy.RequiredChecks {
		var last *ToolCall
		for i := range snap.Calls {
			if snap.Calls[i].ToolName == name {
				last = &snap.Calls[i]
			}
		}
		var check struct {
			Passed bool `json:"passed"`
		}
		fresh := true
		if last != nil && snap.ChecksAfter != "" {
			boundary, e1 := time.Parse(time.RFC3339Nano, snap.ChecksAfter)
			created, e2 := time.Parse(time.RFC3339Nano, last.CreatedAt)
			fresh = e1 == nil && e2 == nil && created.After(boundary)
		}
		if !fresh || last == nil || last.State != CallSucceeded || json.Unmarshal([]byte(last.Result), &check) != nil || !check.Passed {
			missing = append(missing, name)
		}
	}
	return missing
}
func (s *Store) SaveTaskReview(ctx context.Context, root, reviewer, binding string, result ReviewResult) (TaskReview, error) {
	id, idErr := newID()
	if idErr != nil {
		return TaskReview{}, idErr
	}
	out := TaskReview{ID: id, RootRunID: root, ReviewerRunID: reviewer, Binding: binding, Result: result, CreatedAt: now()}
	if err := result.Validate(); err != nil {
		return out, err
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var parent, role, status, reviewerSnapshot string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(r.parent_run_id,''),r.component,r.status,r.config_snapshot FROM runs r WHERE r.id=?`, reviewer).Scan(&parent, &role, &status, &reviewerSnapshot); err != nil {
			return err
		}
		if parent != root {
			var target string
			if err := tx.QueryRowContext(ctx, `SELECT target_run_id FROM review_requests WHERE run_id=?`, parent).Scan(&target); err != nil || target != root {
				return errors.New("审查运行与目标不匹配")
			}
		}
		var parentStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, parent).Scan(&parentStatus); err != nil {
			return err
		}
		if role != "reviewer" || status != "running" || parentStatus != "running" {
			return errors.New("审查必须来自该根任务正在运行的 reviewer")
		}
		snap, err := reviewSnapshotInTx(ctx, tx, root)
		if err != nil {
			return err
		}
		if snap.Binding != binding {
			return errors.New("审查期间交付或条件变化，结论失效")
		}
		valid := map[string]bool{}
		for _, c := range snap.Calls {
			if c.State == CallSucceeded {
				valid[c.ID] = true
			}
		}
		for _, id := range result.EvidenceCallIDs {
			if !valid[id] {
				return errors.New("审查引用了非真实成功工具证据")
			}
		}
		var reviewerProfile RuntimeConfigData
		if err := json.Unmarshal([]byte(reviewerSnapshot), &reviewerProfile); err != nil {
			return err
		}
		for _, name := range reviewerProfile.Execution.Review.RequiredChecks {
			found := false
			for _, old := range snap.Policy.RequiredChecks {
				if old == name {
					found = true
				}
			}
			if !found {
				snap.Policy.RequiredChecks = append(snap.Policy.RequiredChecks, name)
			}
		}
		missing := MissingReviewChecks(snap)
		unknown := false
		for _, c := range snap.Calls {
			if c.State == CallExecutionUnknown || c.State == CallExecuting || c.State == CallPending || c.State == CallApproved {
				unknown = true
			}
		}
		if result.Status == "passed" && (len(missing) > 0 || unknown) {
			out.Result.Status = "inconclusive"
			out.Result.Summary = "宿主未确认必要检查或工具结果：" + strings.Join(missing, ", ") + "。" + result.Summary
		}
		data, _ := json.Marshal(out.Result)
		_, err = tx.ExecContext(ctx, `INSERT INTO task_reviews(id,root_run_id,reviewer_run_id,binding,result,created_at) VALUES(?,?,?,?,?,?)`, out.ID, root, reviewer, binding, string(data), out.CreatedAt)
		out.Current = err == nil
		return err
	})
	return out, err
}
func reviewsInTx(ctx context.Context, tx *sql.Tx, root string) ([]TaskReview, error) {
	out := []TaskReview{}
	snap, snapErr := reviewSnapshotInTx(ctx, tx, root)
	rows, err := tx.QueryContext(ctx, `SELECT id,root_run_id,reviewer_run_id,binding,result,created_at FROM task_reviews WHERE root_run_id=? ORDER BY created_at,id`, root)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r TaskReview
		var raw string
		if err := rows.Scan(&r.ID, &r.RootRunID, &r.ReviewerRunID, &r.Binding, &raw, &r.CreatedAt); err != nil {
			return out, err
		}
		if err := json.Unmarshal([]byte(raw), &r.Result); err != nil {
			return out, err
		}
		r.Current = snapErr == nil && r.Binding == snap.Binding
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) ListTaskReviews(ctx context.Context, root string) ([]TaskReview, error) {
	var out []TaskReview
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var target string
		e := tx.QueryRowContext(ctx, `SELECT target_run_id FROM review_requests WHERE run_id=?`, root).Scan(&target)
		if e == nil {
			root = target
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var err error
		out, err = reviewsInTx(ctx, tx, root)
		return err
	})
	return out, err
}
func enforceRequiredReview(ctx context.Context, tx *sql.Tx, root string) error {
	var component string
	if err := tx.QueryRowContext(ctx, `SELECT component FROM runs WHERE id=?`, root).Scan(&component); err != nil {
		return err
	}
	if component == "improver" {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM profile_drafts WHERE run_id=?)+(SELECT count(*) FROM skill_drafts WHERE run_id=?)`, root, root).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("配置生成没有有效草案")
		}
		return nil
	}

	var retrospectiveTarget string
	retrospectiveErr := tx.QueryRowContext(ctx, `SELECT target_run_id FROM retrospective_requests WHERE run_id=?`, root).Scan(&retrospectiveTarget)
	if retrospectiveErr == nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_retrospectives v JOIN runs r ON r.id=v.reviewer_run_id WHERE r.parent_run_id=? AND r.status='completed' AND v.root_run_id=?`, root, retrospectiveTarget).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("单次复盘没有有效记录")
		}
		return nil
	}
	if !errors.Is(retrospectiveErr, sql.ErrNoRows) {
		return retrospectiveErr
	}

	var target string
	reqErr := tx.QueryRowContext(ctx, `SELECT target_run_id FROM review_requests WHERE run_id=?`, root).Scan(&target)
	if reqErr == nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_reviews v JOIN runs r ON r.id=v.reviewer_run_id WHERE r.parent_run_id=? AND r.status='completed' AND v.root_run_id=?`, root, target).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("单次审查没有有效记录")
		}
		return nil
	}
	if !errors.Is(reqErr, sql.ErrNoRows) {
		return reqErr
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT config_snapshot FROM runs WHERE id=?`, root).Scan(&raw); err != nil {
		return err
	}
	var p RuntimeConfigData
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if p.Execution.Review.Mode != "required" {
		return nil
	}
	reviews, err := reviewsInTx(ctx, tx, root)
	if err != nil {
		return err
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		if reviews[i].Current {
			var reviewerStatus string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, reviews[i].ReviewerRunID).Scan(&reviewerStatus); err != nil {
				return err
			}
			if reviews[i].Result.Status == "passed" && reviewerStatus == "completed" {
				return nil
			}
			break
		}
	}
	return fmt.Errorf("必需独立审查未通过或交付已变化，不能标记完成")
}

// One-shot review has its own root budget and never rewrites the old outcome.
// InspectionRequest fixes a root's independent review purpose at creation.
// Inspection roots never initialize or execute the Profile's business stages.
type InspectionRequest struct {
	TargetRunID   string
	Retrospective bool
}

func initInspectionRequest(ctx context.Context, tx *sql.Tx, root string, request InspectionRequest) error {
	var contextID, targetContext string
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT context_id FROM sessions WHERE id=runs.session_id) FROM runs WHERE id=? AND status='running' AND parent_run_id IS NULL`, root).Scan(&contextID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT context_id FROM sessions WHERE id=runs.session_id) FROM runs WHERE id=? AND status<>'running' AND parent_run_id IS NULL`, request.TargetRunID).Scan(&targetContext); err != nil {
		return err
	}
	if contextID != targetContext {
		return errors.New("审查/复盘仅支持同一上下文内已结束的根任务")
	}
	query := `INSERT INTO review_requests(run_id,target_run_id) VALUES(?,?)`
	if request.Retrospective {
		query = `INSERT INTO retrospective_requests(run_id,target_run_id) VALUES(?,?)`
	}
	_, err := tx.ExecContext(ctx, query, root, request.TargetRunID)
	return err
}
