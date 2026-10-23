package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type LocalContext struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Policy ContextPolicy `json:"policy"`
}

type ConversationOptions struct {
	WebSearch bool `json:"webSearch"`
}

type Conversation struct {
	Options        ConversationOptions `json:"options"`
	Kind           string              `json:"kind"`
	ID             string              `json:"id"`
	ContextID      string              `json:"contextId"`
	ConfigID       string              `json:"configId"`
	ConfigName     string              `json:"configName"`
	Title          string              `json:"title"`
	CreatedAt      string              `json:"createdAt"`
	ConfigArchived bool                `json:"configArchived"`
	ProjectID      string              `json:"projectId"`
}

func (s *Store) GetLocalContext(ctx context.Context, id string) (LocalContext, error) {
	var item LocalContext
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,policy FROM contexts WHERE id=?`, id).Scan(&item.ID, &item.Name, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &item.Policy)
	}
	return item, err
}

func activeRuntimeConfig(ctx context.Context, tx *sql.Tx, id string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runtime_configs WHERE id=? AND archived_at IS NULL`, id).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return errors.New("执行配置不存在或已删除")
	}
	return nil
}

// The context and session are created atomically. reuseSession is explicit;
// an existing config on its own never causes history or grants to be shared.
func (s *Store) CreateConversation(ctx context.Context, configID, reuseSession, title string) (Conversation, error) {
	if strings.TrimSpace(title) == "" {
		title = "新对话"
	}
	if len(title) > 500 {
		return Conversation{}, errors.New("对话标题过长")
	}
	sessionID, err := newID()
	if err != nil {
		return Conversation{}, err
	}
	contextID, err := newID()
	if err != nil {
		return Conversation{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := activeRuntimeConfig(ctx, tx, configID); err != nil {
			return err
		}
		if reuseSession != "" {
			if err := tx.QueryRowContext(ctx, `SELECT context_id FROM sessions WHERE id=? AND kind='normal'`, reuseSession).Scan(&contextID); err != nil {
				return errors.New("找不到可复用的普通对话")
			}
		} else {
			policy, _ := json.Marshal(ContextPolicy{ApprovalMode: "semi"})
			if _, err := tx.ExecContext(ctx, `INSERT INTO contexts(id,name,policy,created_at,updated_at) VALUES(?,?,?,?,?)`, contextID, title, string(policy), now(), now()); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,context_id,kind,title,created_at,config_id) VALUES(?,?,'normal',?,?,?)`, sessionID, contextID, title, now(), configID)
		return err
	})
	if err != nil {
		return Conversation{}, err
	}
	return s.GetConversation(ctx, sessionID)
}

const conversationSelect = `SELECT s.id,s.context_id,s.config_id,p.name,s.title,s.created_at,p.archived_at IS NOT NULL,s.kind,s.options,COALESCE(s.project_id,'') FROM sessions s JOIN contexts a ON a.id=s.context_id JOIN runtime_configs p ON p.id=s.config_id`

func scanConversation(row interface{ Scan(...any) error }) (Conversation, error) {
	var c Conversation
	var raw string
	err := row.Scan(&c.ID, &c.ContextID, &c.ConfigID, &c.ConfigName, &c.Title, &c.CreatedAt, &c.ConfigArchived, &c.Kind, &raw, &c.ProjectID)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c.Options)
	}
	return c, err
}

func (s *Store) GetConversation(ctx context.Context, id string) (Conversation, error) {
	return scanConversation(s.db.QueryRowContext(ctx, conversationSelect+` WHERE s.id=?`, id))
}

func (s *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx, conversationSelect+` WHERE s.kind='normal' ORDER BY COALESCE((SELECT MAX(created_at) FROM messages WHERE session_id=s.id),s.created_at) DESC,s.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		item, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SetConversationOptions(ctx context.Context, id string, options ConversationOptions) (Conversation, error) {
	raw, err := json.Marshal(options)
	if err != nil {
		return Conversation{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET options=? WHERE id=? AND kind='normal'`, string(raw), id)
	if err != nil {
		return Conversation{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Conversation{}, err
	}
	if n != 1 {
		return Conversation{}, errors.New("找不到普通对话")
	}
	return s.GetConversation(ctx, id)
}

// UpdateContextPolicy changes live authorization and wakes affected tasks atomically.
func (s *Store) UpdateContextPolicy(ctx context.Context, id string, policy ContextPolicy) (LocalContext, error) {
	if err := validatePolicy(policy); err != nil {
		return LocalContext{}, err
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return LocalContext{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE contexts SET policy=?,updated_at=? WHERE id=?`, string(raw), now(), id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("本机上下文不存在")
		}
		return signalContextChange(ctx, tx, id, "policy.changed")
	})
	if err != nil {
		return LocalContext{}, err
	}
	return s.GetLocalContext(ctx, id)
}
