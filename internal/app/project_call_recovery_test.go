package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func TestProjectCallRecoveryUsesActualNativeEditAndTransform(t *testing.T) {
	for _, worker := range []bool{false, true} {
		for _, tool := range []string{"project__edit", "project__transform"} {
			t.Run(fmt.Sprintf("%s-worker-%t", tool, worker), func(t *testing.T) {
				ctx, ids, tools := scopeFixture(t, nil)
				s := tools.svc
				if worker {
					data := storage.DefaultRuntimeConfig()
					data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 1}
					data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{Name: "worker", ContextBudget: 8192, Model: tools.profile.Model}}}
					if _, err := s.configureDefault(data); err != nil {
						t.Fatal(err)
					}
				}
				root, profile, err := s.store.CreateRunWithOptions(ctx, tools.sessionID, storage.CreateRunOptions{})
				if err != nil {
					t.Fatal(err)
				}
				tools.runID, tools.profile = root.ID, profile
				if worker {
					parent, _ := s.store.GetSession(ctx, tools.sessionID)
					child, err := s.store.CreateDelegationSession(ctx, parent.ContextID, storage.DefaultConfigID, "恢复 worker")
					if err != nil {
						t.Fatal(err)
					}
					run, snapshot, err := s.store.CreateRunWithOptions(ctx, child.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker"})
					if err != nil {
						t.Fatal(err)
					}
					tools.sessionID, tools.runID, tools.profile = child.ID, run.ID, snapshot
				}
				before, _ := s.GetProject(ids.Project)
				args := transformPayload(ids.Project, before.Revision, "recovered-edit", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{"transpose":{"semitones":2}}`, false)
				if tool == "project__edit" {
					args = editPayload(ids.Project, before.Revision, "recovered-edit", fmt.Sprintf(`{"kind":"note.add","clipId":%q,"notes":[{"start":480,"duration":100,"pitch":64,"velocity":90,"channel":0}]}`, ids.C1))
				}
				_, calls, err := s.store.AddAssistantMessage(ctx, tools.sessionID, tools.runID, "", []storage.MessageToolCall{{ID: "lost-reply", Name: tool, Arguments: args}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				call := calls[0]
				if err = s.store.BeginToolCall(ctx, call.ID); err != nil {
					t.Fatal(err)
				}
				raw, err := tools.Call(ctx, tool, args)
				if err != nil {
					t.Fatal(err)
				}
				var expected project.Result
				if err = json.Unmarshal([]byte(raw), &expected); err != nil {
					t.Fatal(err)
				}
				// Simulate cancellation after the atomic music commit, before the
				// engine could persist its tool result. No duplicate tool dispatch.
				if worker {
					if err = s.store.SetRunStatus(ctx, tools.runID, "cancelled", ""); err != nil {
						t.Fatal(err)
					}
				}
				if err = s.store.SetRunStatus(ctx, root.ID, "cancelled", ""); err != nil {
					t.Fatal(err)
				}
				committed, _ := s.GetProject(ids.Project)
				if _, err = s.ResolveToolCall(call.ID, storage.CallFailed, "", "错误地声称未执行"); err == nil {
					t.Fatal("manual failure contradicted committed music")
				}
				// Old public success entry must use evidence, never supplied output.
				confirmed, err := s.ResolveToolCall(call.ID, storage.CallSucceeded, "fabricated-output", "用户点击核对")
				if err != nil {
					t.Fatal(err)
				}
				var got project.Result
				if err = json.Unmarshal([]byte(confirmed.Result), &got); err != nil || !reflect.DeepEqual(got, expected) {
					t.Fatalf("result not authoritative: %+v %v", confirmed, err)
				}
				after, _ := s.GetProject(ids.Project)
				if !reflect.DeepEqual(after, committed) {
					t.Fatal("query dispatched another edit")
				}
				out, err := s.ReconcileProjectToolCall(call.ID)
				if err != nil || out.Outcome != "committed" {
					t.Fatal(out, err)
				}
				if _, err = s.UndoProject(ids.Project, "undo-recovered", "gui", after.Revision); err != nil {
					t.Fatal(err)
				}
				restored, _ := s.GetProject(ids.Project)
				if !reflect.DeepEqual(before.State, restored.State) {
					t.Fatal("recovery broke common undo")
				}
			})
		}
	}
}

func TestProjectCallRecoveryNoResultCannotBecomeFabricatedSuccess(t *testing.T) {
	_, tools, call := previewFixture(t, "write", func(ids scopeIDs, rev int64) (string, string) {
		return "project__transform", transformPayload(ids.Project, rev, "never-committed", fmt.Sprintf(`{"clipIds":[%q]}`, ids.C1), `{"transpose":{"semitones":1}}`, false)
	})
	ctx := context.Background()
	if err := tools.svc.store.BeginToolCall(ctx, call.ID); err != nil {
		t.Fatal(err)
	}
	if err := tools.svc.store.SetRunStatus(ctx, tools.runID, "interrupted", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.svc.ResolveToolCall(call.ID, storage.CallSucceeded, "guessed", "not proof"); err == nil {
		t.Fatal("absence was called success")
	}
	out, err := tools.svc.ReconcileProjectToolCall(call.ID)
	if err != nil || out.Outcome != "not_found" {
		t.Fatal(out, err)
	}
	still, _ := tools.svc.store.GetToolCall(ctx, call.ID)
	if still.State != storage.CallExecutionUnknown {
		t.Fatal("absence was called failure")
	}
}
