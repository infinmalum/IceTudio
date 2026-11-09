package ipc

import (
	"context"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type conversationModel struct{}

func (conversationModel) Chat(_ context.Context, _ config.Model, _ []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "模拟服务结果"}}, nil
}

func TestConversationAndTasksOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, conversationModel{}, &blockingTools{})
	client := mustConnect(t, startServer(t, svc))
	if err := client.Call("conversations.create", nil, nil); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListConversations()
	if err != nil || len(items) != 1 {
		t.Fatalf("conversations: %+v %v", items, err)
	}
	c := items[0]
	policy := storage.ContextPolicy{ApprovalMode: "manual", Permissions: storage.PermissionScope{WorkspacePaths: []string{"/project"}, Commands: []string{"/bin/sh"}}}
	if err := client.Call("contexts.update", map[string]any{"id": c.ContextID, "policy": policy}, nil); err != nil {
		t.Fatal(err)
	}
	local, err := svc.GetLocalContext(c.ContextID)
	if err != nil || local.Policy.ApprovalMode != "manual" || local.Policy.Permissions.Unrestricted || len(local.Policy.Permissions.Commands) != 1 || local.Policy.Permissions.Commands[0] != "/bin/sh" {
		t.Fatal("permissions not applied", err)
	}
	if err := client.Call("conversations.start", map[string]any{"id": c.ID, "input": "hello"}, nil); err != nil {
		t.Fatal(err)
	}
	svc.WaitIdle()
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
}

func TestConversationSearchOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, conversationModel{}, &blockingTools{})
	client := mustConnect(t, startServer(t, svc))
	var conversation storage.Conversation
	if err := client.Call("conversations.create", map[string]any{}, &conversation); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("conversations.options", map[string]any{"id": conversation.ID, "options": storage.ConversationOptions{WebSearch: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("conversations.get", map[string]any{"id": conversation.ID}, &conversation); err != nil || !conversation.Options.WebSearch {
		t.Fatal(conversation, err)
	}
	var run storage.Run
	if err := client.Call("conversations.start", map[string]any{"id": conversation.ID, "input": "search"}, &run); err == nil {
		t.Fatal("unavailable search started a task")
	}
	if err := client.Call("conversations.searchRecords", map[string]any{"id": conversation.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("conversations.options", map[string]any{"id": conversation.ID, "options": storage.ConversationOptions{}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("conversations.get", map[string]any{"id": conversation.ID}, &conversation); err != nil || conversation.Options.WebSearch {
		t.Fatal(conversation, err)
	}
}
