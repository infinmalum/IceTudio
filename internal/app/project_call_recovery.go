package app

import (
	"context"

	"icetudio/internal/storage"
)

// ReconcileProjectToolCall is an explicit local-user query. It cannot dispatch
// a tool, change music, retarget a call, or resume an interrupted task.
func (s *Service) ReconcileProjectToolCall(callID string) (storage.ProjectCallRecovery, error) {
	if err := s.checkReady(); err != nil {
		return storage.ProjectCallRecovery{}, err
	}
	_, run, err := s.callWithRun(callID)
	if err != nil {
		return storage.ProjectCallRecovery{}, err
	}
	out, err := s.store.ReconcileProjectToolCall(context.Background(), callID)
	if err == nil && out.Outcome == "committed" {
		s.publish(Event{Type: "run.updated", SessionID: run.SessionID, RunID: run.ID})
	}
	return out, err
}
