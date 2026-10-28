package app

import (
	"context"
	"encoding/json"
	"fmt"
	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"icetudio/internal/testfixture"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type basicMock struct {
	file, dir string
	turn      int
}

func (m *basicMock) Chat(_ context.Context, _ config.Model, _ []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	m.turn++
	call := model.ToolCall{ID: fmt.Sprintf("basic%d", m.turn), Type: "function"}
	var args any
	switch m.turn {
	case 1:
		call.Function.Name = "local__write_file"
		args = map[string]any{"path": m.file, "content": "first"}
	case 2:
		call.Function.Name = "local__edit_file"
		args = map[string]any{"path": m.file, "oldText": "first", "newText": "second"}
	case 3:
		call.Function.Name = "local__exec"
		args = map[string]any{"executable": "/bin/sh", "args": []string{"-c", "printf checked"}, "cwd": m.dir, "timeoutSeconds": 5}
	default:
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "文件修改与命令检查完成"}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	raw, _ := json.Marshal(args)
	call.Function.Arguments = string(raw)
	return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestBasicToolsThroughApprovalAndDurableRecords(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "result.txt")
	s, _ := newTestService(t, &basicMock{file: file, dir: dir}, &scriptTools{})
	c, err := s.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	policy := storage.ContextPolicy{ApprovalMode: "semi", Permissions: storage.PermissionScope{WorkspacePaths: []string{dir}, Commands: []string{"/bin/sh"}}}
	if _, err := s.UpdateContextPolicy(c.ContextID, policy); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "修改文件并运行检查", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		calls, err := s.ListToolCalls(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if call.State == storage.CallPending {
				_, _ = s.ApproveToolCall(call.ID)
			}
		}
		run, err := s.GetRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" {
			if run.Status != "completed" || len(calls) != 3 {
				t.Fatal(run, calls)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "second" {
				t.Fatal(string(data), err)
			}
			for _, call := range calls {
				if call.State != storage.CallSucceeded || call.ApprovalSource != storage.ApprovalUser {
					t.Fatal(call)
				}
			}
			if !strings.Contains(calls[2].Result, `"passed":true`) || !strings.Contains(calls[2].Result, "checked") {
				t.Fatal(calls[2])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("basic execution did not finish")
}
func TestBasicGateRejectsOutOfScopeAndRevokedCommands(t *testing.T) {
	s, store := newTestService(t, &scriptModel{}, &scriptTools{})
	ctx := context.Background()
	p, err := testfixture.RunConfig(store, ctx, testfixture.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p.Policy = storage.ContextPolicy{ApprovalMode: "semi", Permissions: storage.PermissionScope{WorkspacePaths: []string{dir}, Commands: []string{"/bin/sh"}}}
	if _, err = s.UpdateContextPolicy(p.ContextID, p.Policy); err != nil {
		t.Fatal(err)
	}
	gate := toolGate{svc: s, contextID: p.ContextID, snapshot: &p}
	for _, name := range []string{"local__write_file", "local__edit_file"} {
		args := map[string]string{"path": filepath.Join(t.TempDir(), "denied.txt")}
		if name == "local__write_file" {
			args["content"] = "x"
		} else {
			args["oldText"] = "old"
			args["newText"] = "new"
		}
		raw, _ := json.Marshal(args)
		if decision := gate.Gate(ctx, agent.ToolRequest{Tool: name, Arguments: string(raw)}); !strings.Contains(decision.Deny, "工作区") {
			t.Fatal("out of scope write offered")
		}
	}
	raw, _ := json.Marshal(map[string]any{"executable": "/bin/sh", "args": []string{"-c", "printf x"}, "cwd": dir})
	req := agent.ToolRequest{Tool: "local__exec", Arguments: string(raw)}
	if decision := gate.Gate(ctx, req); decision.Deny != "" || decision.Confirm == "" {
		t.Fatal(decision)
	}
	revoked := p.Policy
	revoked.Permissions.Commands = nil
	if _, err = s.UpdateContextPolicy(p.ContextID, revoked); err != nil {
		t.Fatal(err)
	}
	if gate.Recheck(ctx, req).Deny == "" {
		t.Fatal("revoked command still accepted")
	}
}
