package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/project/smf"
	"icetudio/internal/storage"
)

func (s *Service) projectChanged(id string, revision int64) {
	if s.playback != nil {
		s.playback.Changed(id)
	}
	s.publish(Event{Type: "project.changed", ProjectID: id, Revision: revision})
}

// Project methods expose the authoritative music project to the desktop
// UI, the CLI (over IPC) and the agent tools. All three callers drive the
// exact same code path; the storage layer owns atomicity and idempotency.

func (s *Service) ListProjects() ([]storage.ProjectSummary, error) {
	return s.store.ListProjects(context.Background())
}

func (s *Service) GetProject(id string) (storage.Project, error) {
	return s.store.GetProject(context.Background(), id)
}

// QueryProject returns the project, or only the requested region when any
// scope is given (track, clip, half-open tick window). The agent's
// project__query and the CLI's projects show share this read path; the
// revision stays authoritative either way.
func (s *Service) QueryProject(id, trackID, clipID string, start, end *int64) (storage.Project, error) {
	item, err := s.store.GetProject(context.Background(), id)
	if err != nil {
		return item, err
	}
	item.State = *project.Region(&item.State, trackID, clipID, start, end)
	return item, nil
}

func (s *Service) CreateProject(title string, ppq int) (storage.Project, error) {
	if ppq == 0 {
		ppq = 480
	}
	item, err := s.store.CreateProject(context.Background(), title, ppq)
	if err == nil {
		s.projectChanged(item.ID, item.Revision)
	}
	return item, err
}

func (s *Service) DeleteProject(id string) error {
	err := s.store.DeleteProject(context.Background(), id)
	if err == nil {
		if s.playback != nil {
			s.playback.Deleted(id)
		}
		s.publish(Event{Type: "project.deleted", ProjectID: id})
	}
	return err
}

// SubmitProjectOps validates the request shape and commits the batch. The
// storage layer enforces opID replay and the base-revision conflict check.
func (s *Service) SubmitProjectOps(projectID, opID, source string, baseRevision int64, ops []project.Op) (project.Result, error) {
	if opID == "" {
		return project.Result{}, errors.New("缺少操作 ID（opId），无法去重与恢复")
	}
	if source == "" {
		source = "gui"
	}
	if len(ops) == 0 {
		return project.Result{}, errors.New("事务不能为空")
	}
	return s.submitProjectOps(context.Background(), projectID, opID, source, baseRevision, ops)
}

// Publish only committed outcomes. Agent and GUI/CLI share this helper.
func (s *Service) submitProjectOps(ctx context.Context, projectID, opID, source string, baseRevision int64, ops []project.Op) (project.Result, error) {
	result, err := s.store.SubmitProjectOps(ctx, projectID, opID, source, baseRevision, ops)
	if err == nil {
		s.projectChanged(projectID, result.Revision)
	}
	return result, err
}

func (s *Service) UndoProject(projectID, opID, source string, baseRevision int64) (project.Result, error) {
	if opID == "" {
		return project.Result{}, errors.New("缺少操作 ID（opId）")
	}
	if source == "" {
		source = "gui"
	}
	result, err := s.store.UndoProject(context.Background(), projectID, opID, source, baseRevision)
	if err == nil {
		s.projectChanged(projectID, result.Revision)
	}
	return result, err
}

func (s *Service) RedoProject(projectID, opID, source string, baseRevision int64) (project.Result, error) {
	if opID == "" {
		return project.Result{}, errors.New("缺少操作 ID（opId）")
	}
	if source == "" {
		source = "gui"
	}
	result, err := s.store.RedoProject(context.Background(), projectID, opID, source, baseRevision)
	if err == nil {
		s.projectChanged(projectID, result.Revision)
	}
	return result, err
}

func (s *Service) GetProjectTransaction(projectID, opID string) (project.Result, bool, error) {
	return s.store.GetProjectTransaction(context.Background(), projectID, opID)
}

func (s *Service) ProjectHistory(projectID string) ([]project.HistoryEntry, error) {
	return s.store.ProjectHistory(context.Background(), projectID)
}

// SetConversationProject binds or unbinds a conversation to a project. The
// binding decides which project the conversation's agent may touch.
func (s *Service) SetConversationProject(conversationID, projectID string) error {
	return s.store.SetSessionProject(context.Background(), conversationID, projectID)
}

