package app

import (
	"context"
	"encoding/json"
	"fmt"

	"icetudio/internal/storage"
)

// Session-scoped bookkeeping views: the per-session compaction summary and
// the resumable plan. Both are per-session records — not cross-conversation
// memory — and never touch approvals, permissions or history.

// GetSessionSummary returns the session's compaction summary; an absent
// summary is an error the clients render as "no summary".
func (s *Service) GetSessionSummary(sessionID string) (storage.SessionSummary, error) {
	if err := s.checkSession(sessionID); err != nil {
		return storage.SessionSummary{}, err
	}
	sum, found, err := s.store.GetSessionSummary(context.Background(), sessionID)
	if err != nil {
		return storage.SessionSummary{}, err
	}
	if !found {
		return storage.SessionSummary{}, fmt.Errorf("会话没有压缩摘要")
	}
	return sum, nil
}

// SessionPlanView is the client projection of a session plan: the parsed
// steps plus the metadata the review panel shows.
type SessionPlanView struct {
	SessionID string             `json:"sessionId"`
	ContextID string             `json:"contextId"`
	UpdatedAt string             `json:"updatedAt"`
	Steps     []storage.PlanStep `json:"steps"`
	Revision  int                `json:"revision"`
}

func (s *Service) GetSessionPlan(sessionID string) (SessionPlanView, error) {
	if err := s.checkSession(sessionID); err != nil {
		return SessionPlanView{}, err
	}
	plan, found, err := s.store.GetSessionPlan(context.Background(), sessionID)
	if err != nil {
		return SessionPlanView{}, err
	}
	if !found {
		return SessionPlanView{}, fmt.Errorf("会话没有进行中的计划")
	}
	var steps []storage.PlanStep
	if err := json.Unmarshal([]byte(plan.Plan), &steps); err != nil {
		return SessionPlanView{}, fmt.Errorf("解析计划: %w", err)
	}
	return SessionPlanView{SessionID: plan.SessionID, ContextID: plan.ContextID, UpdatedAt: plan.UpdatedAt, Steps: steps, Revision: plan.Revision}, nil
}

// ClearSessionPlan removes the session's plan. It never touches approvals,
// permissions or history: a plan is bookkeeping, not authority.
func (s *Service) ClearSessionPlan(sessionID string) error {
	if err := s.checkSession(sessionID); err != nil {
		return err
	}
	return s.store.ClearSessionPlan(context.Background(), sessionID)
}

func (s *Service) checkSession(sessionID string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	return s.checkSessionExists(sessionID)
}
