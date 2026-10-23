package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/materials"
)

type Message struct {
	Files         []materials.File  `json:"files,omitempty"`
	ProviderState []json.RawMessage `json:"provider_state,omitempty"`
	Role          string            `json:"role"`
	Content       string            `json:"content,omitempty"`
	ToolCallID    string            `json:"tool_call_id,omitempty"`
	ToolCalls     []ToolCall        `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Search       SearchReport
	Message      Message
	FinishReason string
	Usage        Usage
}

// ChatClient is the key-less OpenAI-compatible client for the local Ollama
// and LM Studio servers. It speaks the same /v1 protocol in non-streaming and
// streaming form; the agent loop owns tool validation, execution and
// follow-up turns.
type ChatClient struct {
	Client *http.Client
}

func (c ChatClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Chat uses the common non-streaming endpoint supported by Ollama and LM Studio.
func (c ChatClient) Chat(ctx context.Context, source config.Model, messages []Message, tools []Tool) (ChatResponse, error) {
	if err := validateRequest(source, messages); err != nil {
		return ChatResponse{}, err
	}
	request := struct {
		Model     string            `json:"model"`
		Messages  []json.RawMessage `json:"messages"`
		Tools     []Tool            `json:"tools,omitempty"`
		Stream    bool              `json:"stream"`
		MaxTokens int               `json:"max_tokens"`
	}{Model: source.Name, Messages: compatMessages(messages), Tools: tools, Stream: false, MaxTokens: 8192}
	data, err := json.Marshal(request)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("编码模型请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(source.EffectiveBaseURL(), "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return ChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("模型请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, fmt.Errorf("模型服务返回 HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return ChatResponse{}, fmt.Errorf("解析模型回答: %w", err)
	}
	if len(payload.Choices) == 0 {
		return ChatResponse{}, errors.New("模型回答没有 choices")
	}
	return ChatResponse{Message: payload.Choices[0].Message, FinishReason: payload.Choices[0].FinishReason, Usage: payload.Usage}, nil
}

// chatStreamChunk is one SSE payload of the /chat/completions stream.
type chatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int64  `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// ChatStream consumes the streaming endpoint supported by Ollama and LM
// Studio, delivering every text delta to onDelta as it arrives and merging
// the full message in memory for the final response.
func (c ChatClient) ChatStream(ctx context.Context, source config.Model, messages []Message, tools []Tool, onDelta func(string) error) (ChatResponse, error) {
	if err := validateRequest(source, messages); err != nil {
		return ChatResponse{}, err
	}
	request := struct {
		Model     string            `json:"model"`
		Messages  []json.RawMessage `json:"messages"`
		Tools     []Tool            `json:"tools,omitempty"`
		Stream    bool              `json:"stream"`
		Options   *streamOptions    `json:"stream_options,omitempty"`
		MaxTokens int               `json:"max_tokens"`
	}{Model: source.Name, Messages: compatMessages(messages), Tools: tools, Stream: true, MaxTokens: 8192}
	if onDelta != nil {
		request.Options = &streamOptions{IncludeUsage: true}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("编码模型请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(source.EffectiveBaseURL(), "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return ChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("模型请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, fmt.Errorf("模型服务返回 HTTP %d", resp.StatusCode)
	}
	merger := &deltaMerger{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return ChatResponse{}, fmt.Errorf("解析模型流: %w", err)
		}
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta.Content != "" {
				if err := merger.addText(choice.Delta.Content, onDelta); err != nil {
					return ChatResponse{}, err
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				merger.addToolCall(int(call.Index), call.ID, call.Function.Name, call.Function.Arguments)
			}
			if choice.FinishReason != "" {
				merger.finish = choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			merger.usage = *chunk.Usage
		}
	}
	if err := scanner.Err(); err != nil {
		return ChatResponse{}, fmt.Errorf("读取模型流: %w", err)
	}
	return merger.response(), nil
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func validateRequest(source config.Model, messages []Message) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if source.Name == "" {
		return errors.New("请先选择模型 ID")
	}
	if len(messages) == 0 {
		return errors.New("消息列表不能为空")
	}
	return nil
}

func compatMessages(messages []Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(messages))
	for _, m := range messages {
		m.ProviderState = nil
		if len(m.Files) > 0 {
			out = append(out, materialJSON(m, "openai"))
		} else {
			raw, _ := json.Marshal(m)
			out = append(out, raw)
		}
	}
	return out
}
