package main

import (
	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ProjectService is the Wails binding surface for the authoritative music
// project. It owns no run state: every method delegates to the UI-agnostic
// app service so the CLI/IPC client and the Agent tools drive the exact
// same code path. Editing goes through SubmitProjectOps only — the
// transaction (op id, base revision, atomic batch, undo/redo) lives in one
// place for all entrypoints.
type ProjectService struct{ svc *app.Service }

type Project = storage.Project
type ProjectSummary = storage.ProjectSummary
type ProjectResult = project.Result
type ProjectHistoryEntry = project.HistoryEntry
type ProjectEditRecovery = app.ProjectEditRecovery
type TimeMapDraftRecord = app.TimeMapDraftRecord
type MusicInputsRecord = app.MusicInputsRecord

func (s *ProjectService) GetTimeMapDraft() (*TimeMapDraftRecord, error) {
	return s.svc.GetTimeMapDraft()
}
func (s *ProjectService) SaveTimeMapDraft(expectedToken, token, payload string) error {
	return s.svc.SaveTimeMapDraft(expectedToken, token, payload)
}
func (s *ProjectService) ClearTimeMapDraft(expectedToken, token string) error {
	return s.svc.ClearTimeMapDraft(expectedToken, token)
}

// Unapplied music form input drafts are UI recovery state in the Go data dir;
// browser localStorage is not durable across the native quit path.
func (s *ProjectService) GetMusicInputs() (*MusicInputsRecord, error) {
	return s.svc.GetMusicInputs()
}
func (s *ProjectService) SaveMusicInputs(expectedToken, token, payload string) error {
	return s.svc.SaveMusicInputs(expectedToken, token, payload)
}
func (s *ProjectService) ClearMusicInputs(expectedToken, token string) error {
	return s.svc.ClearMusicInputs(expectedToken, token)
}

func (s *ProjectService) GetPendingProjectEdit() (*ProjectEditRecovery, error) {
	return s.svc.GetPendingProjectEdit()
}

func (s *ProjectService) BeginProjectEdit(projectID, opID string, baseRevision int64, kind string) error {
	return s.svc.BeginProjectEdit(projectID, opID, baseRevision, kind)
}

func (s *ProjectService) ClearPendingProjectEdit(projectID, opID string) error {
	return s.svc.ClearPendingProjectEdit(projectID, opID)
}

// ListProjects returns every project without the state blob.
func (s *ProjectService) ListProjects() ([]ProjectSummary, error) {
	return s.svc.ListProjects()
}

func (s *ProjectService) GetProject(id string) (Project, error) {
	return s.svc.GetProject(id)
}

// CreateProject makes a new empty project; ppq 0 selects the default 480.
func (s *ProjectService) CreateProject(title string, ppq int) (Project, error) {
	return s.svc.CreateProject(title, ppq)
}

func (s *ProjectService) DeleteProject(id string) error {
	return s.svc.DeleteProject(id)
}

// SubmitProjectOps applies a whole batch or nothing. opID makes retries
// idempotent; baseRevision must match the current revision or the call is
// rejected instead of silently rebasing.
func (s *ProjectService) SubmitProjectOps(projectID, opID, source string, baseRevision int64, ops []project.Op) (ProjectResult, error) {
	if source == "" {
		source = "gui"
	}
	return s.svc.SubmitProjectOps(projectID, opID, source, baseRevision, ops)
}

func (s *ProjectService) UndoProject(projectID, opID string, baseRevision int64) (ProjectResult, error) {
	return s.svc.UndoProject(projectID, opID, "gui", baseRevision)
}

func (s *ProjectService) RedoProject(projectID, opID string, baseRevision int64) (ProjectResult, error) {
	return s.svc.RedoProject(projectID, opID, "gui", baseRevision)
}

// GetProjectTransaction returns the stored result of a committed operation
// so the UI can confirm the real outcome after an interrupted round trip.
func (s *ProjectService) GetProjectTransaction(projectID, opID string) (ProjectResult, bool, error) {
	return s.svc.GetProjectTransaction(projectID, opID)
}

func (s *ProjectService) ProjectHistory(projectID string) ([]ProjectHistoryEntry, error) {
	return s.svc.ProjectHistory(projectID)
}

// SetConversationProject binds or unbinds (empty projectID) a conversation
// to a project; the Agent tools only touch the bound project.
func (s *ProjectService) SetConversationProject(conversationID, projectID string) error {
	return s.svc.SetConversationProject(conversationID, projectID)
}

// ImportProjectMIDI reads a Standard MIDI File (formats 0/1, PPQ timing)
// into a new project; unsupported encodings are rejected, never guessed.
func (s *ProjectService) ImportProjectMIDI(path string) (Project, error) {
	return s.svc.ImportProjectMIDI(path)
}

func (s *ProjectService) ExportProjectMIDI(projectID, path string) error {
	return s.svc.ExportProjectMIDI(projectID, path)
}

// PickOpenMIDIFile shows the native open dialog and returns the chosen path
// ("" when cancelled). The desktop imports with the returned path.
func (s *ProjectService) PickOpenMIDIFile() (string, error) {
	return application.Get().Dialog.OpenFile().
		SetTitle("导入 MIDI 文件").
		CanChooseFiles(true).
		AddFilter("MIDI 文件", "*.mid;*.midi").
		PromptForSingleSelection()
}

// PickSaveMIDIFile shows the native save dialog and returns the chosen path
// ("" when cancelled).
func (s *ProjectService) PickSaveMIDIFile(defaultName string) (string, error) {
	return application.Get().Dialog.SaveFile().
		SetMessage("导出 MIDI 文件").
		SetFilename(defaultName).
		AddFilter("MIDI 文件", "*.mid").
		PromptForSingleSelection()
}
