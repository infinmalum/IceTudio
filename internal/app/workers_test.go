package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

type workerScript struct {
	calls int
	t     *testing.T
}

func (m *workerScript) Chat(_ context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	m.calls++
	switch m.calls {
	case 1:
		if source.Name != "test-model" {
			m.t.Errorf("main default changed: %s", source.Name)
		}
		found := false
		for _, tool := range tools {
			if tool.Function.Name == "local__delegate_task" && strings.Contains(string(tool.Function.Parameters), "writer") {
				found = true
			}
		}
		if !found {
			m.t.Error("worker choices not advertised")
		}
		call := delegateCall("delegate", "", "写作任务", "")
		call.Function.Arguments = `{"worker":"writer","task":"写作任务"}`
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}}, nil
	case 2:
		if source.Name != "old-worker" || !strings.Contains(messages[0].Content, "old prompt") || strings.Contains(messages[0].Content, "new prompt") {
			m.t.Errorf("worker configuration changed: %s %s", source.Name, messages[0].Content)
		}
		if !strings.Contains(messages[0].Content, "old description") || strings.Contains(messages[0].Content, "new description") {
			m.t.Error("Skill metadata changed")
		}
		call := model.ToolCall{ID: "skill", Type: "function"}
		call.Function.Name = "local__load_skill"
		call.Function.Arguments = `{"name":"note"}`
		return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{call}}}, nil
	case 3:
		last := messages[len(messages)-1].Content
		if !strings.Contains(last, "old body") || strings.Contains(last, "new body") {
			m.t.Errorf("Skill body changed: %s", last)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "worker completed"}}, nil
	default:
		if source.Name != "test-model" {
			m.t.Errorf("root model changed: %s", source.Name)
		}
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "main completed"}}, nil
	}
}

func TestWorkerPinsConfigurationAndSkill(t *testing.T) {
	script := &workerScript{t: t}
	svc, store := newTestService(t, script, &scriptTools{})
	root := t.TempDir()
	path := filepath.Join(root, "note", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: note\ndescription: old description\n---\nold body"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skills.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	svc.skills = catalog
	data := storage.DefaultRuntimeConfig()
	data.AllSkills = false
	data.EnabledSkills = []string{"note"}
	data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1}
	data.Execution.WorkerSources = []storage.WorkerSource{{Name: "writer", Configuration: &storage.WorkerConfiguration{
		Name: "writer", SystemPrompt: "old prompt",
		Model:             config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "old-worker"},
		EnabledMCPServers: []string{"local"}, AllSkills: true, ContextBudget: 8192,
	}}}
	parent, err := svc.configureDefault(data)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := svc.CreateConversation(parent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(conversation.ID, "write", nil)
	if err != nil {
		t.Fatal(err)
	}
	call := waitForCallState(t, svc, conversation.ID, storage.CallPending)
	// Editing the config, the Skill source and the global default cannot alter the frozen worker.
	current, err := svc.GetRuntimeConfig(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Data.Execution.WorkerSources[0].Configuration.SystemPrompt = "new prompt"
	current.Data.Execution.WorkerSources[0].Configuration.Model.Name = "new-worker"
	if _, err := svc.UpdateRuntimeConfig(current); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: note\ndescription: new description\n---\nnew body"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.config.Model.Name = "new-global"
	svc.mu.Unlock()
	if _, err := svc.ApproveToolCall(call.ID); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, svc, run.ID, "completed")
	children, err := store.ListChildRuns(context.Background(), run.ID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children: %+v %v", children, err)
	}
	child := children[0]
	if child.ContextID != conversation.ContextID || child.WorkerName != "writer" || child.ConfigRevision != parent.Revision {
		t.Fatalf("worker identity/snapshot: %+v", child)
	}
	resources, err := store.RunResources(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resources.Workers["writer"].Skills[0].SHA256 == "" {
		t.Fatal("missing Skill digest")
	}
	if script.calls != 4 {
		t.Fatal("unexpected model calls", script.calls)
	}
}

func delegateCall(id, worker, task, materials string) model.ToolCall {
	call := model.ToolCall{ID: id, Type: "function"}
	call.Function.Name = "local__delegate_task"
	raw, _ := json.Marshal(delegateArgs{Worker: worker, Task: task, Materials: materials})
	call.Function.Arguments = string(raw)
	return call
}
