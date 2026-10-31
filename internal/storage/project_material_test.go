package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func materialCall(t *testing.T, s *Store, c Conversation, r Run, id, name, args, approval string) ToolCall {
	t.Helper()
	ctx := context.Background()
	_, calls, err := s.AddAssistantMessage(ctx, c.ID, r.ID, "", []MessageToolCall{{ID: id, Name: name, Arguments: args}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveToolCall(ctx, calls[0].ID, approval); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginToolCall(ctx, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	return calls[0]
}

func TestMaterialSealAndOneUseConcurrentDispatch(t *testing.T) {
	s, c, run := searchRoot(t, 15)
	ctx := context.Background()
	prepare := materialCall(t, s, c, run, "prep", "project__prepare_material", `{}`, ApprovalAuto)
	if err := s.SealPreparedMaterial(ctx, prepare.ID, `{"snapshot":"original"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishToolCall(ctx, prepare.ID, CallSucceeded, `{"snapshot":"original"}`, "", ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreparedProjectMaterial(ctx, "other-run", prepare.ID); err == nil {
		t.Fatal("cross-run reference accepted")
	}
	args := fmt.Sprintf(`{"preparationId":%q}`, prepare.ID)
	first := materialCall(t, s, c, run, "send1", "local__send_material", args, ApprovalUser)
	second := materialCall(t, s, c, run, "send2", "local__send_material", args, ApprovalUser)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, call := range []ToolCall{first, second} {
		wg.Add(1)
		go func(call ToolCall) {
			defer wg.Done()
			results <- s.ClaimMaterialDispatch(ctx, run.ID, call.ID, prepare.ID)
		}(call)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent dispatch winners=%d", wins)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, call := range []ToolCall{first, second} {
		if err := s.ClaimMaterialDispatch(ctx, run.ID, call.ID, prepare.ID); err == nil {
			t.Fatal("replayed after SQLite reopen")
		}
	}
	if err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	for _, call := range []ToolCall{first, second} {
		if err := s.ClaimMaterialDispatch(ctx, run.ID, call.ID, prepare.ID); err == nil {
			t.Fatal("replayed after recovery")
		}
	}
	// The seal is independent of a user-written success result.
	if _, err := s.db.Exec(`UPDATE tool_calls SET result='{"snapshot":"forged"}' WHERE id=?`, prepare.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreparedProjectMaterial(ctx, run.ID, prepare.ID); err == nil {
		t.Fatal("tampered result accepted")
	}
}

func TestMaterialDispatchRejectsUnsealedOrUnapproved(t *testing.T) {
	for _, scenario := range []string{"unsealed", "automatic", "arguments_changed", "wrong_tool"} {
		t.Run(scenario, func(t *testing.T) {
			s, c, run := searchRoot(t, 10)
			ctx := context.Background()
			prep := materialCall(t, s, c, run, "prep", "project__prepare_material", `{}`, ApprovalAuto)
			if scenario != "unsealed" {
				if err := s.SealPreparedMaterial(ctx, prep.ID, `{}`); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.FinishToolCall(ctx, prep.ID, CallSucceeded, `{}`, "", ScopeWorkspace); err != nil {
				t.Fatal(err)
			}
			approval := ApprovalUser
			if scenario == "automatic" {
				approval = ApprovalAuto
			}
			name := "local__send_material"
			if scenario == "wrong_tool" {
				name = "other__send"
			}
			args := fmt.Sprintf(`{"preparationId":%q}`, prep.ID)
			call := materialCall(t, s, c, run, "send", name, args, approval)
			if scenario == "arguments_changed" {
				if _, err := s.db.Exec(`UPDATE tool_calls SET arguments=arguments||' ' WHERE id=?`, call.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ClaimMaterialDispatch(ctx, run.ID, call.ID, prep.ID); err == nil {
				t.Fatal("invalid dispatch accepted")
			}
		})
	}
}
