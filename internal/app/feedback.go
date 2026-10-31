package app

import (
	"context"
	"errors"
	"strings"

	"icetudio/internal/storage"
)

// RecordFeedback captures one of the user's own messages as a per-
// conversation feedback note. The model is never involved; the note quotes
// the message verbatim and links the durable task/project context of now.
func (s *Service) RecordFeedback(sessionID, messageID, note string) (storage.FeedbackNote, error) {
	if err := s.checkReady(); err != nil {
		return storage.FeedbackNote{}, err
	}
	s.taskMu.Lock()
	active := s.running[sessionID]
	s.taskMu.Unlock()
	out, err := s.store.SaveFeedbackNote(context.Background(), sessionID, messageID, active, note)
	if err == nil {
		s.publish(Event{Type: "feedback.updated", SessionID: sessionID})
	}
	return out, err
}

// ListFeedback returns this conversation's notes with their states; the UI
// filters dismissed notes out of refinement selection.
func (s *Service) ListFeedback(sessionID string) ([]storage.FeedbackNote, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	return s.store.ListFeedbackNotes(context.Background(), sessionID)
}

func (s *Service) DismissFeedback(sessionID, noteID string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	if err := s.store.DismissFeedbackNote(context.Background(), sessionID, noteID); err != nil {
		return err
	}
	s.publish(Event{Type: "feedback.updated", SessionID: sessionID})
	return nil
}

// SetSkillDisabled flips the reversible off switch for an installed skill.
// Frozen in-flight runs keep their persisted snapshots.
func (s *Service) SetSkillDisabled(name string, disabled bool) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("缺少 Skill 名称")
	}
	s.mu.RLock()
	catalog := s.skills
	s.mu.RUnlock()
	if catalog == nil || !catalog.Installed(name) {
		return errors.New("Skill 未安装")
	}
	if err := s.store.SetSkillState(context.Background(), name, disabled); err != nil {
		return err
	}
	return catalog.SetDisabled(name, disabled)
}

// RecordSkillEvaluation stores one user-recorded music-task outcome for a
// skill. It never gates adoption or enablement automatically.
func (s *Service) RecordSkillEvaluation(skillName, sessionID, runID, outcome, note string) (storage.SkillEvaluation, error) {
	if err := s.checkReady(); err != nil {
		return storage.SkillEvaluation{}, err
	}
	s.mu.RLock()
	catalog := s.skills
	s.mu.RUnlock()
	if catalog == nil || !catalog.Installed(skillName) {
		return storage.SkillEvaluation{}, errors.New("Skill 未安装")
	}
	return s.store.SaveSkillEvaluation(context.Background(), skillName, sessionID, runID, outcome, note)
}

func (s *Service) ListSkillEvaluations(skillName string) ([]storage.SkillEvaluation, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	return s.store.ListSkillEvaluations(context.Background(), skillName)
}
