package app

import (
	"context"
	"errors"
	"strings"

	"icetudio/internal/materials"

	"icetudio/internal/storage"
)

func (s *Service) ListConversations() ([]storage.Conversation, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	return s.store.ListConversations(context.Background())
}

func (s *Service) GetConversation(id string) (storage.Conversation, error) {
	if err := s.checkReady(); err != nil {
		return storage.Conversation{}, err
	}
	return s.store.GetConversation(context.Background(), id)
}

// CreateConversation uses the host's auto-created default RuntimeConfig when
// no config id is supplied; delegation sessions inherit the root config.
func (s *Service) CreateConversation(configID, reuseSession, title string) (storage.Conversation, error) {
	if err := s.checkReady(); err != nil {
		return storage.Conversation{}, err
	}
	if strings.TrimSpace(configID) == "" {
		configID = storage.DefaultConfigID
	}
	return s.store.CreateConversation(context.Background(), configID, reuseSession, title)
}

func (s *Service) GetLocalContext(id string) (storage.LocalContext, error) {
	if err := s.checkReady(); err != nil {
		return storage.LocalContext{}, err
	}
	return s.store.GetLocalContext(context.Background(), id)
}

func (s *Service) UpdateContextPolicy(id string, policy storage.ContextPolicy) (storage.LocalContext, error) {
	if err := s.checkReady(); err != nil {
		return storage.LocalContext{}, err
	}
	return s.store.UpdateContextPolicy(context.Background(), id, policy)
}

func (s *Service) RenameConversation(id, title string) (storage.Conversation, error) {
	if err := s.checkReady(); err != nil {
		return storage.Conversation{}, err
	}
	return s.store.RenameConversation(context.Background(), id, title)
}
func (s *Service) DeleteConversation(id string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	return s.store.DeleteConversation(context.Background(), id)
}

func (s *Service) MessageAttachment(messageID string, index int) (materials.File, error) {
	if err := s.checkReady(); err != nil {
		return materials.File{}, err
	}
	files, err := s.store.MessageFiles(context.Background(), messageID)
	if err != nil {
		return materials.File{}, err
	}
	if index < 0 || index >= len(files) {
		return materials.File{}, errors.New("找不到这个附件")
	}
	return files[index], nil
}

type ChatMessagePage struct {
	Messages  []storage.Message `json:"messages"`
	HasMore   bool              `json:"hasMore"`
	BeforeSeq int               `json:"beforeSeq"`
}

func (s *Service) ListChatMessages(sessionID string, before, limit int) (ChatMessagePage, error) {
	if err := s.checkReady(); err != nil {
		return ChatMessagePage{}, err
	}
	if limit < 1 || limit > 100 {
		return ChatMessagePage{}, errors.New("每页最多100条消息")
	}
	if err := s.checkSessionExists(sessionID); err != nil {
		return ChatMessagePage{}, err
	}
	items, err := s.store.ChatMessages(context.Background(), sessionID, before, limit+1)
	if err != nil {
		return ChatMessagePage{}, err
	}
	page := ChatMessagePage{Messages: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Messages = items[len(items)-limit:]
	}
	if len(page.Messages) > 0 {
		page.BeforeSeq = page.Messages[0].Seq
	}
	return page, nil
}
