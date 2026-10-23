package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"icetudio/internal/model"
)

func searchRoot(t *testing.T, limit int) (*Store, Conversation, Run) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "search", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.Budget.MaxToolCalls = limit
	p.Data.Execution.WorkerSources = []WorkerSource{{Name: "worker"}}
	if _, err := s.UpdateRuntimeConfig(ctx, p); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetConversationOptions(ctx, c.ID, ConversationOptions{WebSearch: true}); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.CreateRunWithOptions(ctx, c.ID, CreateRunOptions{SearchTools: []string{"web__search"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginModelStep(ctx, r.ID, "request"); err != nil {
		t.Fatal(err)
	}
	return s, c, r
}
func TestSearchOptionsFreezeAndWorkerInheritance(t *testing.T) {
	s, c, root := searchRoot(t, 5)
	ctx := context.Background()
	if _, err := s.SetConversationOptions(ctx, c.ID, ConversationOptions{}); err != nil {
		t.Fatal(err)
	}
	resources, err := s.RunResources(ctx, root.ID)
	if err != nil || !resources.Options.WebSearch || len(resources.SearchTools) != 1 {
		t.Fatal(resources, err)
	}
	session, err := s.CreateDelegationSession(ctx, c.ContextID, DefaultConfigID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	child, profile, err := s.CreateRunWithOptions(ctx, session.ID, CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Resources.Options.WebSearch {
		t.Fatal("worker lost root search ceiling")
	}
	if _, err := s.BeginModelStep(ctx, child.ID, "worker request"); err != nil {
		t.Fatal(err)
	}
	item, err := s.BeginNativeSearch(ctx, child.ID, "openai")
	if err != nil || item.Reserved != 3 {
		t.Fatal(item, err)
	}
	zero := 0
	if err := s.FinishNativeSearch(ctx, item.ID, model.SearchReport{Calls: &zero}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunStatus(ctx, child.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunStatus(ctx, root.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.CreateRun(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginModelStep(ctx, next.ID, "next"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginNativeSearch(ctx, next.ID, "openai"); err == nil {
		t.Fatal("disabled option allowed native search")
	}
}
func TestNativeSearchBudgetReservationConcurrentAndRecovery(t *testing.T) {
	s, c, root := searchRoot(t, 3)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan SearchRequest, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := s.BeginNativeSearch(ctx, root.ID, "openai")
			if err != nil {
				failures <- err
			} else {
				results <- item
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	if len(results) != 1 || len(failures) != 1 {
		t.Fatal("concurrent requests exceeded budget")
	}
	item := <-results
	if err := s.AppendSearchEvent(ctx, item.ID, model.SearchEvent{Type: "searching", ID: "ws"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListConversationSearch(ctx, c.ID)
	if err != nil || len(records) != 1 || records[0].State != "execution_unknown" || len(records[0].Report.Events) != 1 || records[0].Report.Calls != nil {
		t.Fatal(records, err)
	}
	var calls int
	if err := s.db.QueryRow(`SELECT tool_calls FROM root_budgets WHERE root_run_id=?`, root.ID).Scan(&calls); err != nil || calls != 3 {
		t.Fatal(calls, err)
	}
}
func TestNativeSearchRefundAndProviderStatePersistence(t *testing.T) {
	s, c, root := searchRoot(t, 4)
	ctx := context.Background()
	item, err := s.BeginNativeSearch(ctx, root.ID, "openai")
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	report := model.SearchReport{Calls: &one, Citations: []model.Citation{{URL: "https://example.org", Title: "source"}}, Text: "answer"}
	if err := s.FinishNativeSearch(ctx, item.ID, report, nil); err != nil {
		t.Fatal(err)
	}
	var calls int
	if err := s.db.QueryRow(`SELECT tool_calls FROM root_budgets WHERE root_run_id=?`, root.ID).Scan(&calls); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	reasoning := []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"r1","encrypted_content":"opaque"}`)}
	if _, _, err := s.AddAssistantMessage(ctx, c.ID, root.ID, "answer", nil, reasoning); err != nil {
		t.Fatal(err)
	}
	entries, err := s.HistoryEntries(ctx, c.ID)
	if err != nil || len(entries) != 1 || len(entries[0].ProviderState) != 1 {
		t.Fatal(entries, err)
	}
	next, err := s.BeginNativeSearch(ctx, root.ID, "openai")
	if err != nil || next.Reserved != 3 {
		t.Fatal(next, err)
	}
	if err := s.FinishNativeSearch(ctx, next.ID, model.SearchReport{}, errors.New("stream lost")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginNativeSearch(ctx, root.ID, "openai"); err == nil {
		t.Fatal("unknown request refunded its budget")
	}
}

func TestGeminiSearchBudgetUsesHostRequestsAndKeepsUnknownInternalCounts(t *testing.T) {
	s, c, root := searchRoot(t, 2)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		item, err := s.BeginNativeSearch(ctx, root.ID, "gemini")
		if err != nil || item.Reserved != 1 || item.Report.BudgetUnit != "grounded_request" {
			t.Fatal(item, err)
		}
		one := 1
		report := model.SearchReport{BudgetUnit: "grounded_request", BudgetUsed: &one, Events: []model.SearchEvent{{Type: "google_search_call", Queries: []string{"q1", "q2", "q3", "q4"}}}}
		if err := s.FinishNativeSearch(ctx, item.ID, report, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginNativeSearch(ctx, root.ID, "gemini"); err == nil {
		t.Fatal("Gemini host request budget exceeded")
	}
	records, err := s.ListConversationSearch(ctx, c.ID)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	for _, record := range records {
		if record.Report.Calls != nil || record.Report.BudgetUsed == nil || *record.Report.BudgetUsed != 1 {
			t.Fatal("internal count fabricated", record)
		}
	}
}
