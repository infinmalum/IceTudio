package app

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/materials"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type continuationMock struct {
	mu           sync.Mutex
	calls        map[string]int
	missingInput bool
}

func (m *continuationMock) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	content := messages[len(messages)-1].Content
	if source.Name == "planner-model" {
		panic("planner in fixed flow")
	}
	if source.Name == "review-model" {
		return (&reviewMock{status: "passed"}).Chat(ctx, source, messages, tools)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.Contains(content, "宿主已完成任务调度") {
		m.calls["summary"]++
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "汇总结果"}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	task := strings.Split(content, "\n")[0]
	m.calls[task]++
	if task == "Analyze B" && (!strings.Contains(content, "补充材料 B") || len(messages[len(messages)-1].Files) != 1 || messages[len(messages)-1].Files[0].Text != "附件正文") {
		m.missingInput = true
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: dynamicOutcome(task + " result")}, Usage: model.Usage{TotalTokens: 10}}, nil
}

func continueWhenSettled(t *testing.T, s *Service, r storage.Run, stage, input string, files ...materials.Upload) storage.Run {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.taskMu.Lock()
		active := s.running[r.SessionID]
		s.taskMu.Unlock()
		if active == "" {
			out, err := s.ContinueConversationTask(r.ID, stage, input, files...)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("task did not settle")
	return storage.Run{}
}

func TestFixedWorkflowWaitAnswerContinueOriginalWithoutRepeatingStages(t *testing.T) {
	s, c, p := fixedFixture(t, &dynamicMock{})
	m := &continuationMock{calls: map[string]int{}}
	s.chat = m
	p.Data.Execution.Stages[1].DependsOn = []string{"a"}
	p.Data.Execution.Stages[1].InputPrompt = "请提供 B"
	p.Data.Execution.Review.Mode = "required"
	p.Data.Execution.Reviewer.Model = &config.Model{Provider: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Name: "review-model"}
	var err error
	p, err = s.UpdateRuntimeConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "用户特定目标")
	if err != nil {
		t.Fatal(err)
	}
	final := approveDynamicUntilDone(t, s, c, r)
	if final.Status != "waiting" {
		t.Fatalf("not waiting %+v", final)
	}
	before, err := s.GetTaskGraph(r.ID)
	if err != nil || len(before.Attempts) != 1 || before.Tasks[0].State != "succeeded" || before.Tasks[1].State != "pending" {
		t.Fatal("lost progress", before, err)
	}
	// Mutating both the source and provider defaults cannot alter resumed work.
	p.Data.Execution.Stages[1].Task = "incorrect changed task"
	if _, err = s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.config.Model.Name = "changed-default"
	s.mu.Unlock()
	resumed := continueWhenSettled(t, s, r, "b", "补充材料 B", materials.Upload{Name: "b.txt", Data: base64.StdEncoding.EncodeToString([]byte("附件正文"))})
	if resumed.ID != r.ID {
		t.Fatal("created a new root")
	}
	final = approveDynamicUntilDone(t, s, c, resumed)
	if final.Status != "completed" {
		t.Fatalf("not complete %+v", final)
	}
	g, err := s.GetTaskGraph(r.ID)
	if err != nil || len(g.Continuations) != 1 || g.Continuations[0].PreviousStatus != "waiting" || len(g.Attempts) != 3 || len(g.Artifacts) != 3 || g.Budget.ModelCalls != 5 || g.Coordination.Rounds != 0 || g.Goal != "用户特定目标" {
		t.Fatal("wrong retained state", g, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls["Analyze A"] != 1 || m.calls["Analyze B"] != 1 || m.calls["Combine"] != 1 || m.calls["summary"] != 1 || m.missingInput {
		t.Fatal("replayed or lost material", m.calls, m.missingInput)
	}
	snapshot, err := s.store.ReviewSnapshot(context.Background(), r.ID)
	if err != nil || len(snapshot.Inputs) != 1 || snapshot.Inputs[0].Content != "补充材料 B" || len(snapshot.Attachments) != 1 {
		t.Fatal("review missing input", snapshot, err)
	}
}

func TestFixedWorkflowTwoInputGatesKeepSuccessfulStages(t *testing.T) {
	s, c, p := fixedFixture(t, &dynamicMock{})
	m := &continuationMock{calls: map[string]int{}}
	s.chat = m
	p.Data.Execution.Stages[0].InputPrompt = "补充 A"
	p.Data.Execution.Stages[1].InputPrompt = "补充 B"
	p.Data.Execution.Stages[1].DependsOn = []string{"a"}
	if _, err := s.UpdateRuntimeConfig(p); err != nil {
		t.Fatal(err)
	}
	r, err := s.StartConversationTask(c.ID, "用户特定目标")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"a", "b"} {
		final := approveDynamicUntilDone(t, s, c, r)
		if final.Status != "waiting" {
			t.Fatal(final)
		}
		g, _ := s.GetTaskGraph(r.ID)
		if g.WaitingTaskID != stage {
			t.Fatal("wrong gate", g)
		}
		r = continueWhenSettled(t, s, r, stage, "answer "+stage)
	}
	final := approveDynamicUntilDone(t, s, c, r)
	if final.Status != "completed" {
		t.Fatal(final)
	}
	g, _ := s.GetTaskGraph(r.ID)
	if len(g.Attempts) != 3 || g.Budget.ModelCalls != 4 {
		t.Fatal("repeat execution", g)
	}
}
