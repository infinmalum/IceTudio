package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"icetudio/internal/app"
	"icetudio/internal/ipc"
	"icetudio/internal/project"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
)

func TestCLIAndIPCReconcileNativeProjectCall(t *testing.T) {
	ctx := context.Background()
	s, err := testfixture.Open(filepath.Join(t.TempDir(), "cli-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	svc := app.New(app.Options{Store: s})
	p, err := svc.CreateProject("CLI核对", 480)
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
	_, calls, err := s.AddAssistantMessage(ctx, c.ID, run.ID, "", []storage.MessageToolCall{{ID: "cli-recovery", Name: "project__edit", Arguments: fmt.Sprintf(`{"projectId":%q,"opId":"lost-cli","baseRevision":0,"ops":[{"kind":"project.rename","title":"已提交"}]}`, p.ID)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SubmitProjectOps(p.ID, "lost-cli", "agent:"+run.ID, 0, []project.Op{{Kind: "project.rename", Title: "已提交"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, run.ID, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	server, err := ipc.Start(svc, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := ipc.Connect(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = client.Call("calls.reconcileProject", map[string]any{"callId": calls[0].ID, "projectId": "retarget"}, nil); err == nil {
		t.Fatal("IPC allowed retargeting field")
	}
	if code := cmdApprovals(client, []string{"reconcile-project", calls[0].ID}); code != 0 {
		t.Fatal("CLI recovery failed", code)
	}
	var result storage.ProjectCallRecovery
	if err = client.Call("calls.reconcileProject", map[string]any{"callId": calls[0].ID}, &result); err != nil || result.Outcome != "committed" {
		t.Fatal(result, err)
	}
	if code := cmdApprovals(client, []string{"reconcile-project", calls[0].ID, "extra"}); code == 0 {
		t.Fatal("extra CLI arguments ignored")
	}
	if code := cmdApprovals(client, []string{"reconcile-project", "missing"}); code == 0 {
		t.Fatal("unknown call accepted")
	}
	actual, err := svc.GetProject(p.ID)
	if err != nil || actual.Revision != 1 {
		t.Fatal("reconciliation replayed edit", err)
	}
}
