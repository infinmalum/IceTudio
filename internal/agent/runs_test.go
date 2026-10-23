package agent

import (
	"context"
	"icetudio/internal/testfixture"
	"path/filepath"
	"testing"
)

func TestCancelPersistsAndSignals(t *testing.T) {
	ctx := context.Background()
	store, err := testfixture.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := testfixture.Session(store, ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	runs := NewRuns(store)
	run, runCtx, _, err := runs.Start(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := runs.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	if runCtx.Err() != context.Canceled {
		t.Fatalf("task context not cancelled: %v", runCtx.Err())
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("run: %+v, %v", got, err)
	}
	if err := runs.Cancel(run.ID); err == nil {
		t.Fatal("second cancellation accepted")
	}
	second, secondCtx, _, err := runs.Start(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs.Close()
	if secondCtx.Err() != context.Canceled {
		t.Fatal("shutdown did not cancel task")
	}
	got, err = store.GetRun(ctx, second.ID)
	if err != nil || got.Status != "interrupted" {
		t.Fatalf("run after shutdown: %+v, %v", got, err)
	}
}
