package ipc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

type retrospectiveChat struct{}

func (retrospectiveChat) Chat(_ context.Context, _ config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	if strings.Contains(messages[0].Content, "你是任务复盘者") {
		var snap storage.RetrospectiveSnapshot
		if err := json.Unmarshal([]byte(messages[1].Content), &snap); err != nil {
			return model.ChatResponse{}, err
		}
		raw, _ := json.Marshal(storage.RetrospectiveResult{Summary: "IPC 复盘已完成", Observations: []storage.RetrospectiveObservation{{Reference: "run:" + snap.Graph.RootRunID, Description: "仅分析过程"}}, Recommendations: []string{"建议增加案例"}})
		return model.ChatResponse{Message: model.Message{Role: "assistant", Content: string(raw)}, Usage: model.Usage{TotalTokens: 10}}, nil
	}
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "原结果"}, Usage: model.Usage{TotalTokens: 10}}, nil
}
func TestRetrospectiveOverIPC(t *testing.T) {
	svc, _ := newTestHost(t, retrospectiveChat{}, &blockingTools{})
	client := mustConnect(t, startServer(t, svc))
	c, err := svc.CreateConversation(storage.DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	original, err := svc.StartConversationTask(c.ID, "原目标", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.WaitIdle()
	if err := client.Call("tasks.retrospect", map[string]any{"sessionId": c.ID, "runId": original.ID}, nil); err != nil {
		t.Fatal(err)
	}
	svc.WaitIdle()
	runs, err := svc.ListRuns(c.ID)
	if err != nil || len(runs) != 2 {
		t.Fatal(runs, err)
	}
	reports, err := svc.ListTaskRetrospectives(original.ID)
	if err != nil || len(reports) != 1 {
		t.Fatal(reports, err)
	}
	var remote []storage.TaskRetrospective
	if err = client.Call("tasks.retrospectives", map[string]any{"runId": runs[0].ID}, &remote); err != nil || len(remote) != 1 {
		t.Fatal(remote, err)
	}
	var run storage.Run
	if err := client.Call("runs.get", map[string]any{"runId": runs[0].ID}, &run); err != nil || run.ID != runs[0].ID {
		t.Fatal(run, err)
	}
	if err = client.Call("tasks.retrospect", map[string]any{"sessionId": c.ID, "runId": original.ID, "agentId": "obsolete"}, nil); err == nil {
		t.Fatal("obsolete IPC parameter accepted")
	}
}
