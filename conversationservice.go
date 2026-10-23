package main

import (
	"icetudio/internal/app"
	"icetudio/internal/materials"
	"icetudio/internal/storage"
)

// Desktop operations use conversations and RuntimeConfigs (执行配置).
func (s *StatusService) ListConversations() ([]storage.Conversation, error) {
	return s.svc.ListConversations()
}
func (s *StatusService) GetConversation(id string) (storage.Conversation, error) {
	return s.svc.GetConversation(id)
}
func (s *StatusService) CreateConversation(configID, reuseSession, title string) (storage.Conversation, error) {
	return s.svc.CreateConversation(configID, reuseSession, title)
}
func (s *StatusService) SelectConversationConfig(id, configID string) (storage.Conversation, error) {
	return s.svc.SelectConversationConfig(id, configID)
}
func (s *StatusService) GetLocalContext(id string) (storage.LocalContext, error) {
	return s.svc.GetLocalContext(id)
}
func (s *StatusService) UpdateContextPolicy(id string, policy storage.ContextPolicy) (storage.LocalContext, error) {
	return s.svc.UpdateContextPolicy(id, policy)
}
func (s *StatusService) ListComponents() []storage.Component { return storage.Components() }

func (s *StatusService) GetRuntimeConfig(id string) (storage.RuntimeConfig, error) {
	return s.svc.GetRuntimeConfig(id)
}

func (s *StatusService) GetTaskGraph(runID string) (storage.TaskGraph, error) {
	return s.svc.GetTaskGraph(runID)
}

func (s *StatusService) ListTaskReviews(runID string) ([]storage.TaskReview, error) {
	return s.svc.ListTaskReviews(runID)
}
func (s *StatusService) StartConversationReview(sessionID, target string) (storage.Run, error) {
	return s.svc.StartConversationReview(sessionID, target)
}

func (s *StatusService) StartConversationTask(sessionID, input string, uploads []materials.Upload) (storage.Run, error) {
	return s.svc.StartConversationTask(sessionID, input, uploads...)
}

func (s *StatusService) SetConversationOptions(id string, options storage.ConversationOptions) (storage.Conversation, error) {
	return s.svc.SetConversationOptions(id, options)
}
func (s *StatusService) ConversationSearchAvailability(id string) (app.SearchAvailability, error) {
	return s.svc.ConversationSearchAvailability(id)
}
func (s *StatusService) ListConversationSearch(id string) ([]storage.SearchRequest, error) {
	return s.svc.ListConversationSearch(id)
}

func (s *StatusService) StartConversationRetrospective(sessionID, target string) (storage.Run, error) {
	return s.svc.StartConversationRetrospective(sessionID, target)
}
func (s *StatusService) ListTaskRetrospectives(runID string) ([]storage.TaskRetrospective, error) {
	return s.svc.ListTaskRetrospectives(runID)
}

func (s *StatusService) ContinueConversationTask(runID, taskID, input string, uploads []materials.Upload) (storage.Run, error) {
	return s.svc.ContinueConversationTask(runID, taskID, input, uploads...)
}

func (s *StatusService) RenameConversation(id, title string) (storage.Conversation, error) {
	return s.svc.RenameConversation(id, title)
}
func (s *StatusService) DeleteConversation(id string) error { return s.svc.DeleteConversation(id) }
func (s *StatusService) MessageAttachment(messageID string, index int) (materials.File, error) {
	return s.svc.MessageAttachment(messageID, index)
}

func (s *StatusService) PreviewSend(sessionID string, attachments bool, role string) ([]app.SendConsent, error) {
	return s.svc.PreviewSend(sessionID, attachments, role)
}

func (s *StatusService) ListChatMessages(sessionID string, before, limit int) (app.ChatMessagePage, error) {
	return s.svc.ListChatMessages(sessionID, before, limit)
}
