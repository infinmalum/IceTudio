package ipc

import (
	"icetudio/internal/materials"
	"icetudio/internal/storage"
)

// The same application contract is used by the RuntimeConfig (执行配置)
// oriented CLI and desktop.
func (s *Server) dispatchConversation(request Request) (any, error) {
	var params struct {
		Attachments  []materials.Upload          `json:"attachments"`
		Options      storage.ConversationOptions `json:"options"`
		ID           string                      `json:"id"`
		ConfigID     string                      `json:"configId"`
		ReuseSession string                      `json:"reuseSession"`
		Title        string                      `json:"title"`
		Input        string                      `json:"input"`
		Policy       storage.ContextPolicy       `json:"policy"`
	}
	if err := decodeParams(request.Params, &params); err != nil {
		return nil, err
	}
	switch request.Method {
	case "conversations.options":
		return s.svc.SetConversationOptions(params.ID, params.Options)
	case "conversations.searchAvailability":
		return s.svc.ConversationSearchAvailability(params.ID)
	case "conversations.searchRecords":
		return s.svc.ListConversationSearch(params.ID)
	case "conversations.list":
		return s.svc.ListConversations()
	case "conversations.get":
		return s.svc.GetConversation(params.ID)
	case "conversations.create":
		return s.svc.CreateConversation(params.ConfigID, params.ReuseSession, params.Title)
	case "conversations.start":
		return s.svc.StartConversationTask(params.ID, params.Input, params.Attachments...)
	case "contexts.get":
		return s.svc.GetLocalContext(params.ID)
	case "contexts.update":
		return s.svc.UpdateContextPolicy(params.ID, params.Policy)
	case "components.list":
		return storage.Components(), nil
	}
	return nil, nil
}
