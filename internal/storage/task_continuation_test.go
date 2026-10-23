package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"icetudio/internal/materials"
)

func waitingFixed(t *testing.T) (*Store, Conversation, Run) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	d := fixedData()
	d.Execution.Stages[0].InputPrompt = "请提供材料"
	p, err := s.CreateRuntimeConfig(ctx, "wait", d)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMessage(ctx, c.ID, r.ID, RoleUser, "original goal"); err != nil {
		t.Fatal(err)
	}
	g, _ := s.GetTaskGraph(ctx, r.ID)
	if err = s.AcknowledgeCoordination(ctx, r.ID, g.Coordination.EventSeq, "waiting", "请提供材料"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRunStatus(ctx, r.ID, "failed", "等待输入"); err != nil {
		t.Fatal(err)
	}
	return s, c, r
}

func TestFixedContinuationAtomicInputFrozenResourcesAndBudget(t *testing.T) {
	s, c, r := waitingFixed(t)
	ctx := context.Background()
	before, _ := s.GetTaskGraph(ctx, r.ID)
	if before.Tasks[0].State != "pending" || before.WaitingTaskID != "collect" {
		t.Fatalf("lost gate: %+v", before)
	}
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := s.GetTaskGraph(ctx, r.ID)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(recovered)
	if string(a) != string(b) {
		t.Fatal("waiting restart mutated graph")
	}
	// Simulate a long user pause; active time and counters cannot reset.
	paused := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := s.db.Exec(`UPDATE runs SET updated_at=? WHERE id=?`, paused.Format(time.RFC3339Nano), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE root_budgets SET deadline=?,model_calls=2,tool_calls=1,tokens=40 WHERE root_run_id=?`, paused.Add(30*time.Second).Format(time.RFC3339Nano), r.ID); err != nil {
		t.Fatal(err)
	}
	files, err := materials.Prepare([]materials.Upload{{Name: "more.txt", Data: base64.StdEncoding.EncodeToString([]byte("additional data"))}})
	if err != nil {
		t.Fatal(err)
	}
	// Force an error after inserting the message. No partial answer may remain.
	if _, err = s.db.Exec(`CREATE TRIGGER reject_continue BEFORE INSERT ON task_continuations BEGIN SELECT RAISE(ABORT,'reject'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ContinueFixedRun(ctx, r.ID, "collect", "answer", files); err == nil {
		t.Fatal("commit failure ignored")
	}
	messages, _ := s.ListMessages(ctx, c.ID)
	if len(messages) != 1 {
		t.Fatal("partial input persisted", messages)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_continue`); err != nil {
		t.Fatal(err)
	}
	p, remaining, err := s.ContinueFixedRun(ctx, r.ID, "collect", "answer", files)
	if err != nil {
		t.Fatal(err)
	}
	if remaining < 29*time.Second || remaining > 31*time.Second || !p.ResourcesFrozen || p.Execution.Stages[0].InputPrompt != "请提供材料" {
		t.Fatalf("snapshot/time: %+v %s", p, remaining)
	}
	after, _ := s.GetTaskGraph(ctx, r.ID)
	if after.RunStatus != "running" || after.Goal != "original goal" || after.Tasks[0].Input != "answer" || after.Tasks[0].InputMessageID == "" || after.Budget.ModelCalls != 2 || after.Budget.ToolCalls != 1 || after.Budget.Tokens != 40 || len(after.Attempts) != 0 {
		t.Fatalf("continuation changed contract: %+v", after)
	}
	got, err := s.RunInputFiles(ctx, r.ID)
	if err != nil || len(got) != 1 || got[0].Text != "additional data" {
		t.Fatal("input file not forwarded", got, err)
	}
	if _, _, err = s.ContinueFixedRun(ctx, r.ID, "collect", "duplicate", nil); err == nil {
		t.Fatal("duplicate continuation allowed")
	}
}

func TestFixedContinuationRejectsUnsafeStates(t *testing.T) {
	for _, tc := range []string{"wrong stage", "empty input", "exhausted time", "exhausted calls", "newer task", "unknown effect", "failed attempt"} {
		t.Run(tc, func(t *testing.T) {
			s, c, r := waitingFixed(t)
			ctx := context.Background()
			root, stage, input := r.ID, "collect", "answer"
			switch tc {
			case "wrong stage":
				stage = "report"
			case "empty input":
				input = " "
			case "exhausted time":
				_, _ = s.db.Exec(`UPDATE root_budgets SET deadline='2000-01-01T00:00:00Z' WHERE root_run_id=?`, r.ID)
			case "exhausted calls":
				_, _ = s.db.Exec(`UPDATE root_budgets SET model_calls=1000 WHERE root_run_id=?`, r.ID)
			case "newer task":
				if _, _, err := s.CreateRun(ctx, c.ID); err != nil {
					t.Fatal(err)
				}
			case "unknown effect":
				_, calls, err := s.AddAssistantMessage(ctx, c.ID, r.ID, "", []MessageToolCall{{ID: "unknown", Name: "local__exec", Arguments: "{}"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = s.db.Exec(`UPDATE tool_calls SET state='execution_unknown' WHERE id=?`, calls[0].ID)
			case "failed attempt":
				_, _ = s.db.Exec(`UPDATE task_items SET state='interrupted' WHERE root_run_id=? AND id='report'`, r.ID)
			}
			if _, _, err := s.ContinueFixedRun(ctx, root, stage, input, nil); err == nil {
				t.Fatal("unsafe continuation accepted")
			}
			var n int
			if err := s.db.QueryRow(`SELECT count(*) FROM task_inputs WHERE root_run_id=?`, r.ID).Scan(&n); err != nil || n != 0 {
				t.Fatal("rejected continuation persisted input", n, err)
			}
		})
	}
}

func TestFixedContinuationConcurrentAnswerOnlyOneCommits(t *testing.T) {
	s, _, r := waitingFixed(t)
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.ContinueFixedRun(context.Background(), r.ID, "collect", "answer", nil)
			out <- err
		}()
	}
	wg.Wait()
	close(out)
	successes := 0
	for err := range out {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("committed %d continuations", successes)
	}
}

func TestFixedContinuationInterruptedPendingWithoutReplay(t *testing.T) {
	s, _, r := waitingFixed(t)
	ctx := context.Background()
	// An interrupted run at a safe boundary can continue even without answering
	// its next input gate. The scheduler will surface that gate again.
	_, _ = s.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, r.ID)
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetTaskGraph(ctx, r.ID)
	if before.Tasks[0].State != "pending" {
		t.Fatal("pending stage cancelled")
	}
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetTaskGraph(ctx, r.ID)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("recovery is not idempotent")
	}
	if _, _, err := s.ContinueFixedRun(ctx, r.ID, "", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestFixedInputPromptValidation(t *testing.T) {
	for _, prompt := range []string{"  ", strings.Repeat("x", 4001)} {
		d := fixedData()
		d.Execution.Stages[0].InputPrompt = prompt
		if err := validateRuntimeConfigData(d); err == nil {
			t.Fatal("invalid prompt accepted")
		}
	}
}
