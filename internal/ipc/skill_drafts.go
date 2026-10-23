package ipc

import "icetudio/internal/storage"

func (s *Server) dispatchSkillDraft(request Request) (any, error) {
	var p struct {
		ID          string                `json:"id"`
		SessionID   string                `json:"sessionId"`
		Source      string                `json:"source"`
		Request     string                `json:"request"`
		EvidenceIDs []string              `json:"evidenceIds"`
		Revision    int                   `json:"revision"`
		Proposal    storage.SkillProposal `json:"proposal"`
	}
	if err := decodeParams(request.Params, &p); err != nil {
		return nil, err
	}
	switch request.Method {
	case "skillDrafts.start":
		return s.svc.StartSkillDraft(p.SessionID, p.Source, p.Request, p.EvidenceIDs)
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