// ImportProjectMIDI reads a strict SMF file and stores it as a new project.
// The file is fully validated before anything is written.
func (s *Service) ImportProjectMIDI(path string) (storage.Project, error) {
	if path == "" {
		return storage.Project{}, errors.New("缺少文件路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return storage.Project{}, err
	}
	state, err := smf.Import(data)
	if err != nil {
		return storage.Project{}, err
	}
	item, err := s.store.CreateProjectFromState(context.Background(), state)
	if err != nil {
		return storage.Project{}, err
	}
	s.projectChanged(item.ID, item.Revision)
	return item, nil
}

// ExportProjectMIDI writes the project as SMF via a temp file and an
// atomic rename, so a failed export never destroys the target file.
func (s *Service) ExportProjectMIDI(projectID, path string) error {
	if path == "" {
		return errors.New("缺少导出路径")
	}
	item, err := s.store.GetProject(context.Background(), projectID)
	if err != nil {
		return err
	}
	data, err := smf.Export(&item.State)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".icetudio-export-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// projectDefinitions are the agent-facing music tools. They are offered
// only when the run's conversation is bound to a project.
func projectDefinitions() []model.Tool {
	specs := []struct{ name, description, schema string }{
		{"project__query", "查询当前对话绑定的音乐工程：轨道、片段、音符、控制事件、速度/拍号映射与当前修订号。可选限定范围：trackId/clipId 或 startTick/endTick（半开区间 [start,end)，音符按重叠计入）；给出任一范围时只返回该区域，适合先查区域再构造批量编辑。结果 JSON 的 revision 字段是提交编辑时必须携带的基础修订。", `{"type":"object","properties":{"projectId":{"type":"string"},"trackId":{"type":"string"},"clipId":{"type":"string"},"startTick":{"type":"integer"},"endTick":{"type":"integer"}},"required":["projectId"],"additionalProperties":false}`},
		{"project__edit", "对绑定的音乐工程提交一组编辑操作（原子事务）：提供 baseRevision（来自最近一次查询）、opId（本次调用的唯一 ID）与 ops 数组。整组成功或整组拒绝；修订冲突时必须重新查询后重试。", `{"type":"object","properties":{"projectId":{"type":"string"},"opId":{"type":"string"},"baseRevision":{"type":"integer"},"ops":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"}},"required":["kind"]}}},"required":["projectId","opId","baseRevision","ops"],"additionalProperties":false}`},
	}
	out := []model.Tool{}
	for _, s := range specs {
		d := model.Tool{Type: "function"}
		d.Function.Name = s.name
		d.Function.Description = s.description
		d.Function.Parameters = json.RawMessage(s.schema)
		out = append(out, d)
	}
	return out
}

type projectEditArgs struct {
	ProjectID    string       `json:"projectId"`
	OpID         string       `json:"opId"`
	BaseRevision *int64       `json:"baseRevision"`
	Ops          []project.Op `json:"ops"`
}

// projectCall implements the two project tools inside a run. The run's
// session must be bound to a project, and both tools operate on exactly
// that project — a task cannot reach other projects by ID.
func (t bookkeepingTools) projectCall(ctx context.Context, name, raw string) (string, error) {
	session, err := t.svc.store.GetSession(ctx, t.sessionID)
	if err != nil {
		return "", fmt.Errorf("无法读取对话：%w", err)
	}
	if session.ProjectID == "" {
		return "", errors.New("当前对话未绑定音乐工程；请先在工程工作区选择或创建工程")
	}
	if name == "project__query" {
		var args struct {
			ProjectID string `json:"projectId"`
			TrackID   string `json:"trackId"`
			ClipID    string `json:"clipId"`
			StartTick *int64 `json:"startTick"`
			EndTick   *int64 `json:"endTick"`
		}
		if err := decodeRequiredOperation(raw, &args, "projectId"); err != nil {
			return "", err
		}
		if args.ProjectID != session.ProjectID {
			return "", errors.New("只能查询当前对话绑定的工程")
		}
		item, err := t.svc.QueryProject(args.ProjectID, args.TrackID, args.ClipID, args.StartTick, args.EndTick)
		if err != nil {
			return "", err
		}
		payload := map[string]any{
			"projectId": item.ID, "title": item.Title, "ppq": item.PPQ,
			"revision": item.Revision, "state": item.State,
		}
		out, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	// project__edit
	var args projectEditArgs
	if err := decodeRequiredOperation(raw, &args, "projectId", "opId", "baseRevision", "ops"); err != nil {
		return "", err
	}
	if args.ProjectID != session.ProjectID {
		return "", errors.New("只能编辑当前对话绑定的工程")
	}
	source := "agent:" + t.runID
	result, err := t.svc.submitProjectOps(ctx, session.ProjectID, args.OpID, source, *args.BaseRevision, args.Ops)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
