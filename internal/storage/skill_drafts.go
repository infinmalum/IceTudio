package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"icetudio/internal/skills"
	"strings"
)

type SkillProposal struct {
	EvidenceRefs   []string `json:"evidenceRefs"`
	Content        string   `json:"content"`
	Summary        string   `json:"summary"`
	Reasons        []string `json:"reasons"`
	Limitations    []string `json:"limitations"`
	ExpectedChecks []string `json:"expectedChecks"`
}

func (p SkillProposal) Validate() error {
	if _, err := skills.ParseSnapshot(p.Content); err != nil {
		return err
	}
	if strings.TrimSpace(p.Summary) == "" || len(p.Summary) > 16000 {
		return errors.New("Skill草案说明无效")
	}
	for _, list := range [][]string{p.Reasons, p.Limitations, p.ExpectedChecks} {
		if len(list) > 32 {
			return errors.New("Skill草案说明过多")
		}
		for _, value := range list {
			if strings.TrimSpace(value) == "" || len(value) > 8000 {
				return errors.New("Skill草案说明为空或过长")
			}
		}
	}
	return nil
}

// ConfigReference reports one execution config whose selection covers a
// Skill: the common catalog or an explicit worker slot configuration.
type ConfigReference struct {
	ConfigID string   `json:"configId"`
	Name     string   `json:"name"`
	Revision int      `json:"revision"`
	Workers  []string `json:"workers"`
}

type SkillDraft struct {
	Scopes          []string          `json:"scopes"`
	ID              string            `json:"id"`
	RunID           string            `json:"runId"`
	Source          skills.Snapshot   `json:"source"`
	Request         string            `json:"request"`
	Proposal        SkillProposal     `json:"proposal"`
	Revision        int               `json:"revision"`
	State           string            `json:"state"`
	Published       skills.Snapshot   `json:"published"`
	Evidence        []DraftEvidence   `json:"evidence"`
	Impact          []ConfigReference `json:"impact"`
	AllSkillsImpact []ConfigReference `json:"allSkillsImpact"`
	CreatedAt       string            `json:"createdAt"`
	UpdatedAt       string            `json:"updatedAt"`
}

const skillDraftColumns = `id,run_id,source,scopes,request,proposal,revision,state,published,created_at,updated_at`

func scanSkillDraft(row interface{ Scan(...any) error }) (SkillDraft, error) {
	var d SkillDraft
	var source, proposal, published, scopes string
	if err := row.Scan(&d.ID, &d.RunID, &source, &scopes, &d.Request, &proposal, &d.Revision, &d.State, &published, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return d, err
	}
	for _, item := range []struct {
		raw  string
		dest any
	}{{source, &d.Source}, {scopes, &d.Scopes}, {proposal, &d.Proposal}, {published, &d.Published}} {
		if err := json.Unmarshal([]byte(item.raw), item.dest); err != nil {
			return d, err
		}
	}
	return d, nil
}
func (s *Store) GetSkillDraft(ctx context.Context, id string) (SkillDraft, error) {
	return scanSkillDraft(s.db.QueryRowContext(ctx, `SELECT `+skillDraftColumns+` FROM skill_drafts WHERE id=?`, id))
}
func (s *Store) ListSkillDrafts(ctx context.Context) ([]SkillDraft, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+skillDraftColumns+` FROM skill_drafts ORDER BY created_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillDraft{}
	for rows.Next() {
		d, e := scanSkillDraft(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Evidence, err = s.DraftEvidence(ctx, out[i].RunID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) SaveSkillDraft(ctx context.Context, run string, source skills.Snapshot, request string, proposal SkillProposal) (SkillDraft, error) {
	if err := proposal.Validate(); err != nil {
		return SkillDraft{}, err
	}
	id, err := newID()
	if err != nil {
		return SkillDraft{}, err
	}
	raw, _ := json.Marshal(proposal)
	base, _ := json.Marshal(source)
	created := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE id=? AND component='improver' AND parent_run_id IS NULL AND status='running'`, run).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("Skill草案生成运行无效")
		}
		if err := validateDraftRefsInTx(ctx, tx, run, proposal.EvidenceRefs); err != nil {
			return err
		}
		if err := validateDraftEvidenceInTx(ctx, tx, run); err != nil {
			return err
		}
		scopes, err := runScopesInTx(ctx, tx, run)
		if err != nil {
			return err
		}
		scopeJSON, _ := json.Marshal(scopes)
		_, err = tx.ExecContext(ctx, `INSERT INTO skill_drafts(id,run_id,source,scopes,request,proposal,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, run, string(base), string(scopeJSON), request, string(raw), created, created)
		return err
	})
	if err != nil {
		return SkillDraft{}, err
	}
	return s.GetSkillDraft(ctx, id)
}
func (s *Store) EditSkillDraft(ctx context.Context, id string, revision int, proposal SkillProposal) (SkillDraft, error) {
	if err := proposal.Validate(); err != nil {
		return SkillDraft{}, err
	}
	raw, _ := json.Marshal(proposal)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		d, err := scanSkillDraft(tx.QueryRowContext(ctx, `SELECT `+skillDraftColumns+` FROM skill_drafts WHERE id=?`, id))
		if err != nil {
			return err
		}
		if d.State != "draft" || d.Revision != revision {
			return errors.New("Skill草案已变化或已处理")
		}
		if err = validateDraftRefsInTx(ctx, tx, d.RunID, proposal.EvidenceRefs); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE skill_drafts SET proposal=?,revision=revision+1,updated_at=? WHERE id=?`, string(raw), now(), id)
		return err
	})
	if err != nil {
		return SkillDraft{}, err
	}
	return s.GetSkillDraft(ctx, id)
}
func (s *Store) DiscardSkillDraft(ctx context.Context, id string, revision int) error {
	result, err := s.db.ExecContext(ctx, `UPDATE skill_drafts SET state='discarded',revision=revision+1,updated_at=? WHERE id=? AND revision=? AND state='draft'`, now(), id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("Skill草案已变化或已处理")
	}
	return err
}
func (s *Store) ApplySkillDraft(ctx context.Context, id string, revision int) (skills.Snapshot, error) {
	var published skills.Snapshot
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		d, err := scanSkillDraft(tx.QueryRowContext(ctx, `SELECT `+skillDraftColumns+` FROM skill_drafts WHERE id=?`, id))
		if err != nil {
			return err
		}
		if d.State != "draft" || d.Revision != revision {
			return errors.New("Skill草案已变化或已处理")
		}
		var status string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, d.RunID).Scan(&status); err != nil {
			return err
		}
		if status != "completed" {
			return errors.New("Skill草案生成未成功结束")
		}
		if err = validateDraftEvidenceInTx(ctx, tx, d.RunID); err != nil {
			return err
		}
		if err = d.Proposal.Validate(); err != nil {
			return err
		}
		published, err = skills.CopySnapshot(d.Proposal.Content, d.ID[:12])
		if err != nil {
			return err
		}
		published.Resources = skills.CloneResources(d.Source.Resources)
		published.Scopes = append([]string(nil), d.Scopes...)
		raw, _ := json.Marshal(published)
		_, err = tx.ExecContext(ctx, `UPDATE skill_drafts SET published=?,state='applied',revision=revision+1,updated_at=? WHERE id=?`, string(raw), now(), id)
		return err
	})
	return published, err
}

