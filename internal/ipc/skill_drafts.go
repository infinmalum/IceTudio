package ipc

import "icetudio/internal/storage"

func (s *Server) dispatchSkillDraft(request Request) (any, error) {
	var p struct {
		ID          string                `json:"id"`
		SessionID   string                `json:"sessionId"`
		Source      string                `json:"source"`
		Request     string                `json:"request"`
		EvidenceIDs []string              `json:"evidenceIds"`
		FeedbackIDs []string              `json:"feedbackIds"`
		Revision    int                   `json:"revision"`
		Proposal    storage.SkillProposal `json:"proposal"`
	}
	if err := decodeParams(request.Params, &p); err != nil {
		return nil, err
	}
	switch request.Method {
	case "skillDrafts.start":
		return s.svc.StartSkillDraft(p.SessionID, p.Source, p.Request, p.EvidenceIDs, p.FeedbackIDs)
	case "skillDrafts.list":
		return s.svc.ListSkillDrafts()
	case "skillDrafts.edit":
		return s.svc.EditSkillDraft(p.ID, p.Revision, p.Proposal)
	case "skillDrafts.apply":
		return s.svc.ApplySkillDraft(p.ID, p.Revision)
	case "skillDrafts.withdraw":
		return nil, s.svc.WithdrawSkillDraft(p.ID, p.Revision)
	case "skillDrafts.discard":
		return nil, s.svc.DiscardSkillDraft(p.ID, p.Revision)
	}
	return nil, nil
}

// dispatchSkillEvolution covers the P4 feedback/evaluation/disable surface.
// Every action is an explicit user call; no run can write these records.
func (s *Server) dispatchSkillEvolution(request Request) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		MessageID string `json:"messageId"`
		NoteID    string `json:"noteId"`
		Note      string `json:"note"`
		SkillName string `json:"skillName"`
		RunID     string `json:"runId"`
		Outcome   string `json:"outcome"`
		Disabled  bool   `json:"disabled"`
	}
	if err := decodeParams(request.Params, &p); err != nil {
		return nil, err
	}
	switch request.Method {
	case "feedback.record":
		return s.svc.RecordFeedback(p.SessionID, p.MessageID, p.Note)
	case "feedback.list":
		return s.svc.ListFeedback(p.SessionID)
	case "feedback.dismiss":
		return nil, s.svc.DismissFeedback(p.SessionID, p.NoteID)
	case "skills.list":
		return s.svc.ListSkills()
	case "skills.setDisabled":
		return nil, s.svc.SetSkillDisabled(p.SkillName, p.Disabled)
	case "skillEvaluations.record":
		return s.svc.RecordSkillEvaluation(p.SkillName, p.SessionID, p.RunID, p.Outcome, p.Note)
	case "skillEvaluations.list":
		return s.svc.ListSkillEvaluations(p.SkillName)
	}
	return nil, nil
}
