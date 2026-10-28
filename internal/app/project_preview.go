package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/playback"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

type ProjectEditPreview struct {
	CallID        string           `json:"callId"`
	ArgumentsHash string           `json:"argumentsHash"`
	ProjectID     string           `json:"projectId"`
	Title         string           `json:"title"`
	BaseRevision  int64            `json:"baseRevision"`
	StartTick     int64            `json:"startTick"`
	EndTick       int64            `json:"endTick"`
	Diff          project.NoteDiff `json:"diff"`
}

type preparedProjectPreview struct {
	view   ProjectEditPreview
	before project.State
	after  project.State
}

func (s *Service) prepareProjectPreview(callID string) (preparedProjectPreview, error) {
	var out preparedProjectPreview
	ctx := context.Background()
	call, run, err := s.callWithRun(callID)
	if err != nil {
		return out, err
	}
	if call.State != storage.CallPending || run.Status != "running" {
		return out, errors.New("候选已不在等待采用状态；请查看任务实际结果")
	}
	data, found, err := s.store.ProfileSnapshot(ctx, run.ID)
	if err != nil {
		return out, err
	}
	if !found {
		return out, errors.New("缺少任务起始配置")
	}
	resources, err := s.store.RunResources(ctx, run.ID)
	if err != nil {
		return out, err
	}
	p := storage.RunConfigSnapshot{RuntimeConfigData: data, ContextID: run.ContextID, Policy: resources.RootPolicy, Resources: &resources, ResourcesFrozen: true}
	gate := toolGate{svc: s, contextID: run.ContextID, snapshot: &p, runID: run.ID}
	if d := gate.Gate(ctx, agent.ToolRequest{Tool: call.ToolName, Arguments: call.Arguments, CallID: call.CallID}); d.Deny != "" {
		return out, errors.New(d.Deny)
	}
	t := bookkeepingTools{svc: s, profile: p, sessionID: run.SessionID, runID: run.ID}
	target, scope, _, err := t.projectAccess(ctx)
	if err != nil {
		return out, err
	}
	item, err := s.GetProject(target)
	if err != nil {
		return out, err
	}
	var ops []project.Op
	var projectID string
	var opID string
	var base *int64
	switch call.ToolName {
	case "project__edit":
		if err := checkProjectOpFields(call.Arguments); err != nil {
			return out, err
		}
		var a projectEditArgs
		if err := decodeRequiredOperation(call.Arguments, &a, "projectId", "opId", "baseRevision", "ops"); err != nil {
			return out, err
		}
		projectID, base, ops = a.ProjectID, a.BaseRevision, a.Ops
		opID = a.OpID
		for _, op := range ops {
			if op.Kind != "note.add" && op.Kind != "note.update" && op.Kind != "note.remove" {
				return out, errors.New("本批候选预览仅支持音符增删改；轨道、时间图和控制事件请使用原通用审批")
			}
		}
		if a.OpID == "" {
			return out, errors.New("缺少操作 ID")
		}
	case "project__transform":
		var a struct {
			ProjectID    string                 `json:"projectId"`
			OpID         string                 `json:"opId"`
			BaseRevision *int64                 `json:"baseRevision"`
			Selection    *project.Selection     `json:"selection"`
			Transform    *project.TransformSpec `json:"transform"`
			DryRun       bool                   `json:"dryRun"`
		}
		if err := decodeRequiredOperation(call.Arguments, &a, "projectId", "opId", "baseRevision", "transform"); err != nil {
			return out, err
		}
		if a.DryRun || a.OpID == "" {
			return out, errors.New("预演调用不是待采用的音乐修改")
		}
		projectID, base = a.ProjectID, a.BaseRevision
		opID = a.OpID
		// Check identity/revision before compiling or exposing any music.
		if projectID != target {
			return out, errors.New("候选不属于任务冻结的工程")
		}
		if *base != item.Revision {
			return out, errors.New("工程已修改，候选基准已过时；请丢弃并让助手重新提出")
		}
		sel := project.Selection{}
		if a.Selection != nil {
			sel = *a.Selection
		}
		ops, _, err = compileScopedTransform(item, sel, *a.Transform, scope, true)
		if err != nil {
			return out, err
		}
	default:
		return out, errors.New("此调用没有音乐候选预览")
	}
	if projectID != target {
		return out, errors.New("候选不属于任务冻结的工程")
	}
	if base == nil || *base != item.Revision {
		return out, errors.New("工程已修改，候选基准已过时；请丢弃并让助手重新提出")
	}
	if _, found, err := s.GetProjectTransaction(target, opID); err != nil {
		return out, err
	} else if found {
		return out, errors.New("原操作已有提交结果；请核对任务结果，不再作为新候选采用")
	}
	if scope.AccessMode != "write" {
		return out, errors.New("本任务为只读，不能采用或试听编辑")
	}
	if len(scope.Tracks) > 0 || len(scope.Clips) > 0 {
		if err := checkScopeOps(scope, ops, &item.State); err != nil {
			return out, err
		}
	}
	out.before, out.after = item.State, item.State
	if _, _, _, err := project.ApplyNewTransaction(&out.after, ops, nil); err != nil {
		return out, err
	}
	b := project.ScopeRegion(&out.before, scope.Tracks, scope.Clips)
	a := project.ScopeRegion(&out.after, scope.Tracks, scope.Clips)
	hash := sha256.Sum256([]byte(call.Arguments))
	out.view = ProjectEditPreview{CallID: callID, ArgumentsHash: hex.EncodeToString(hash[:]), ProjectID: target, Title: item.Title, BaseRevision: item.Revision, Diff: project.DiffNotes(b, a, 2000)}
	if out.view.Diff.Total == 0 {
		return out, errors.New("候选没有实际音符变化；可丢弃这次调用")
	}
	// All affected clips, including changes beyond the transported row cap.
	clipIDs := map[string]bool{}
	for _, op := range ops {
		clipIDs[op.ClipID] = true
	}
	out.view.StartTick = project.MaxTick
	for _, tr := range item.State.Tracks {
		for _, c := range tr.Clips {
			if clipIDs[c.ID] {
				out.view.StartTick = min(out.view.StartTick, c.Start)
				out.view.EndTick = max(out.view.EndTick, c.Start+c.Length)
			}
		}
	}
	return out, nil
}