// Reports references without changing them; copies never replace shared Skills.
func (s *Store) SkillReferences(ctx context.Context, name string, allOnly bool) ([]ConfigReference, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,revision,config FROM runtime_configs WHERE archived_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConfigReference{}
	for rows.Next() {
		var p RuntimeConfig
		var raw string
		if err = rows.Scan(&p.ID, &p.Name, &p.Revision, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p.Data); err != nil {
			return nil, err
		}
		refs := []string{}
		if p.Data.AllSkills || (!allOnly && p.Data.EnablesSkill(name)) {
			refs = append(refs, "公共配置")
		}
		for _, slot := range p.Data.Execution.WorkerSources {
			if slot.Configuration != nil && (slot.Configuration.AllSkills || (!allOnly && slot.Configuration.ConfigData().EnablesSkill(name))) {
				refs = append(refs, slot.Name)
			}
		}
		if len(refs) > 0 {
			out = append(out, ConfigReference{ConfigID: p.ID, Name: p.Name, Revision: p.Revision, Workers: refs})
		}
	}
	return out, rows.Err()
}

func (s *Store) WithdrawSkillDraft(ctx context.Context, id string, revision int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		d, err := scanSkillDraft(tx.QueryRowContext(ctx, `SELECT `+skillDraftColumns+` FROM skill_drafts WHERE id=?`, id))
		if err != nil {
			return err
		}
		if d.State != "applied" || d.Revision != revision {
			return errors.New("Skill草案已变化或不能撤销")
		}
		rows, err := tx.QueryContext(ctx, `SELECT config FROM runtime_configs WHERE archived_at IS NULL`)
		if err != nil {
			return err
		}
		referenced := false
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			var data RuntimeConfigData
			if err = json.Unmarshal([]byte(raw), &data); err != nil {
				rows.Close()
				return err
			}
			if !data.AllSkills && data.EnablesSkill(d.Published.Name) {
				referenced = true
			}
			for _, slot := range data.Execution.WorkerSources {
				if slot.Configuration != nil && !slot.Configuration.AllSkills && slot.Configuration.ConfigData().EnablesSkill(d.Published.Name) {
					referenced = true
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if referenced {
			return errors.New("副本仍被执行配置显式引用，请先取消引用再撤销")
		}
		_, err = tx.ExecContext(ctx, `UPDATE skill_drafts SET state='withdrawn',revision=revision+1,updated_at=? WHERE id=?`, now(), id)
		return err
	})
}
