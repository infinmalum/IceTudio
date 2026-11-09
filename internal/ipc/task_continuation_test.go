package ipc

import (
	"context"
	"strings"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type continuationChat struct{}

func (continuationChat) Chat(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool) (model.ChatResponse, error) {
	content := messages[len(messages)-1].Content
	if strings.Contains(content, "宿主已完成任务调度") {
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "汇总"}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: `{"status":"succeeded","content":"交付","evidenceCallIds":[]}`}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestContinueFixedTaskOverIPC(t *testing.T) {
	s, _ := newTestHost(t, continuationChat{}, &blockingTools{})
	client := mustConnect(t, startServer(t, s))
	p, err := s.GetRuntimeConfig(storage.DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.Workflow = "fixed"
	p.Data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker"}}
	p.Data.Delegation = storage.DelegationPolicy{Enabled: true, MaxDepth: 1, MaxConcurrency: 1}
	p.Data.Execution.Stages = []storage.TaskSpec{{ID: "analyze", Worker: "worker", Task: "分析", AccessMode: "read", InputPrompt: "请提供材料"}}
	if _, err = s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(p.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateContextPolicy(c.ContextID, storage.ContextPolicy{ApprovalMode: "automatic"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "整理", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := s.GetRun(r.ID)
		if current.Status == "waiting" {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if err := client.Call("tasks.continue", map[string]any{"runId": r.ID, "taskId": "wrong", "input": "材料"}, nil); err == nil {
		t.Fatal("invalid stage accepted")
	}
	// Wait for root goroutine cleanup after its terminal record is visible.
	time.Sleep(20 * time.Millisecond)
	if err := client.Call("tasks.continue", map[string]any{"runId": r.ID, "taskId": "analyze", "input": "用户补充材料"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := s.ListToolCalls(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if call.State == storage.CallPending {
				if err := client.Call("calls.approve", map[string]any{"callId": call.ID}, nil); err != nil {
					t.Fatal(err)
				}
			}
		}
		current, _ := s.GetRun(r.ID)
		if current.Status != "running" {
			if current.Status != "completed" {
				t.Fatal(current)
			}
			g, err := s.GetTaskGraph(r.ID)
			if err != nil || len(g.Attempts) != 1 || g.Tasks[0].Input != "用户补充材料" {
				t.Fatal(g, err)
			}
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("continued run timed out")
}
