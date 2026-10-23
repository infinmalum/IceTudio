package app

import (
	"context"
	"encoding/json"
	"icetudio/internal/storage"
	"strings"
	"testing"
)

func TestWorkerTracePagingAccumulatesSensitivity(t *testing.T) {
	svc, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	data := storage.DefaultRuntimeConfig()
	data.PlanEnabled = true
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker"}}
	p, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := store.CreateRunWithOptions(ctx, c.ID, storage.CreateRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	specs := []storage.TaskSpec{{ID: "a", Worker: "worker", Task: "a", AccessMode: "read"}, {ID: "b", Worker: "worker", Task: "b", AccessMode: "read"}}
	if _, err = store.SetTaskGraph(ctx, root.ID, 0, specs); err != nil {
		t.Fatal(err)
	}
	create := func(spec storage.TaskSpec) (storage.Run, storage.RunConfigSnapshot) {
		session, e := store.CreateDelegationSession(ctx, c.ContextID, storage.DefaultConfigID, spec.ID)
		if e != nil {
			t.Fatal(e)
		}
		run, p, e := store.CreateRunWithOptions(ctx, session.ID, storage.CreateRunOptions{ParentRunID: root.ID, Depth: 1, WorkerName: "worker", TaskSpec: &spec})
		if e != nil {
			t.Fatal(e)
		}
		return run, p
	}
	a, _ := create(specs[0])
	b, profile := create(specs[1])
	content := strings.Repeat("观察材料", 600)
	observation, err := store.RecordObservation(ctx, a.ID, content, storage.ScopeCredential)
	if err != nil {
		t.Fatal(err)
	}
	tool := bookkeepingTools{svc: svc, profile: profile, sessionID: b.SessionID, runID: b.ID, delegated: true}
	var assembled strings.Builder
	offset := 0
	for {
		raw, _ := json.Marshal(map[string]any{"id": observation.ID, "offset": offset})
		result, err := tool.traceCall(ctx, "local__get_worker_trace", string(raw))
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Trace storage.WorkerTrace `json:"trace"`
			Next  int                 `json:"nextOffset"`
		}
		if err = json.Unmarshal([]byte(result), &page); err != nil {
			t.Fatal(err)
		}
		assembled.WriteString(page.Trace.Content)
		if page.Next < 0 {
			break
		}
		offset = page.Next
	}
	if assembled.String() != content {
		t.Fatal("paging lost UTF-8 content")
	}
	scopes, err := svc.sessionScopes(ctx, b.SessionID, profile)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range scopes {
		if scope == storage.ScopeCredential {
			found = true
		}
	}
	if !found {
		t.Fatal("trace sensitivity was not inherited")
	}
	own, err := store.RecordObservation(ctx, b.ID, "自己的观察", storage.ScopePersonal)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"id": own.ID})
	if _, err = tool.traceCall(ctx, "local__get_worker_trace", string(raw)); err == nil {
		t.Fatal("worker could read own trace through cross-worker API")
	}
}
