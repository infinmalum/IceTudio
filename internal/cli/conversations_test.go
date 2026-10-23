package cli

import (
	"context"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type conversationModel struct{}

func (conversationModel) Chat(_ context.Context, _ config.Model, _ []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "模拟服务结果"}}, nil
}

func TestConversationAndTasksCLI(t *testing.T) {
	client, svc := newCLIHost(t, conversationModel{}, time.Second)
	if code := dispatch(client, []string{"conversations", "create"}); code != 0 {
		t.Fatal("conversation creation", code)
	}
	items, err := svc.ListConversations()
	if err != nil || len(items) != 1 {
		t.Fatalf("conversations: %+v %v", items, err)
	}
	c := items[0]
	if code := dispatch(client, []string{"conversations", "permissions", c.ID, "--mode", "manual", "--workspace", "/project", "--command", "/bin/sh"}); code != 0 {
		t.Fatal("permission update", code)
	}
	local, err := svc.GetLocalContext(c.ContextID)
	if err != nil || local.Policy.ApprovalMode != "manual" || local.Policy.Permissions.Unrestricted || len(local.Policy.Permissions.Commands) != 1 || local.Policy.Permissions.Commands[0] != "/bin/sh" {
		t.Fatal("permissions not applied", err)
	}
	if code := dispatch(client, []string{"conversations", "start", c.ID, "hello", "--wait"}); code != 0 {
		t.Fatal("simulated task", code)
	}
	history, err := svc.ListMessages(c.ID)
	if err != nil || len(history) != 2 || history[1].Content != "模拟服务结果" {
		t.Fatalf("missing model response: %+v %v", history, err)
	}
	runs, err := svc.ListRuns(c.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	var graph storage.TaskGraph
	if err := client.Call("tasks.graph", map[string]any{"runId": runs[0].ID}, &graph); err != nil || graph.Goal != "hello" || graph.RunStatus != "completed" {
		t.Fatalf("IPC root graph: %+v %v", graph, err)
	}
	if err := client.Call("tasks.graph", map[string]any{"runId": "missing"}, &graph); err == nil {
		t.Fatal("missing graph accepted")
	}
	if code := dispatch(client, []string{"tasks", "status", runs[0].ID}); code != 0 {
		t.Fatal("CLI task graph status", code)
	}
	if code := dispatch(client, []string{"conversations", "show", c.ID}); code != 0 {
		t.Fatal("conversation history", code)
	}
}
