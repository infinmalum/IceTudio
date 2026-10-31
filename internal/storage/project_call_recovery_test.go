package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"icetudio/internal/project"
)

type nativeRecoveryFixture struct {
	store   *Store
	path    string
	project Project
	session Session
	run     Run
	call    ToolCall
}

func newNativeRecoveryFixture(t *testing.T) nativeRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	f := nativeRecoveryFixture{path: filepath.Join(t.TempDir(), "native-recovery.db")}
	var err error
	f.store, err = testOpen(f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.Close() })
	f.project, err = f.store.CreateProject(ctx, "原工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	f.session, err = f.store.CreateSession(ctx, "核对")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetSessionProject(ctx, f.session.ID, f.project.ID); err != nil {
		t.Fatal(err)
	}
	f.run, _, err = f.store.CreateRun(ctx, f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"projectId":%q,"opId":"lost-op","baseRevision":0,"ops":[{"kind":"project.rename","title":"已保存"}]}`, f.project.ID)
	_, calls, err := f.store.AddAssistantMessage(ctx, f.session.ID, f.run.ID, "", []MessageToolCall{{ID: "lost-call", Name: "project__edit", Arguments: args}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.call = calls[0]
	if err = f.store.BeginToolCall(ctx, f.call.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f nativeRecoveryFixture) commit(t *testing.T, source string) project.Result {
	t.Helper()
	result, err := f.store.SubmitProjectOps(context.Background(), f.project.ID, "lost-op", source, 0, []project.Op{{Kind: "project.rename", Title: "已保存"}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeProjectRecoveryReopenRebindUndoAndHistory(t *testing.T) {
	ctx := context.Background()
	f := newNativeRecoveryFixture(t)
	want := f.commit(t, "agent:"+f.run.ID)
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reopened
	defer reopened.Close()
	if err = f.store.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateProject(ctx, "新绑定工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetSessionProject(ctx, f.session.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.UndoProject(ctx, f.project.ID, "undo-after-lost", "gui", want.Revision); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.GetProject(ctx, f.project.ID)
	history, _ := f.store.ProjectHistory(ctx, f.project.ID)
	out, err := f.store.ReconcileProjectToolCall(ctx, f.call.ID)
	if err != nil || out.Outcome != "committed" || out.ProjectID != f.project.ID || out.CurrentRevision != 2 || out.Result == nil || !reflect.DeepEqual(*out.Result, want) {
		t.Fatalf("wrong recovery: %+v %v", out, err)
	}
	after, _ := f.store.GetProject(ctx, f.project.ID)
	historyAfter, _ := f.store.ProjectHistory(ctx, f.project.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(history, historyAfter) {
		t.Fatal("recovery rewrote music/history")
	}
	call, _ := f.store.GetToolCall(ctx, f.call.ID)
	if call.State != CallSucceeded || call.SourceScope != ScopeWorkspace || !strings.Contains(call.Error, "未重放") {
		t.Fatalf("wrong call receipt: %+v", call)
	}
	var got project.Result
	if err = json.Unmarshal([]byte(call.Result), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("fabricated model result")
	}
	var step string
	if err = f.store.db.QueryRowContext(ctx, `SELECT status FROM steps WHERE id=?`, f.call.StepID).Scan(&step); err != nil || step != "succeeded" {
		t.Fatal("step not reconciled", err)
	}
	run, _ := f.store.GetRun(ctx, f.run.ID)
	if run.Status != "interrupted" {
		t.Fatal("reconciliation resumed task")
	}
	entries, err := f.store.HistoryEntries(ctx, f.session.ID)
	if err != nil || entries[len(entries)-1].Content != call.Result {
		t.Fatal("model history lacks verified result")
	}
	if _, err = f.store.ReconcileProjectToolCall(ctx, f.call.ID); err != nil {
		t.Fatal(err)
	}
	repeated, _ := f.store.GetToolCall(ctx, f.call.ID)
	if !reflect.DeepEqual(call, repeated) {
		t.Fatal("repeated query overwrote receipt")
	}
	if err = f.store.FinishToolCall(ctx, f.call.ID, CallSucceeded, "late guessed result", "", ScopePublic); err == nil {
		t.Fatal("late response changed reconciled result")
	}
	if next, _, err := f.store.CreateRun(ctx, f.session.ID); err != nil {
		t.Fatal("verified result still blocked next explicit task", err)
	} else if err = f.store.SetRunStatus(ctx, next.ID, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
}

func TestNativeProjectRecoveryAbsenceConflictAndInvalidEvidence(t *testing.T) {
	for _, which := range []string{"not_found", "project_deleted", "source", "base", "corrupt", "metadata", "scope", "readonly", "wrong_project", "no_opid", "dry", "external", "pending"} {
		t.Run(which, func(t *testing.T) {
			ctx := context.Background()
			f := newNativeRecoveryFixture(t)
			if which != "not_found" && which != "project_deleted" {
				source := "agent:" + f.run.ID
				if which == "source" {
					source = "gui"
				}
				f.commit(t, source)
			}
			if err := f.store.SetRunStatus(ctx, f.run.ID, "cancelled", ""); err != nil {
				t.Fatal(err)
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := f.store.db.ExecContext(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch which {
			case "project_deleted":
				if err := f.store.DeleteProject(ctx, f.project.ID); err != nil {
					t.Fatal(err)
				}
			case "base":
				exec(`UPDATE tool_calls SET arguments=replace(arguments,'"baseRevision":0','"baseRevision":1') WHERE id=?`, f.call.ID)
			case "corrupt":
				exec(`UPDATE project_revisions SET result='{}' WHERE project_id=?`, f.project.ID)
			case "metadata":
				exec(`UPDATE project_revisions SET source='gui' WHERE project_id=?`, f.project.ID)
			case "scope":
				exec(`UPDATE runs SET project_scope='' WHERE id=?`, f.run.ID)
			case "readonly":
				exec(`UPDATE runs SET project_scope=replace(project_scope,'"write"','"read"') WHERE id=?`, f.run.ID)
			case "wrong_project":
				exec(`UPDATE tool_calls SET arguments=replace(arguments,?, 'wrong_project') WHERE id=?`, f.project.ID, f.call.ID)
			case "no_opid":
				exec(`UPDATE tool_calls SET arguments=replace(arguments,'lost-op','') WHERE id=?`, f.call.ID)
			case "dry":
				exec(`UPDATE tool_calls SET tool_name='project__transform',arguments=? WHERE id=?`, fmt.Sprintf(`{"projectId":%q,"opId":"lost-op","baseRevision":0,"dryRun":true}`, f.project.ID), f.call.ID)
			case "external":
				exec(`UPDATE tool_calls SET tool_name='external__project__edit' WHERE id=?`, f.call.ID)
			case "pending":
				exec(`UPDATE tool_calls SET state='pending' WHERE id=?`, f.call.ID)
			}
			before, _ := f.store.GetToolCall(ctx, f.call.ID)
			out, err := f.store.ReconcileProjectToolCall(ctx, f.call.ID)
			expected := ""
			switch which {
			case "not_found", "project_deleted":
				expected = which
			case "source", "base":
				expected = "conflict"
			}
			if expected == "" {
				if err == nil {
					t.Fatal("invalid evidence accepted", out)
				}
			} else if err != nil || out.Outcome != expected || out.Result != nil {
				t.Fatalf("unexpected evidence: %+v %v", out, err)
			}
			after, _ := f.store.GetToolCall(ctx, f.call.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unverified outcome changed call")
			}
			if which == "source" || which == "base" {
				if err = f.store.ResolveToolCall(ctx, f.call.ID, CallFailed, "", "仅凭界面猜测未执行"); err == nil {
					t.Fatal("manual failure contradicted history")
				}
			}
		})
	}
}

func TestNativeProjectRecoveryAtomicityAndConcurrentQueries(t *testing.T) {
	ctx := context.Background()
	f := newNativeRecoveryFixture(t)
	f.commit(t, "agent:"+f.run.ID)
	if err := f.store.SetRunStatus(ctx, f.run.ID, "interrupted", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_native_receipt BEFORE UPDATE ON steps BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.GetToolCall(ctx, f.call.ID)
	if _, err := f.store.ReconcileProjectToolCall(ctx, f.call.ID); err == nil {
		t.Fatal("ignored step failure")
	}
	after, _ := f.store.GetToolCall(ctx, f.call.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("step failure left half-success")
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER fail_native_receipt`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := f.store.ReconcileProjectToolCall(ctx, f.call.ID)
			if err == nil && out.Outcome != "committed" {
				err = fmt.Errorf("unexpected outcome %s", out.Outcome)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.db.Exec(`UPDATE tool_calls SET result='forged-result' WHERE id=?`, f.call.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReconcileProjectToolCall(ctx, f.call.ID); err == nil {
		t.Fatal("mismatched successful receipt was described as verified")
	}
	history, err := f.store.ProjectHistory(ctx, f.project.ID)
	if err != nil || len(history) != 1 {
		t.Fatal("query replayed music", err)
	}
}

func TestNativeProjectRecoveryCannotForgeSuccessAndKeepsExternalDisposition(t *testing.T) {
	for _, kind := range []string{"native", "external", "dry"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			f := newNativeRecoveryFixture(t)
			if err := f.store.SetRunStatus(ctx, f.run.ID, "interrupted", ""); err != nil {
				t.Fatal(err)
			}
			if kind == "external" {
				if _, err := f.store.db.Exec(`UPDATE tool_calls SET tool_name='remote__write' WHERE id=?`, f.call.ID); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "dry" {
				if _, err := f.store.db.Exec(`UPDATE tool_calls SET tool_name='project__transform',arguments='{"dryRun":true}' WHERE id=?`, f.call.ID); err != nil {
					t.Fatal(err)
				}
			}
			err := f.store.ResolveToolCall(ctx, f.call.ID, CallSucceeded, "human-verified-output", "用户核对目标服务的直接证据")
			if (err == nil) == (kind == "native") {
				t.Fatalf("wrong disposition for %s: %v", kind, err)
			}
			if kind == "native" {
				if err = f.store.ResolveToolCall(ctx, f.call.ID, CallFailed, "", "   "); err == nil {
					t.Fatal("blank note treated as verification evidence")
				}
			}
		})
	}
}
