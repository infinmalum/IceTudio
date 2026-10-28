package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// This model exercises the real engine, discovery, approval, delegation and
// SQLite paths; it neither contacts a provider nor simulates the project API.
type projectWorkerModel struct {
	ids     scopeIDs
	tool    string
	denial  string
	step    int
	outside bool
}

func (m *projectWorkerModel) Chat(_ context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	answer := func(text string) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: text}}, nil
	}
	call := func(id, name, raw string) (model.ChatResponse, error) {
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall(id, name, raw)}}}, nil
	}
	if source.Name != "music-worker" {
		if messages[len(messages)-1].Role == "tool" {
			return answer("子任务已结束，按实际结果向用户说明。")
		}
		return call("delegate-music", "local__delegate_task", `{"worker":"worker","task":"将当前指定片段升高一个半音"}`)
	}
	m.step++
	switch m.step {
	case 1:
		found := map[string]bool{}
		for _, tool := range tools {
			found[tool.Function.Name] = true
			if tool.Function.Name == "project__query" && (!strings.Contains(tool.Function.Description, m.ids.Project) || !strings.Contains(tool.Function.Description, m.ids.C1)) {
				return model.ChatResponse{}, fmt.Errorf("worker did not receive frozen project/clip handles")
			}
		}
		if !found["project__query"] || !found[m.tool] {
			return model.ChatResponse{}, fmt.Errorf("worker missing native music tools: %v", found)
		}
		return call("query-music", "project__query", fmt.Sprintf(`{"projectId":%q}`, m.ids.Project))
	case 2:
		var query struct {
			Revision int64         `json:"revision"`
			State    project.State `json:"state"`
		}
		if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &query); err != nil {
			return model.ChatResponse{}, err
		}
		if len(query.State.Tracks) != 1 || len(query.State.Tracks[0].Clips) != 1 || query.State.Tracks[0].Clips[0].ID != m.ids.C1 {
			return model.ChatResponse{}, fmt.Errorf("worker query escaped frozen clip: %+v", query.State)
		}
		if m.tool == "project__transform" {
			return call("write-music", m.tool, transformPayload(m.ids.Project, query.Revision, "worker-write", fmt.Sprintf(`{"clipIds":[%q]}`, m.ids.C1), `{"transpose":{"semitones":1}}`, false))
		}
		note := query.State.Tracks[0].Clips[0].Notes[0]
		clipID := m.ids.C1
		if m.outside {
			clipID = m.ids.C2
		}
		return call("write-music", m.tool, editPayload(m.ids.Project, query.Revision, "worker-write", fmt.Sprintf(`{"kind":"note.update","clipId":%q,"noteDeltas":[{"id":%q,"pitch":61}]}`, clipID, note.ID)))
	case 3:
		result := messages[len(messages)-1].Content
		if m.denial != "" {
			if !strings.Contains(result, m.denial) {
				return model.ChatResponse{}, fmt.Errorf("worker did not receive rejection %q: %s", m.denial, result)
			}
		} else {
			var committed project.Result
			if err := json.Unmarshal([]byte(result), &committed); err != nil || committed.OpID != "worker-write" {
				return model.ChatResponse{}, fmt.Errorf("worker did not receive committed transaction: %s (%v)", result, err)
			}
		}
		return answer("仅报告实际工具结果，不另行重放。")
	}
	return model.ChatResponse{}, fmt.Errorf("unexpected worker turn %d", m.step)
}

