package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRetrospectivePersistenceUnknownEffectAndCompletionGate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retrospective.db")
	s, err := testOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, target.ID, RoleUser, "检查写入失败原因"); err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.AddAssistantMessage(ctx, c.ID, target.ID, "", []MessageToolCall{{ID: "write", Name: "remote__write", Arguments: "{}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishToolCall(ctx, calls[0].ID, CallExecutionUnknown, "", "响应丢失", "private"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, target.ID, "failed", "响应丢失"); err != nil {
		t.Fatal(err)
	}
	root, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{Inspection: &InspectionRequest{TargetRunID: target.ID, Retrospective: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, root.ID, "completed", ""); err == nil {
		t.Fatal("retrospective completed without report")
	}
	child, err := s.CreateDelegationSession(ctx, c.ContextID, p.ID, "复盘")
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _, err := s.CreateRunWithOptions(ctx, child.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "@reviewer", Component: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.RetrospectiveSnapshot(ctx, target.ID)
	if err != nil || len(snap.Calls) != 1 || snap.Calls[0].State != CallExecutionUnknown {
		t.Fatal(snap, err)
	}
	forged := RetrospectiveResult{Summary: "无依据", Observations: []RetrospectiveObservation{{Reference: "run:other", Description: "不存在"}}}
	if _, err = s.SaveTaskRetrospective(ctx, target.ID, reviewer.ID, snap.Binding, forged); err == nil {
		t.Fatal("invented reference accepted")
	}
	result := RetrospectiveResult{Summary: "执行效果未知，未重跑", Observations: []RetrospectiveObservation{{Reference: "call:" + calls[0].ID, Description: "响应丢失，不能断言成功或失败"}}, Limitations: []string{"需用户确认效果"}}
	if _, err = s.SaveTaskRetrospective(ctx, target.ID, reviewer.ID, snap.Binding, result); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, root.ID, "completed", ""); err == nil {
		t.Fatal("running reviewer bypassed completion")
	}
	if err = s.SetRunStatus(ctx, reviewer.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, root.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := s.ListTaskRetrospectives(ctx, root.ID)
	if err != nil || len(reports) != 1 || !reports[0].Current {
		t.Fatal(reports, err)
	}
	if err = s.ResolveToolCall(ctx, calls[0].ID, CallFailed, "", "用户确认写入未生效"); err != nil {
		t.Fatal(err)
	}
	reports, err = s.ListTaskRetrospectives(ctx, target.ID)
	if err != nil || len(reports) != 1 || reports[0].Current {
		t.Fatal(reports, err)
	}
	old, err := s.GetRun(ctx, target.ID)
	if err != nil || old.Status != "failed" {
		t.Fatal(old, err)
	}
	reviews, err := s.ListTaskReviews(ctx, target.ID)
	if err != nil || len(reviews) != 0 {
		t.Fatal(reviews, err)
	}
}
