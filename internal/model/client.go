package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"icetudio/internal/config"
)

// ModelLister is the discovery surface: list the model IDs one configured
// provider currently offers. The local compat discovery and the unified
// Client both implement it.
type ModelLister interface {
	List(ctx context.Context, source config.Model) ([]string, error)
}

// CredentialLookup resolves the API key stored for a remote provider.
type CredentialLookup = config.CredentialLookup

// Client dispatches to the adapter of the configured provider: the key-less
// OpenAI-compatible client for local Ollama / LM Studio, and the official
// SDKs for OpenAI, Anthropic and Gemini. The SDKs only implement the model
// protocol; tools, permissions, approvals and sessions stay with IceTudio.
// API keys are resolved per request through Keys and never enter messages,
// snapshots or logs.
type Client struct {
	HTTP *http.Client
	Keys CredentialLookup
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return sharedHTTPClient()
}

var sharedHTTPClient = sync.OnceValue(func() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
})

func (c Client) Chat(ctx context.Context, source config.Model, messages []Message, tools []Tool) (ChatResponse, error) {
	return c.chat(ctx, source, messages, tools, nil)
}

// ChatStream streams the response, forwarding each text delta to onDelta and
// merging the complete message in memory. The returned ChatResponse is the
// final, fully merged message.
func (c Client) ChatStream(ctx context.Context, source config.Model, messages []Message, tools []Tool, onDelta func(string) error) (ChatResponse, error) {
	return c.chat(ctx, source, messages, tools, onDelta)
}

func (c Client) chat(ctx context.Context, source config.Model, messages []Message, tools []Tool, onDelta func(string) error) (ChatResponse, error) {
	if onDelta == nil {
		switch source.Provider {
		case "ollama", "lmstudio":
			return ChatClient{Client: c.HTTP}.Chat(ctx, source, messages, tools)
		case "openai":
			return c.openaiChat(ctx, source, messages, tools)
		case "anthropic":
			return c.anthropicChat(ctx, source, messages, tools)
		case "gemini":
			return c.geminiChat(ctx, source, messages, tools)
		default:
			return ChatResponse{}, fmt.Errorf("暂不支持模型提供方 %q", source.Provider)
		}
	}
	switch source.Provider {
	case "ollama", "lmstudio":
		return ChatClient{Client: c.HTTP}.ChatStream(ctx, source, messages, tools, onDelta)
	case "openai":
		return c.openaiChatStream(ctx, source, messages, tools, onDelta)
	case "anthropic":
		return c.anthropicChatStream(ctx, source, messages, tools, onDelta)
	case "gemini":
		return c.geminiChatStream(ctx, source, messages, tools, onDelta)
	default:
		return ChatResponse{}, fmt.Errorf("暂不支持模型提供方 %q", source.Provider)
	}
}

// apiKey resolves the credential for one remote provider with a clear,
// actionable error when it is missing.
func (c Client) apiKey(provider string) (string, error) {
	var label string
	switch provider {
	case "openai":
		label = "OpenAI"
	case "anthropic":
		label = "Anthropic"
	case "gemini":
		label = "Gemini"
	default:
		label = provider
	}
	if c.Keys == nil {
		return "", fmt.Errorf("未配置 %s API 密钥，请在凭据面板保存后再使用远端模型", label)
	}
	key, ok := c.Keys(provider)
	if !ok {
		return "", fmt.Errorf("未配置 %s API 密钥，请在凭据面板保存后再使用远端模型", label)
	}
	return key, nil
}

// providerTools converts the unified tool declarations to the raw name/
// description/JSON-schema form every adapter maps from.
type providerTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

func providerToolsOf(tools []Tool) []providerTool {
	result := make([]providerTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, providerTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return result
}

// deltaMerger accumulates streamed fragments into the final unified message.
// Every adapter feeds it primitives: text deltas and index-keyed tool-call
// fragments (id and name on the first fragment, argument JSON fragments
// after).
type deltaMerger struct {
	content strings.Builder
	calls   map[int]*ToolCall
	order   []int
	finish  string
	usage   Usage
}

func (m *deltaMerger) addText(text string, onDelta func(string) error) error {
	if onDelta != nil {
		if err := onDelta(text); err != nil {
			return err
		}
	}
	m.content.WriteString(text)
	return nil
}

func (m *deltaMerger) addToolCall(index int, id, name, argumentsDelta string) {
	if m.calls == nil {
		m.calls = map[int]*ToolCall{}
	}
	call, ok := m.calls[index]
	if !ok {
		call = &ToolCall{Type: "function"}
		m.calls[index] = call
		m.order = append(m.order, index)
	}
	if id != "" {
		call.ID = id
	}
	if name != "" {
		call.Function.Name = name
	}
	call.Function.Arguments += argumentsDelta
}

func (m *deltaMerger) response() ChatResponse {
	sort.Ints(m.order)
	message := Message{Role: "assistant", Content: m.content.String()}
	for _, index := range m.order {
		call := m.calls[index]
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", index)
		}
		if call.Function.Arguments == "" {
			call.Function.Arguments = "{}"
		}
		message.ToolCalls = append(message.ToolCalls, *call)
	}
	return ChatResponse{Message: message, FinishReason: m.finish, Usage: m.usage}
}

// toolSchema converts one tool's JSON-schema declaration into the generic
// properties/required form the adapters need.
func toolSchema(raw json.RawMessage) (properties map[string]any, required []string, err error) {
	if len(raw) == 0 {
		return map[string]any{}, nil, nil
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, fmt.Errorf("工具参数 schema 无效: %w", err)
	}
	if schema.Properties == nil {
		schema.Properties = map[string]any{}
	}
	return schema.Properties, schema.Required, nil
}
