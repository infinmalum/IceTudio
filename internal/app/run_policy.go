package app

import (
	"context"
	"errors"
	"fmt"
	"icetudio/internal/config"
	"icetudio/internal/storage"
)

func restrictPolicy(a, b storage.ContextPolicy) storage.ContextPolicy {
	a.ApprovalMode = stricterMode(a.ApprovalMode, b.ApprovalMode)
	a.Permissions = intersectPermissions(a.Permissions, b.Permissions)
	return a
}

// Every component shares the root context. Live changes can only narrow its frozen ceiling.
func (s *Service) restrictRunPolicy(ctx context.Context, runID string, p storage.RunConfigSnapshot) (storage.ContextPolicy, error) {
	resources, err := s.resourcesFor(ctx, p, runID)
	if err != nil {
		return p.Policy, err
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return p.Policy, err
	}
	if run.ContextID != resources.RootContextID || p.ContextID != resources.RootContextID {
		return p.Policy, errors.New("运行上下文与根任务不一致")
	}
	local, err := s.store.GetLocalContext(ctx, resources.RootContextID)
	if err != nil {
		return p.Policy, err
	}
	return restrictPolicy(restrictPolicy(p.Policy, resources.RootPolicy), local.Policy), nil
}
func (s *Service) exfilCheckRun(ctx context.Context, runID, sessionID string, source config.Model, p storage.RunConfigSnapshot) error {
	if _, err := s.restrictRunPolicy(ctx, runID, p); err != nil {
		return err
	}
	origin, local, err := endpointOrigin(source.EffectiveBaseURL())
	if err != nil {
		return err
	}
	if local {
		return nil
	}
	scopes, err := s.sessionScopes(ctx, sessionID, p)
	if err != nil {
		return err
	}
	resources, err := s.resourcesFor(ctx, p, runID)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, scope := range resources.Grants[origin].Scopes {
		allowed[scope] = true
	}
	for _, scope := range scopes {
		if !allowed[scope] {
			return fmt.Errorf("数据外发超出根任务开始时的授权上限：%s / %s；新增授权用于新任务", origin, scope)
		}
	}
	return requireGrantCoverage(ctx, s.store, resources.RootContextID, origin, scopes)
}
