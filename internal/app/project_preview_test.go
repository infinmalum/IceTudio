package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func previewFixture(t *testing.T, mode string, payload func(scopeIDs, int64) (string, string)) (scopeIDs, bookkeepingTools, storage.ToolCall) {
	t.Helper()
	ctx, ids, tools := scopeFixture(t, nil)
	raw := fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":%q,"clips":[%q]}`, ids.Project, mode, ids.C1)
	run, profile, err := tools.svc.store.CreateRunWithOptions(ctx, tools.sessionID, storage.CreateRunOptions{ProjectScope: raw})
	if err != nil {
		t.Fatal(err)
	}
	tools.runID, tools.profile = run.ID, profile
	item, err := tools.svc.GetProject(ids.Project)
	if err != nil {
		t.Fatal(err)
	}
	name, args := payload(ids, item.Revision)
	_, calls, err := tools.svc.store.AddAssistantMessage(ctx, tools.sessionID, run.ID, "", []storage.MessageToolCall{{ID: "candidate-call", Name: name, Arguments: args}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ids, tools, calls[0]
}

func TestProjectPreviewIsReadOnlyAndAdoptionUsesOriginalCall(t *testing.T) {
	for _, name := range []string{"project__edit", "project__transform"} {
		t.Run(name, func(t *testing.T) {
			ids, tools, call := previewFixture(t, "write", func(ids scopeIDs, revision int64) (string, string) {
				if name == "project__transform" {
					return name, transformPayload(ids.Project, revision, "candidate-op", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{"transpose":{"semitones":2}}`, false)
				}
				return name, editPayload(ids.Project, revision, "candidate-op", fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":500,"duration":100,"pitch":72,"velocity":80,"channel":0}]}`, ids.C1))
			})
			s := tools.svc
			before, _ := s.GetProject(ids.Project)
			history, _ := s.ProjectHistory(ids.Project)
			view, err := s.PreviewProjectEdit(call.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Diff.Total != 1 || len(view.Diff.Changes) != 1 || view.StartTick != 0 || view.EndTick != 960 {
				t.Fatalf("preview: %+v", view)
			}
			after, _ := s.GetProject(ids.Project)
			unchanged, _ := s.ProjectHistory(ids.Project)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(history, unchanged) {
				t.Fatal("preview wrote project or history")
			}
			if _, err := s.ApproveProjectEditPreview(call.ID, "wrong-hash"); err == nil {
				t.Fatal("mismatched preview accepted")
			}
			if err := s.PlayProjectEditPreview(call.ID, "candidate"); err == nil {
				t.Fatal("missing audio service was hidden")
			}
			approved, err := s.ApproveProjectEditPreview(call.ID, view.ArgumentsHash)
			if err != nil || approved.State != storage.CallApproved {
				t.Fatalf("approve: %+v %v", approved, err)
			}
			still, _ := s.GetProject(ids.Project)
			if still.Revision != before.Revision {
				t.Fatal("approval was treated as a commit")
			}
			if _, err := tools.Call(context.Background(), name, call.Arguments); err != nil {
				t.Fatal(err)
			}
			committed, _ := s.GetProject(ids.Project)
			if committed.Revision != before.Revision+1 {
				t.Fatal("original call did not commit")
			}
			diff := project.DiffNotes(&before.State, &committed.State, 2000)
			if diff.Added != view.Diff.Added || diff.Updated != view.Diff.Updated || diff.Removed != view.Diff.Removed {
				t.Fatalf("preview differed from commit: %+v %+v", view.Diff, diff)
			}
			if name == "project__transform" && diff.Changes[0].After.Pitch != view.Diff.Changes[0].After.Pitch {
				t.Fatal("transform preview used different computation")
			}
			if _, err := s.UndoProject(ids.Project, "undo-candidate", "gui", committed.Revision); err != nil {
				t.Fatal(err)
			}
			restored, _ := s.GetProject(ids.Project)
			if !reflect.DeepEqual(before.State, restored.State) {
				t.Fatal("shared undo did not restore pre-candidate music")
			}
		})
	}
}

func TestProjectPreviewRefusalsDoNotChangeMusic(t *testing.T) {
	for _, scenario := range []string{"read", "outside", "stale", "finished", "discarded", "unsupported", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			mode := "write"
			if scenario == "read" {
				mode = "read"
			}
			ids, tools, call := previewFixture(t, mode, func(ids scopeIDs, rev int64) (string, string) {
				clip := ids.C1
				if scenario == "outside" {
					clip = ids.C2
				}
				op := fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":0,"duration":10,"pitch":72,"velocity":80,"channel":0}]}`, clip)
				if scenario == "unsupported" {
					op = `{"kind":"project.rename","title":"still supported by normal approval"}`
				}
				if scenario == "invalid" {
					op = fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":950,"duration":100,"pitch":72,"velocity":80,"channel":0}]}`, clip)
				}
				return "project__edit", editPayload(ids.Project, rev, "reject-preview", op)
			})
			s := tools.svc
			if scenario == "stale" {
				p, _ := s.GetProject(ids.Project)
				if _, err := s.SubmitProjectOps(ids.Project, "user-edited", "gui", p.Revision, []project.Op{{Kind: "project.rename", Title: "用户新名称"}}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "finished" {
				if err := s.store.SetRunStatus(context.Background(), tools.runID, "cancelled", ""); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "discarded" {
				if _, err := s.DenyToolCall(call.ID, "丢弃候选"); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.GetProject(ids.Project)
			if _, err := s.PreviewProjectEdit(call.ID); err == nil {
				t.Fatal("invalid preview accepted")
			}
			if _, err := s.ApproveProjectEditPreview(call.ID, "anything"); err == nil {
				t.Fatal("invalid candidate adopted")
			}
			after, _ := s.GetProject(ids.Project)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected preview changed music")
			}
			if scenario == "unsupported" {
				if _, err := s.ApproveToolCall(call.ID); err != nil {
					t.Fatal("generic approval was removed", err)
				}
				if _, err := tools.Call(context.Background(), call.ToolName, call.Arguments); err == nil { // clip-scoped project.rename should still be rejected by the existing scope.
					t.Fatal("preview absence bypassed frozen scope")
				}
			}
		})
	}
}

func TestProjectPreviewBecomesStaleBeforeAdoption(t *testing.T) {
	ids, tools, call := previewFixture(t, "write", func(ids scopeIDs, rev int64) (string, string) {
		return "project__transform", transformPayload(ids.Project, rev, "preview-stale", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{"transpose":{"semitones":1}}`, false)
	})
	s := tools.svc
	view, err := s.PreviewProjectEdit(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitProjectOps(ids.Project, "newer-edit", "gui", view.BaseRevision, []project.Op{{Kind: "project.rename", Title: "用户手改"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveProjectEditPreview(call.ID, view.ArgumentsHash); err == nil || !strings.Contains(err.Error(), "过时") {
		t.Fatalf("stale candidate accepted: %v", err)
	}
	current, _ := s.store.GetToolCall(context.Background(), call.ID)
	if current.State != storage.CallPending {
		t.Fatal("failed adoption changed approval state")
	}
}
