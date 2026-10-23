package main

import (
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// StatusService is the Wails binding surface. It owns no run state: every
// method delegates to the UI-agnostic app service so the CLI/IPC client
// drives the exact same code path.
type StatusService struct{ svc *app.Service }

type AppStatus = app.AppStatus

func (s *StatusService) GetStatus() AppStatus { return s.svc.GetStatus() }

func (s *StatusService) SaveModelConfig(next config.Model) (AppStatus, error) {
	return s.svc.SaveModelConfig(next)
}

// ListRuntimeConfigs returns every RuntimeConfig (执行配置; the shareable,
// versioned combinations), built-in default first.
func (s *StatusService) ListRuntimeConfigs() ([]storage.RuntimeConfig, error) {
	return s.svc.ListRuntimeConfigs()
}

// UpdateRuntimeConfig saves a new combination atomically and checks that the
// selected skills and MCP servers actually exist on this host.
func (s *StatusService) UpdateRuntimeConfig(c storage.RuntimeConfig) (storage.RuntimeConfig, error) {
	return s.svc.UpdateRuntimeConfig(c)
}

// RestoreRuntimeConfigDefault resets the built-in default config (执行配置)
// to the shipped combination.
func (s *StatusService) RestoreRuntimeConfigDefault(revision int) (storage.RuntimeConfig, error) {
	return s.svc.RestoreRuntimeConfigDefault(revision)
}

// ListMessages returns the display projection of a session: persisted
// messages plus one synthetic entry per tool call so the transcript shows
// tool activity without storing free-form tool rows again.
func (s *StatusService) ListMessages(sessionID string) ([]storage.Message, error) {
	return s.svc.ListMessages(sessionID)
}

func (s *StatusService) ListToolCalls(sessionID string) ([]storage.ToolCall, error) {
	return s.svc.ListToolCalls(sessionID)
}

// ResolveToolCall records a user disposition for a result-unknown call.
func (s *StatusService) ResolveToolCall(callID, state, result, note string) (storage.ToolCall, error) {
	return s.svc.ResolveToolCall(callID, state, result, note)
}

// ApproveToolCall grants one pending tool call; the approval binds to that
// exact call and its arguments.
func (s *StatusService) ApproveToolCall(callID string) (storage.ToolCall, error) {
	return s.svc.ApproveToolCall(callID)
}

// DenyToolCall refuses one pending tool call; the denial is returned to the
// model as a tool error.
func (s *StatusService) DenyToolCall(callID, note string) (storage.ToolCall, error) {
	return s.svc.DenyToolCall(callID, note)
}

// ListExfilGrants shows the destinations data may be sent to and the scopes
// each grant covers.
func (s *StatusService) ListExfilGrants(contextID string) ([]storage.ExfilGrant, error) {
	return s.svc.ListExfilGrants(contextID)
}

// GrantExfil authorizes sending the listed data scopes to one endpoint.
func (s *StatusService) GrantExfil(contextID, destination string, scopes []string) (storage.ExfilGrant, error) {
	return s.svc.GrantExfil(contextID, destination, scopes)
}

// RevokeExfil removes the authorization for one destination.
func (s *StatusService) RevokeExfil(contextID, destination string) error {
	return s.svc.RevokeExfil(contextID, destination)
}

// SaveCredential stores one remote provider's API key in the protected data
// directory. The key is write-only: no binding ever returns it.
func (s *StatusService) SaveCredential(provider, apiKey string) error {
	return s.svc.SaveCredential(provider, apiKey)
}

// DeleteCredential removes one remote provider's stored key.
func (s *StatusService) DeleteCredential(provider string) error {
	return s.svc.DeleteCredential(provider)
}

// ListCredentialStatus reports which remote providers have a stored key.
func (s *StatusService) ListCredentialStatus() ([]app.CredentialStatus, error) {
	return s.svc.ListCredentialStatus()
}

// PendingStreamText returns the streamed-but-uncommitted assistant text of a
// run, so a reconnecting client can pick the live transcript back up.
func (s *StatusService) PendingStreamText(runID string) (string, error) {
	return s.svc.PendingStreamText(runID)
}

// GetSessionSummary returns the session's compaction summary with its
// covered range and scope; an absent summary is an error.
func (s *StatusService) GetSessionSummary(sessionID string) (storage.SessionSummary, error) {
	return s.svc.GetSessionSummary(sessionID)
}

// GetSessionPlan returns the session's resumable plan with parsed steps.
func (s *StatusService) GetSessionPlan(sessionID string) (app.SessionPlanView, error) {
	return s.svc.GetSessionPlan(sessionID)
}

// ClearSessionPlan removes the session's plan; it never touches approvals,
// permissions or history.
func (s *StatusService) ClearSessionPlan(sessionID string) error {
	return s.svc.ClearSessionPlan(sessionID)
}

// ListMCPEndpoints shows each attached MCP server's source, process command,
// tool list and fingerprints, with a change marker when a fingerprint no
// longer matches the registry.
func (s *StatusService) ListMCPEndpoints() ([]app.MCPEndpointStatus, error) {
	return s.svc.ListMCPEndpoints()
}

// ListConfiguredMCPServers returns the configured MCP server catalog that
// assistant editing picks enabled servers from.
func (s *StatusService) ListConfiguredMCPServers() []config.MCPServer {
	return s.svc.ListConfiguredMCPServers()
}

// SaveMCPServer validates, persists and connects one MCP server: adding it,
// or replacing the entry with the same name. The command runs immediately so
// configuration mistakes surface here and not mid-task.
func (s *StatusService) SaveMCPServer(server config.MCPServer) ([]config.MCPServer, error) {
	return s.svc.SaveMCPServer(server)
}

// DeleteMCPServer removes one configured server, stops its process and drops
// its fingerprint registration.
func (s *StatusService) DeleteMCPServer(name string) ([]config.MCPServer, error) {
	return s.svc.DeleteMCPServer(name)
}

// ConfirmMCPEndpoint re-trusts one server's current fingerprints after the
// user reviewed the change; automatic execution resumes.
func (s *StatusService) ConfirmMCPEndpoint(name string) ([]app.MCPEndpointStatus, error) {
	return s.svc.ConfirmMCPEndpoint(name)
}

// ListSkills returns the installed skill catalog that assistant editing
// picks enabled skills from.
func (s *StatusService) ListSkills() ([]app.SkillInfo, error) {
	return s.svc.ListSkills()
}

// ReadHostLogs returns the recent lines of the host log, oldest first —
// the desktop counterpart of `icetudio logs`.
func (s *StatusService) ReadHostLogs(limit int) ([]string, error) {
	return s.svc.ReadHostLogs(limit)
}

// ListWorkerTraces returns one root run's worker work history for the
// developer view: delegated tool calls and explicit observations.
func (s *StatusService) ListWorkerTraces(runID, query string) ([]storage.WorkerTrace, error) {
	return s.svc.ListWorkerTraces(runID, query)
}

// ListRuns returns the runs of one session, newest first.
func (s *StatusService) ListRuns(sessionID string) ([]storage.Run, error) {
	return s.svc.ListRuns(sessionID)
}

func (s *StatusService) GetRun(id string) (storage.Run, error) {
	return s.svc.GetRun(id)
}

// ListChildRuns returns the delegated subtask runs of one run for the task
// tree; all components share the root Context.
func (s *StatusService) ListChildRuns(runID string) ([]storage.Run, error) {
	return s.svc.ListChildRuns(runID)
}

// PendingApprovals sweeps every session of the context — normal and hidden
// delegation sessions — for calls needing user action.
func (s *StatusService) PendingApprovals(contextID string) ([]storage.ToolCall, error) {
	return s.svc.PendingApprovals(contextID)
}

func (s *StatusService) CancelTask(id string) error {
	return s.svc.CancelTask(id)
}

func (s *StatusService) StartSkillDraft(sessionID, sourceName, request string, evidenceIDs []string) (storage.Run, error) {
	return s.svc.StartSkillDraft(sessionID, sourceName, request, evidenceIDs)
}
func (s *StatusService) ListSkillDrafts() ([]storage.SkillDraft, error) {
	return s.svc.ListSkillDrafts()
}
func (s *StatusService) EditSkillDraft(id string, revision int, proposal storage.SkillProposal) (storage.SkillDraft, error) {
	return s.svc.EditSkillDraft(id, revision, proposal)
}
func (s *StatusService) ApplySkillDraft(id string, revision int) (skills.Snapshot, error) {
	return s.svc.ApplySkillDraft(id, revision)
}
func (s *StatusService) DiscardSkillDraft(id string, revision int) error {
	return s.svc.DiscardSkillDraft(id, revision)
}
func (s *StatusService) WithdrawSkillDraft(id string, revision int) error {
	return s.svc.WithdrawSkillDraft(id, revision)
}
