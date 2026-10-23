package model

import (
	"context"
	"errors"
	"strings"

	"icetudio/internal/config"
)

// SearchOptions applies to a single model request, never provider configuration.
// OnEvent must persist before returning; its failure aborts the stream.
type SearchOptions struct {
	MaxCalls int                     `json:"maxCalls"`
	OnEvent  func(SearchEvent) error `json:"-"`
}
type SearchEvent struct {
	Queries []string `json:"queries,omitempty"`
	Type    string   `json:"type"`
	ID      string   `json:"id,omitempty"`
	Query   string   `json:"query,omitempty"`
	URL     string   `json:"url,omitempty"`
	Status  string   `json:"status,omitempty"`
}
type Citation struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}
type SearchReport struct {
	BudgetUnit  string        `json:"budgetUnit"`
	BudgetUsed  *int          `json:"budgetUsed"`
	Suggestions []string      `json:"suggestions,omitempty"`
	Events      []SearchEvent `json:"events"`
	Citations   []Citation    `json:"citations"`
	// Nil means the provider did not report separate search usage/cost.
	Calls *int     `json:"calls"`
	Cost  *float64 `json:"cost"`
	Text  string   `json:"text"`
}
type SearchChatModel interface {
	ChatWithSearch(context.Context, config.Model, []Message, []Tool, SearchOptions, func(string) error) (ChatResponse, error)
}

func NativeSearchSupported(source config.Model) bool {
	name := strings.TrimPrefix(source.Name, "models/")
	switch source.Provider {
	case "anthropic":
		for _, prefix := range []string{"claude-sonnet-4", "claude-opus-4", "claude-opus-5", "claude-haiku-4", "claude-mythos"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	case "gemini":
		if strings.Contains(name, "image") || strings.Contains(name, "tts") || strings.Contains(name, "audio") || strings.Contains(name, "live") {
			return false
		}
		return strings.HasPrefix(name, "gemini-3") || strings.HasPrefix(name, "gemini-4-")
	case "openai":
		if strings.Contains(name, "nano") || strings.Contains(name, "chat") || strings.Contains(name, "audio") || strings.Contains(name, "realtime") || strings.Contains(name, "search") {
			return false
		}
		for _, family := range []string{"gpt-4o", "gpt-4.1", "gpt-5", "gpt-6", "o3", "o4-mini"} {
			if name == family || strings.HasPrefix(name, family+"-") || strings.HasPrefix(name, family+".") {
				return true
			}
		}
	}
	return false
}
func (c Client) ChatWithSearch(ctx context.Context, source config.Model, messages []Message, tools []Tool, search SearchOptions, onDelta func(string) error) (ChatResponse, error) {
	if !NativeSearchSupported(source) {
		return ChatResponse{}, errors.New("当前模型接口尚未支持原生联网搜索")
	}
	if search.MaxCalls < 1 || search.MaxCalls > 3 {
		return ChatResponse{}, errors.New("原生搜索预算需为 1..3")
	}
	switch source.Provider {
	case "openai":
		return c.openaiChatWithSearch(ctx, source, messages, tools, search, onDelta)
	case "anthropic":
		return c.anthropicChatWithSearch(ctx, source, messages, tools, search, onDelta)
	case "gemini":
		return c.geminiChatWithSearch(ctx, source, messages, tools, search, onDelta)
	}
	return ChatResponse{}, errors.New("未实现原生搜索适配")
}
