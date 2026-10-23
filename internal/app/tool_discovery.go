package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"icetudio/internal/model"
)

type discoveryTools struct {
	inner   bookkeepingTools
	mu      sync.Mutex
	loaded  map[string]model.Tool
	offered map[string]string
}

func newDiscoveryTools(inner bookkeepingTools) *discoveryTools {
	return &discoveryTools{inner: inner, loaded: map[string]model.Tool{}, offered: map[string]string{}}
}
func (*discoveryTools) DynamicDefinitions() bool { return true }
func toolFingerprint(t model.Tool) string {
	raw, _ := json.Marshal(t)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func toolSearchDefinition() model.Tool {
	t := model.Tool{Type: "function"}
	t.Function.Name = "local__tool_search"
	t.Function.Description = "按名称或用途搜索当前执行配置允许的 MCP 工具，并加载匹配工具的真实参数定义到下一轮请求。基础本机工具已直接可用。搜索不执行目标工具；先搜索，下一轮再调用。"
	t.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":200},"limit":{"type":"integer","minimum":1,"maximum":5}},"required":["query"],"additionalProperties":false}`)
	return t
}
func (d *discoveryTools) Definitions(ctx context.Context) ([]model.Tool, error) {
	if err := d.loadSearchFallback(ctx); err != nil {
		return nil, err
	}
	var local []model.Tool
	var err error
	if source, ok := d.inner.inner.(interface {
		LocalDefinitions(context.Context) ([]model.Tool, error)
	}); ok {
		local, err = source.LocalDefinitions(ctx)
	} else {
		local, err = d.inner.inner.Definitions(ctx)
	}
	if err != nil {
		return nil, err
	}
	basics := []model.Tool{}
	for _, t := range local {
		if strings.HasPrefix(t.Function.Name, "local__") {
			basics = append(basics, t)
		}
	}
	basics, err = d.inner.definitionsWith(ctx, basics)
	if err != nil {
		return nil, err
	}
	// A read-only worker cannot discover external/unknown effects. Its filtered
	// catalog is still enforced by bookkeepingTools.Definitions at search time.
	basics = append(basics, toolSearchDefinition())
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []model.Tool{}
	seen := map[string]bool{}
	for _, t := range basics {
		if d.inner.profile.AllowsTool(t.Function.Name) && !seen[t.Function.Name] {
			out = append(out, t)
			seen[t.Function.Name] = true
		}
	}
	for name, t := range d.loaded {
		if d.inner.profile.AllowsTool(name) && d.searchToolAllowed(name) && !seen[name] {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Function.Name < out[j].Function.Name })
	d.offered = map[string]string{}
	for _, t := range out {
		d.offered[t.Function.Name] = toolFingerprint(t)
	}
	return out, nil
}
func (d *discoveryTools) Call(ctx context.Context, name, raw string) (string, error) {
	if name == "local__tool_search" {
		return d.search(ctx, raw)
	}
	if strings.HasPrefix(name, "local__") {
		return d.inner.Call(ctx, name, raw)
	}
	if !d.searchToolAllowed(name) {
		return "", errors.New("本次任务未开启联网搜索")
	}
	d.mu.Lock()
	expected := d.offered[name]
	d.mu.Unlock()
	if expected == "" {
		return "", errors.New("工具未在本轮加载，请先调用 tool_search")
	}
	// Re-fetch real metadata immediately before dispatch. A changed schema or
	// removed tool cannot reuse the pending request/approval from the old turn.
	current, err := d.inner.Definitions(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range current {
		if t.Function.Name == name {
			if toolFingerprint(t) != expected {
				return "", errors.New("工具定义已变化；请重新搜索并在下一轮重新请求审批")
			}
			if err := d.inner.svc.SyncMCPEndpoints(ctx); err != nil {
				return "", err
			}
			return d.inner.Call(ctx, name, raw)
		}
	}
	return "", errors.New("工具已移除或当前组件不可用")
}
func (d *discoveryTools) search(ctx context.Context, raw string) (string, error) {
	if !d.inner.profile.AllowsTool("local__tool_search") {
		return "", errors.New("工具发现未启用")
	}
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeOperation(raw, &args); err != nil {
		return "", err
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" || len(args.Query) > 200 {
		return "", errors.New("搜索词需为 1..200 字节")
	}
	if args.Limit == 0 {
		args.Limit = 3
	}
	if args.Limit < 1 || args.Limit > 5 {
		return "", errors.New("最多加载 5 项匹配工具")
	}
	catalog, err := d.inner.Definitions(ctx)
	if err != nil {
		return "", err
	}
	if err := d.inner.svc.SyncMCPEndpoints(ctx); err != nil {
		return "", err
	}
	type match struct {
		Tool   model.Tool `json:"tool"`
		SHA256 string     `json:"sha256"`
		score  int
	}
	matches := []match{}
	terms := strings.Fields(strings.ToLower(args.Query))
	for _, t := range catalog {
		if !d.inner.profile.AllowsTool(t.Function.Name) || !d.searchToolAllowed(t.Function.Name) {
			continue
		}
		hay := strings.ToLower(t.Function.Name + " " + t.Function.Description)
		score := 0
		for _, term := range terms {
			if strings.Contains(hay, term) {
				score++
			}
		}
		if strings.EqualFold(t.Function.Name, args.Query) {
			score += 100
		}
		if score > 0 {
			matches = append(matches, match{Tool: t, SHA256: toolFingerprint(t), score: score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].Tool.Function.Name < matches[j].Tool.Function.Name
		}
		return matches[i].score > matches[j].score
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	selected := []match{}
	skipped := 0
	added := 0
	for _, m := range matches {
		if len(selected) >= args.Limit {
			break
		}
		_, exists := d.loaded[m.Tool.Function.Name]
		local := strings.HasPrefix(m.Tool.Function.Name, "local__")
		if !exists && !local && len(d.loaded)+added >= 32 {
			skipped++
			continue
		}
		trial := append(append([]match{}, selected...), m)
		data, _ := json.Marshal(trial)
		if len(data) > 12000 {
			skipped++
			continue
		}
		selected = trial
		if !exists && !local {
			added++
		}
	}
	// The schema result and subsequent model request snapshots are durable via
	// the normal ToolCall/Step recorder; compaction retains this run-local map.
	for _, m := range selected {
		if !strings.HasPrefix(m.Tool.Function.Name, "local__") {
			d.loaded[m.Tool.Function.Name] = m.Tool
		}
	}
	result, err := json.Marshal(map[string]any{"matches": selected, "skipped": skipped, "loadedCount": len(d.loaded), "instruction": "匹配定义已加载；下一轮调用目标工具，仍需权限与审批。"})
	if err != nil {
		return "", fmt.Errorf("编码工具发现结果：%w", err)
	}
	return string(result), nil
}
