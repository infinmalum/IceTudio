package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func reviewStorageFixture(t *testing.T, checks []string) (*Store, Conversation, Run, Run) {
	t.Helper()
	s, err := testOpen(filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.Review = ReviewPolicy{Mode: "required", RequiredChecks: checks}
	p, err = s.UpdateRuntimeConfig(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, root.ID, RoleUser, "目标"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, root.ID, RoleAssistant, "交付"); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateDelegationSession(ctx, c.ContextID, DefaultConfigID, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _, err := s.CreateRunWithOptions(ctx, child.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "@reviewer", Component: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	return s, c, root, reviewer
}
func TestRequiredReviewCannotBypassOrReuseChangedDelivery(t *testing.T) {
	s, c, root, reviewer := reviewStorageFixture(t, nil)
	ctx := context.Background()
	if err := s.SetRunStatus(ctx, root.ID, "completed", ""); err == nil {
		t.Fatal("required review bypassed")
	}
	snap, err := s.ReviewSnapshot(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := ReviewResult{Status: "passed", Summary: "满足目标"}
	if _, err = s.SaveTaskReview(ctx, root.ID, reviewer.ID, snap.Binding, result); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, root.ID, RoleAssistant, "修改后的交付"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, root.ID, "completed", ""); err == nil {
		t.Fatal("old conclusion reused")
	}
	reviews, err := s.ListTaskReviews(ctx, root.ID)
	if err != nil || len(reviews) != 1 || reviews[0].Current {
		t.Fatal(reviews, err)
	}
	if _, err = s.SaveTaskReview(ctx, root.ID, reviewer.ID, snap.Binding, result); err == nil {
		t.Fatal("stale save accepted")
	}
}
func TestRequiredChecksNeedMachineReadableSuccess(t *testing.T) {
	for _, tc := range []struct {
		result string
		want   string
	}{{`{"passed":false}`, "inconclusive"}, {`tests passed`, "inconclusive"}, {`{"passed":true}`, "passed"}} {
		t.Run(tc.result, func(t *testing.T) {
			s, c, root, reviewer := reviewStorageFixture(t, []string{"tests__run"})
			ctx := context.Background()
			_, calls, err := s.AddAssistantMessage(ctx, c.ID, root.ID, "", []MessageToolCall{{ID: "test", Name: "tests__run", Arguments: "{}"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishToolCall(ctx, calls[0].ID, CallSucceeded, tc.result, "", "public"); err != nil {
				t.Fatal(err)
			}
			snap, err := s.ReviewSnapshot(ctx, root.ID)
			if err != nil {
				t.Fatal(err)
			}
			review, err := s.SaveTaskReview(ctx, root.ID, reviewer.ID, snap.Binding, ReviewResult{Status: "passed", Summary: "审查", EvidenceCallIDs: []string{calls[0].ID}})
			if err != nil || review.Result.Status != tc.want {
				t.Fatal(review, err)
			}
		})
	}
}

func TestReworkCannotReusePreviousSuccessfulCheck(t *testing.T) {
	s, c, root, reviewer := reviewStorageFixture(t, []string{"tests__run"})
	ctx := context.Background()
	addCheck := func(id string) {
		t.Helper()
		_, calls, err := s.AddAssistantMessage(ctx, c.ID, root.ID, "", []MessageToolCall{{ID: id, Name: "tests__run", Arguments: "{}"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
			t.Fatal(err)
		}
		if err = s.FinishToolCall(ctx, calls[0].ID, CallSucceeded, `{"passed":true}`, "", "public"); err != nil {
			t.Fatal(err)
		}
	}
	addCheck("old-check")
	snap, err := s.ReviewSnapshot(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.SaveTaskReview(ctx, root.ID, reviewer.ID, snap.Binding, ReviewResult{Status: "changes_requested", Summary: "修正交付", Defects: []ReviewDefect{{Reference: "delivery", Description: "缺内容"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, root.ID, RoleAssistant, "修正后的交付"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.ReviewSnapshot(ctx, root.ID)
	if err != nil || snap.ChecksAfter != review.CreatedAt || len(MissingReviewChecks(snap)) != 1 {
		t.Fatal(snap, err)
	}
	nextReviewer := func() {
		t.Helper()
		if err := s.SetRunStatus(ctx, reviewer.ID, "completed", ""); err != nil {
			t.Fatal(err)
		}
		session, err := s.CreateDelegationSession(ctx, c.ContextID, c.ConfigID, "再次审查")
		if err != nil {
			t.Fatal(err)
		}
		reviewer, _, err = s.CreateRunWithOptions(ctx, session.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "@reviewer", Component: "reviewer"})
		if err != nil {
			t.Fatal(err)
		}
	}
	nextReviewer()
	result, err := s.SaveTaskReview(ctx, root.ID, reviewer.ID, snap.Binding, ReviewResult{Status: "passed", Summary: "模型认为通过"})
	if err != nil || result.Result.Status != "inconclusive" {
		t.Fatal(result, err)
	}
	addCheck("fresh-check")
	snap, err = s.ReviewSnapshot(ctx, root.ID)
	if err != nil || len(MissingReviewChecks(snap)) != 0 {
		t.Fatal(snap, err)
	}
	nextReviewer()
	result, err = s.SaveTaskReview(ctx, root.ID, reviewer.ID, snap.Binding, ReviewResult{Status: "passed", Summary: "新证据通过"})
	if err != nil || result.Result.Status != "passed" {
		t.Fatal(result, err)
	}
	reviews, err := s.ListTaskReviews(ctx, root.ID)
	if err != nil || len(reviews) != 3 || !reviews[2].Current {
		t.Fatal(reviews, err)
	}
}
