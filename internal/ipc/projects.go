package ipc

import (
	"context"
	"fmt"

	"icetudio/internal/project"
)

// dispatchProjects routes the projects.* IPC methods. They map one-to-one
// to the app service methods the desktop UI calls, so CLI edits and GUI
// edits receive identical validation, idempotency and conflict behavior.
func (s *Server) dispatchProjects(ctx context.Context, request Request) (any, error) {
	switch request.Method {
	case "projects.list":
		return s.svc.ListProjects()

	case "projects.get":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.GetProject(params.ID)

	case "projects.query":
		var params struct {
			ProjectID string `json:"projectId"`
			TrackID   string `json:"trackId"`
			ClipID    string `json:"clipId"`
			StartTick *int64 `json:"startTick"`
			EndTick   *int64 `json:"endTick"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.QueryProject(params.ProjectID, params.TrackID, params.ClipID, params.StartTick, params.EndTick)

	case "projects.create":
		var params struct {
			Title string `json:"title"`
			PPQ   int    `json:"ppq"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.CreateProject(params.Title, params.PPQ)

	case "projects.delete":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, s.svc.DeleteProject(params.ID)

	case "projects.edit":
		var params struct {
			ProjectID    string       `json:"projectId"`
			OpID         string       `json:"opId"`
			Source       string       `json:"source"`
			BaseRevision *int64       `json:"baseRevision"`
			Ops          []project.Op `json:"ops"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if params.BaseRevision == nil {
			return nil, fmt.Errorf("缺少 baseRevision")
		}
		return s.svc.SubmitProjectOps(params.ProjectID, params.OpID, params.Source, *params.BaseRevision, params.Ops)

	case "projects.undo":
		var params struct {
			ProjectID    string `json:"projectId"`
			OpID         string `json:"opId"`
			BaseRevision *int64 `json:"baseRevision"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if params.BaseRevision == nil {
			return nil, fmt.Errorf("缺少 baseRevision")
		}
		return s.svc.UndoProject(params.ProjectID, params.OpID, "cli", *params.BaseRevision)

	case "projects.redo":
		var params struct {
			ProjectID    string `json:"projectId"`
			OpID         string `json:"opId"`
			BaseRevision *int64 `json:"baseRevision"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if params.BaseRevision == nil {
			return nil, fmt.Errorf("缺少 baseRevision")
		}
		return s.svc.RedoProject(params.ProjectID, params.OpID, "cli", *params.BaseRevision)

	case "projects.transaction":
		var params struct {
			ProjectID string `json:"projectId"`
			OpID      string `json:"opId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		result, found, err := s.svc.GetProjectTransaction(params.ProjectID, params.OpID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"found": found, "result": result}, nil

	case "projects.history":
		var params struct {
			ProjectID string `json:"projectId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ProjectHistory(params.ProjectID)

	case "projects.import":
		var params struct {
			Path string `json:"path"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ImportProjectMIDI(params.Path)

	case "projects.export":
		var params struct {
			ProjectID string `json:"projectId"`
			Path      string `json:"path"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, s.svc.ExportProjectMIDI(params.ProjectID, params.Path)

	case "projects.bind":
		var params struct {
			ConversationID string `json:"conversationId"`
			ProjectID      string `json:"projectId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, s.svc.SetConversationProject(params.ConversationID, params.ProjectID)

	default:
		return nil, fmt.Errorf("未知 IPC 方法 %q", request.Method)
	}
}
