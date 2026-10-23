package app

import (
	"context"
	"errors"

	"icetudio/internal/materials"
	"icetudio/internal/storage"
)

// ContinueConversationTask explicitly resumes the original fixed run; 执行配置,
// model, tools, grants, Skills and search settings remain its frozen resources.
func (s *Service) ContinueConversationTask(runID, taskID, input string, uploads ...materials.Upload) (storage.Run, error) {
	if err := s.checkReady(); err != nil {
		return storage.Run{}, err
	}
	if s.runs == nil || s.tools == nil || s.chat == nil {
		return storage.Run{}, errors.New("运行时尚未初始化")
	}
	files, err := materials.Prepare(uploads)
	if err != nil {
		return storage.Run{}, err
	}
	run, err := s.GetRun(runID)
	if err != nil {
		return storage.Run{}, err
	}
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.running[run.SessionID] != "" {
		return storage.Run{}, errors.New("该会话执行尚未结束，请稍后继续")
	}
	if unresolved, err := s.store.HasUnresolvedToolCalls(context.Background(), run.SessionID); err != nil {
		return storage.Run{}, err
	} else if unresolved {
		return storage.Run{}, errors.New("请先处置会话中结果未知的工具调用")
	}
	runCtx, p, err := s.runs.ContinueFixed(context.Background(), runID, taskID, input, files)
	if err != nil {
		return storage.Run{}, err
	}
	s.running[run.SessionID] = runID
	run.Status = "running"
	run.Error = ""
	s.publish(Event{Type: "run.started", SessionID: run.SessionID, RunID: runID, Detail: "继续原固定流程"})
	go s.executeTask(runCtx, runID, run.SessionID, p.Model, p, storage.ExfilGrant{})
	return run, nil
}