func (s *Service) PreviewProjectEdit(callID string) (ProjectEditPreview, error) {
	p, err := s.prepareProjectPreview(callID)
	return p.view, err
}

func (s *Service) ApproveProjectEditPreview(callID, argumentsHash string) (storage.ToolCall, error) {
	p, err := s.prepareProjectPreview(callID)
	if err != nil {
		return storage.ToolCall{}, err
	}
	if p.view.ArgumentsHash != argumentsHash {
		return storage.ToolCall{}, errors.New("候选参数与所查看的预览不一致，请重新检查")
	}
	return s.ApproveToolCall(callID)
}

func (s *Service) PlayProjectEditPreview(callID, variant string) error {
	if variant != "original" && variant != "candidate" {
		return errors.New("请选择原版或候选试听")
	}
	if s.playback == nil {
		return errors.New("此宿主未启用原生播放；仍可检查差异和丢弃候选")
	}
	p, err := s.prepareProjectPreview(callID)
	if err != nil {
		return err
	}
	state := p.before
	if variant == "candidate" {
		state = p.after
	}
	req := playback.Request{ProjectID: p.view.ProjectID, Revision: p.view.BaseRevision, Tick: p.view.StartTick, Loop: true, LoopStart: p.view.StartTick, LoopEnd: p.view.EndTick}
	if err := s.playback.PlayPreview(req, playback.Snapshot{ID: p.view.ProjectID, Revision: p.view.BaseRevision, State: state}, callID, variant); err != nil {
		return err
	}
	// A cancellation/approval during compilation must not leave a late preview
	// audible after its persisted candidate has already expired.
	call, run, err := s.callWithRun(callID)
	if err != nil || call.State != storage.CallPending || run.Status != "running" {
		s.playback.StopPreview(callID)
		return fmt.Errorf("候选已失效，试听已停止")
	}
	go s.watchProjectPreview(callID)
	return nil
}

// Approval timeouts and disconnected clients still retire preview audio. This
// watcher reads only lifecycle metadata; no project/DB work enters audio callbacks.
func (s *Service) watchProjectPreview(callID string) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if s.playback.Status().PreviewID != callID {
			return
		}
		call, run, err := s.callWithRun(callID)
		if err != nil || call.State != storage.CallPending || run.Status != "running" {
			s.playback.StopPreview(callID)
			return
		}
	}
}

func (s *Service) StopProjectEditPreview(callID string) error {
	if s.playback != nil {
		s.playback.StopPreview(callID)
	}
	return nil
}