func TestProjectWorkerEngineEditsAndRefusals(t *testing.T) {
	for _, scenario := range []struct {
		name, tool, denial string
	}{
		{"edit", "project__edit", ""},
		{"transform", "project__transform", ""},
		{"user-conflict", "project__transform", "工程已被其他操作修改"},
		{"approval-denied", "project__edit", "用户拒绝"},
		{"outside-scope", "project__edit", "超出任务范围"},
		{"cancelled", "project__transform", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, ids, fixture := scopeFixture(t, nil)
			svc := fixture.svc
			chat := &projectWorkerModel{ids: ids, tool: scenario.tool, denial: scenario.denial, outside: scenario.name == "outside-scope"}
			svc.chat, svc.tools = chat, emptyTools{}
			data := storage.DefaultRuntimeConfig()
			data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 2}
			// A delegation-only main must not narrow the worker's separately
			// frozen common capability selection.
			data.Execution.Main = storage.ComponentConfig{RestrictTools: true, AllowedTools: []string{"local__delegate_task"}}
			data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker", Configuration: &storage.WorkerConfiguration{
				Name: "worker", ContextBudget: 8192,
				Model: config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "music-worker"},
			}}}
			if _, err := svc.configureDefault(data); err != nil {
				t.Fatal(err)
			}
			parent, err := svc.store.GetSession(ctx, fixture.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			setContextPolicy(t, svc, parent.ContextID, func(policy *storage.ContextPolicy) { policy.ApprovalMode = "semi" })
			run, err := svc.StartConversationTask(parent.ID, "请修改这一片段", &ProjectScopeSelection{Clips: []string{ids.C1}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = svc.CancelTask(run.ID); svc.WaitIdle() }()
			delegate := waitForCallState(t, svc, parent.ID, storage.CallPending)
			if delegate.ToolName != "local__delegate_task" {
				t.Fatalf("unexpected parent approval: %+v", delegate)
			}
			if _, err := svc.ApproveToolCall(delegate.ID); err != nil {
				t.Fatal(err)
			}
			var child storage.Run
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				children, err := svc.store.ListChildRuns(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(children) > 0 {
					child = children[0]
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if child.ID == "" {
				t.Fatal("delegation did not create worker")
			}
			pending := waitForProjectWrite(t, svc, child.SessionID, scenario.tool)
			var previewHash string
			if scenario.name == "edit" || scenario.name == "transform" {
				view, err := svc.PreviewProjectEdit(pending.ID)
				if err != nil || view.Diff.Updated != 1 || view.Diff.Changes[0].Before.Pitch != 60 || view.Diff.Changes[0].After.Pitch != 61 {
					t.Fatalf("worker preview: %+v %v", view, err)
				}
				previewHash = view.ArgumentsHash
			}
			calls, err := svc.store.ListRunToolCalls(ctx, child.ID)
			if err != nil {
				t.Fatal(err)
			}
			querySucceeded := false
			for _, call := range calls {
				if call.ToolName == "project__query" && call.State == storage.CallSucceeded && call.ApprovalSource == storage.ApprovalAuto {
					querySucceeded = true
				}
			}
			if !querySucceeded {
				t.Fatalf("native query did not auto-execute: %+v", calls)
			}
			childSession, err := svc.store.GetSession(ctx, child.SessionID)
			if err != nil || childSession.ProjectID != "" {
				t.Fatalf("gateway should not bind hidden session: %+v %v", childSession, err)
			}
			before, err := svc.GetProject(ids.Project)
			if err != nil {
				t.Fatal(err)
			}
			// Rebinding the parent while its worker waits for approval must
			// neither retarget the edit nor revoke the original frozen scope.
			other, err := svc.CreateProject("另一个工程", 480)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.SetConversationProject(parent.ID, other.ID); err != nil {
				t.Fatal(err)
			}
			wantRevision, wantPitch := before.Revision, 60
			switch scenario.name {
			case "cancelled":
				if err := svc.CancelTask(run.ID); err != nil {
					t.Fatal(err)
				}
			case "approval-denied":
				if _, err := svc.DenyToolCall(pending.ID, "不要修改这段"); err != nil {
					t.Fatal(err)
				}
			default:
				if scenario.name == "user-conflict" {
					noteID := before.State.Tracks[0].Clips[0].Notes[0].ID
					pitch := 65
					if _, err := svc.SubmitProjectOps(ids.Project, "user-newer", "gui", before.Revision, []project.Op{{Kind: "note.update", ClipID: ids.C1, NoteDeltas: []project.NoteDelta{{ID: noteID, Pitch: &pitch}}}}); err != nil {
						t.Fatal(err)
					}
					wantPitch = 65
					wantRevision++
				} else if scenario.denial == "" {
					wantPitch = 61
					wantRevision++
				}
				approve := func() (storage.ToolCall, error) {
					if previewHash != "" {
						return svc.ApproveProjectEditPreview(pending.ID, previewHash)
					}
					return svc.ApproveToolCall(pending.ID)
				}
				if _, err := approve(); err != nil {
					t.Fatal(err)
				}
			}
			wantStatus := "completed"
			if scenario.name == "cancelled" {
				wantStatus = "cancelled"
			}
			waitForRun(t, svc, run.ID, wantStatus)
			after, err := svc.GetProject(ids.Project)
			if err != nil || after.Revision != wantRevision || after.State.Tracks[0].Clips[0].Notes[0].Pitch != wantPitch {
				t.Fatalf("unexpected project outcome: %+v %v", after, err)
			}
			if outcome, found, err := svc.GetProjectTransaction(ids.Project, "worker-write"); err != nil || found != (scenario.name == "edit" || scenario.name == "transform") || (found && outcome.Source != "agent:"+child.ID) {
				t.Fatalf("worker durable result: %+v %v %v", outcome, found, err)
			}
			if scenario.name == "edit" || scenario.name == "transform" {
				if _, err := svc.UndoProject(ids.Project, "gui-undo-worker", "gui", after.Revision); err != nil {
					t.Fatal(err)
				}
				undone, err := svc.GetProject(ids.Project)
				if err != nil || undone.State.Tracks[0].Clips[0].Notes[0].Pitch != 60 {
					t.Fatalf("GUI undo failed to reverse worker: %+v %v", undone, err)
				}
			}
			untouched, err := svc.GetProject(other.ID)
			if err != nil || untouched.Revision != 0 {
				t.Fatalf("worker retargeted rebound project: %+v %v", untouched, err)
			}
		})
	}
}

