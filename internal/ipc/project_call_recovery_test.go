package ipc

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
)

func TestReconcileNativeProjectCallOverIPC(t *testing.T) {
	ctx := context.Background()
	s, err := testfixture.Open(filepath.Join(t.TempDir(), "ipc-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	svc := app.New(app.Options{Store: s})
	p, err := svc.CreateProject("IPC核对", 480)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetConversationProject(c.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, calls, err := s.AddAssistantMessage(ctx, c.ID, run.ID, "", []storage.MessageToolCall{{ID: "ipc-recovery", Name: "project__edit", Arguments: fmt.Sprintf(`{"projectId":%q,"opId":"lost-ipc","baseRevision":0,"ops":[{"kind":"project.rename","title":"已提交"}]}`, p.ID)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SubmitProjectOps(p.ID, "lost-ipc", "agent:"+run.ID, 0, []project.Op{{Kind: "project.rename", Title: "已提交"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, run.ID, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
	client := mustConnect(t, startServer(t, svc))
	if err = client.Call("calls.reconcileProject", map[string]any{"callId": calls[0].ID, "projectId": "retarget"}, nil); err == nil {
		t.Fatal("IPC allowed retargeting field")
	}
	var result storage.ProjectCallRecovery
	if err = client.Call("calls.reconcileProject", map[string]any{"callId": calls[0].ID}, &result); err != nil || result.Outcome != "committed" {
		t.Fatal(result, err)
	}
	if err = client.Call("calls.reconcileProject", map[string]any{"callId": "missing"}, nil); err == nil {
		t.Fatal("unknown call accepted")
	}
	actual, err := svc.GetProject(p.ID)
	if err != nil || actual.Revision != 1 {
		t.Fatal("reconciliation replayed edit", err)
	}
}
