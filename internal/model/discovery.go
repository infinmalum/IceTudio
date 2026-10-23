package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"icetudio/internal/config"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/genai"
)

type Discovery struct {
	Client *http.Client
}

func (d Discovery) List(ctx context.Context, source config.Model) ([]string, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	client := d.Client
	if client == nil {
		client = &http.Client{
			Timeout: 3 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接模型服务: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("模型服务返回 HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析模型列表: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// List dispatches discovery by provider: local servers use the shared
// /v1/models listing, remote providers use their official SDKs. Discovery is
// metadata-only — it carries no session context, but the credential lookup
// still applies and a missing key fails explicitly.
func (c Client) List(ctx context.Context, source config.Model) ([]string, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	switch source.Provider {
	case "ollama", "lmstudio":
		return Discovery{Client: c.HTTP}.List(ctx, source)
	case "openai":
		client, err := c.openaiClient(source)
		if err != nil {
			return nil, err
		}
		page, err := client.Models.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("OpenAI 模型列表失败: %w", err)
		}
		models := make([]string, 0, len(page.Data))
		for _, item := range page.Data {
			if item.ID != "" {
				models = append(models, item.ID)
			}
		}
		sort.Strings(models)
		return models, nil
	case "anthropic":
		client, err := c.anthropicClient(source)
		if err != nil {
			return nil, err
		}
		page, err := client.Models.List(ctx, anthropic.ModelListParams{})
		if err != nil {
			return nil, fmt.Errorf("Anthropic 模型列表失败: %w", err)
		}
		models := make([]string, 0, len(page.Data))
		for _, item := range page.Data {
			if item.ID != "" {
				models = append(models, item.ID)
			}
		}
		sort.Strings(models)
		return models, nil
	case "gemini":
		client, err := c.geminiClient(ctx, source)
		if err != nil {
			return nil, err
		}
		page, err := client.Models.List(ctx, &genai.ListModelsConfig{})
		if err != nil {
			return nil, fmt.Errorf("Gemini 模型列表失败: %w", err)
		}
		models := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			if item == nil || item.Name == "" {
				continue
			}
			models = append(models, geminiModelName(item.Name))
		}
		sort.Strings(models)
		return models, nil
	default:
		return nil, fmt.Errorf("暂不支持模型提供方 %q", source.Provider)
	}
}
