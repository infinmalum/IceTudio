package app

import (
	"context"
	"icetudio/internal/storage"
)

// Only the explicitly selected task trees are sent. Sensitivity metadata from
// every process and call survives into the requesting conversation.
func (s *Service) inheritDraftEvidence(ctx context.Context, session string, p storage.RunConfigSnapshot, evidence []storage.DraftEvidence) error {
	scopes := []string{}
	for _, item := range evidence {
		resources, err := s.store.RunResources(ctx, item.RootRunID)
		if err != nil {
			return err
		}
		frozen := []storage.RuntimeConfigRevision{resources.Root, resources.Planner, resources.Reviewer}
		for _, worker := range resources.Workers {
			frozen = append(frozen, worker)
		}
		for _, config := range frozen {
			scopes = append(scopes, config.Scopes...)
			for _, skill := range config.Skills {
				scopes = append(scopes, skill.Scopes...)
			}
		}

		seen := map[string]bool{}
		for _, run := range item.Snapshot.Runs {
			if seen[run.SessionID] {
				continue
			}
			seen[run.SessionID] = true
			ss, err := s.sessionScopes(ctx, run.SessionID, p)
			if err != nil {
				return err
			}
			scopes = append(scopes, ss...)
		}
		for _, call := range item.Snapshot.Calls {
			if call.SourceScope != "" {
				scopes = append(scopes, call.SourceScope)
			}
		}
	}
	return s.store.AccumulateContextScopes(ctx, session, scopes)
}
