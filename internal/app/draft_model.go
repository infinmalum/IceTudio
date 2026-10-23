package app

import (
	"context"
	"errors"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

func (s *Service) callDraftModel(ctx context.Context, run storage.Run, p storage.RunConfigSnapshot, payload []byte, contract string) (string, error) {
	if len(payload) > 512*1024 {
		return "", errors.New("草案材料超过512KiB，未发送")
	}
	scopes, err := s.sessionScopes(ctx, run.SessionID, p)
	if err != nil {
		return "", err
	}
	scopes = append(scopes, storage.ScopePersonal, storage.ScopeWorkspace)
	if err = s.store.AccumulateContextScopes(ctx, run.SessionID, scopes); err != nil {
		return "", err
	}
	messages := []model.Message{{Role: "system", Content: p.Persona + "\n" + p.SystemPrompt + "\n" + contract}, {Role: "user", Content: string(payload)}}
	recorder := runRecorder{svc: s, runID: run.ID, sessionID: run.SessionID, classifier: s.classifierFor(p)}
	step, err := recorder.ModelRequest(ctx, p.Model, messages, nil)
	if err != nil {
		return "", err
	}
	answer, chatErr := s.componentChat(run.ID, run.SessionID, p, p.Model).Chat(ctx, p.Model, messages, nil)
	if err = recorder.ModelDone(context.WithoutCancel(ctx), step, chatErr); err != nil {
		return "", err
	}
	if chatErr != nil {
		return "", chatErr
	}
	if len(answer.Message.ToolCalls) > 0 {
		return "", errors.New("草案组件不允许工具调用")
	}
	if len(answer.Message.Content) > 512*1024 {
		return "", errors.New("草案输出超过512KiB")
	}
	return answer.Message.Content, nil
}