// A recorded call briefly starts as pending before its automatic gate decision.
// Wait for the specific write; a query that actually requires user approval
// will block the model and make this fail rather than being auto-approved here.
func waitForProjectWrite(t *testing.T, svc *Service, sessionID, name string) storage.ToolCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := svc.ListToolCalls(sessionID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if call.ToolName == name && call.State == storage.CallPending {
				return call
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("worker did not reach pending write %s", name)
	return storage.ToolCall{}
}

func TestProjectGatewayRejectsMissingAndMismatchedRuns(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	for _, runID := range []string{"", "missing-run"} {
		tools.runID = runID
		if _, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, ids.Project)); err == nil {
			t.Fatal("missing run fell back to binding")
		}
	}
	other, err := tools.svc.CreateConversation(storage.DefaultConfigID, "", "另一个对话")
	if err != nil {
		t.Fatal(err)
	}
	tools.runID = projectToolRun(t, tools.svc.store, other.ID)
	if _, err := tools.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, ids.Project)); err == nil {
		t.Fatal("foreign run fell back to session binding")
	}
}

func TestProjectReadonlyWorkerDiscoveryAndGateway(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, func(ids scopeIDs) string {
		return fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"read","clips":[%q]}`, ids.Project, ids.C1)
	})
	tools.delegated = true
	discovery := newDiscoveryTools(tools)
	defs, err := discovery.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range defs {
		found[tool.Function.Name] = true
	}
	if !found["project__query"] || !found["project__select"] || found["project__edit"] || found["project__transform"] {
		t.Fatalf("read-only catalog: %v", found)
	}
	if _, err := discovery.Call(ctx, "project__query", fmt.Sprintf(`{"projectId":%q}`, ids.Project)); err != nil {
		t.Fatal(err)
	}
	gate := toolGate{svc: tools.svc, contextID: tools.profile.ContextID, snapshot: &tools.profile, runID: tools.runID}
	for _, name := range []string{"project__edit", "project__transform"} {
		if decision := gate.Gate(ctx, agent.ToolRequest{Tool: name, Arguments: `{}`}); decision.Deny == "" {
			t.Fatalf("read-only write reached approval instead of denial: %+v", decision)
		}
		if _, err := tools.Call(ctx, name, `{}`); err == nil {
			t.Fatal("read-only gateway accepted a write")
		}
	}
}

func TestProjectGatewayConcurrentRootsRejectStaleEdits(t *testing.T) {
	ctx, ids, first := scopeFixture(t, nil)
	svc := first.svc
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "并发工程对话")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(c.ID, ids.Project); err != nil {
		t.Fatal(err)
	}
	second := first
	second.sessionID = c.ID
	second.runID = projectToolRun(t, svc.store, c.ID)
	before, err := svc.GetProject(ids.Project)
	if err != nil {
		t.Fatal(err)
	}
	noteID := before.State.Tracks[0].Clips[0].Notes[0].ID
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, tools := range []bookkeepingTools{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := tools.Call(ctx, "project__edit", editPayload(ids.Project, before.Revision, fmt.Sprintf("concurrent-%d", i), fmt.Sprintf(`{"kind":"note.update","clipId":%q,"noteDeltas":[{"id":%q,"pitch":%d}]}`, ids.C1, noteID, 61+i)))
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !strings.Contains(err.Error(), "工程已被其他操作修改") {
			t.Fatalf("unexpected concurrency rejection: %v", err)
		}
	}
	after, err := svc.GetProject(ids.Project)
	if err != nil || succeeded != 1 || after.Revision != before.Revision+1 {
		t.Fatalf("concurrent roots overwrote each other: successes=%d revision=%d err=%v", succeeded, after.Revision, err)
	}
}

func TestProjectGateRechecksLivePolicy(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	gate := toolGate{svc: tools.svc, contextID: tools.profile.ContextID, snapshot: &tools.profile, runID: tools.runID}
	req := agent.ToolRequest{CallID: "read-native", Tool: "project__query", Arguments: fmt.Sprintf(`{"projectId":%q}`, ids.Project)}
	if decision := gate.Gate(ctx, req); decision.Deny != "" || decision.Confirm != "" {
		t.Fatalf("scoped query did not use native read classification: %+v", decision)
	}
	_, calls, err := tools.svc.store.AddAssistantMessage(ctx, tools.sessionID, tools.runID, "", []storage.MessageToolCall{{ID: req.CallID, Name: req.Tool, Arguments: req.Arguments}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tools.svc.store.ApproveToolCall(ctx, calls[0].ID, storage.ApprovalAuto); err != nil {
		t.Fatal(err)
	}
	setContextPolicy(t, tools.svc, tools.profile.ContextID, func(p *storage.ContextPolicy) { p.ApprovalMode = "manual" })
	if decision := gate.Recheck(ctx, req); decision.Deny == "" {
		t.Fatalf("native query bypassed newly manual policy: %+v", decision)
	}
}
