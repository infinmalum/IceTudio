package app

import (
	"context"
	"encoding/json"
	"errors"
	"icetudio/internal/model"
	"icetudio/internal/storage"
	"strings"
	"unicode/utf8"
)

func traceDefinitions() []model.Tool {
	var tools []model.Tool
	for _, name := range []string{"local__search_worker_traces", "local__list_worker_traces", "local__get_worker_trace", "local__record_observation"} {
		t := model.Tool{Type: "function"}
		t.Function.Name = name
		t.Function.Description = "在当前根任务内交换其他 worker 的工具过程与显式观察；排除自身，不表示已独立验证。search 用 q；get 用 id 并以 offset 分页取 content；record_observation 用 content 记录观察。"
		t.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"},"id":{"type":"string"},"offset":{"type":"integer","minimum":0},"content":{"type":"string"},"part":{"type":"string","enum":["content","arguments","error"]}}}`)
		tools = append(tools, t)
	}
	return tools
}

// ListWorkerTraces returns one root run's worker work history — delegated
// tool calls and explicit observations, newest first — for the developer
// view. The same store query backs the model-side trace tools.
func (s *Service) ListWorkerTraces(runID, query string) ([]storage.WorkerTrace, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(runID) == "" {
		return []storage.WorkerTrace{}, nil
	}
	return s.store.WorkerTraces(context.Background(), runID, query)
}
func (t bookkeepingTools) traceCall(ctx context.Context, name, raw string) (string, error) {
	if !t.delegated {
		return "", errors.New("过程交换只向 worker 提供")
	}
	var args struct {
		Q       string `json:"q"`
		ID      string `json:"id"`
		Offset  int    `json:"offset"`
		Content string `json:"content"`
		Part    string `json:"part"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", err
	}
	if name == "local__record_observation" {
		scope, err := t.svc.sessionScopeMax(ctx, t.sessionID, t.profile)
		if err != nil {
			return "", err
		}
		item, err := t.svc.store.RecordObservation(ctx, t.runID, args.Content, scope)
		if err != nil {
			return "", err
		}
		return item.ID, nil
	}
	items, err := t.svc.store.WorkerTraces(ctx, t.runID, args.Q)
	if err != nil {
		return "", err
	}
	if name == "local__get_worker_trace" {
		for _, item := range items {
			if item.ID != args.ID {
				continue
			}
			full := item.Content
			switch args.Part {
			case "", "content":
				args.Part = "content"
			case "arguments":
				full = item.Arguments
			case "error":
				full = item.Error
			default:
				return "", errors.New("part 必须为 content、arguments 或 error")
			}
			if args.Offset < 0 || args.Offset > len(full) {
				return "", errors.New("无效过程分页 offset")
			}
			if args.Offset < len(full) && !utf8.RuneStart(full[args.Offset]) {
				return "", errors.New("offset 必须为字符边界")
			}
			end := args.Offset + 5000
			if end > len(full) {
				end = len(full)
			}
			for end < len(full) && end > args.Offset && !utf8.RuneStart(full[end]) {
				end--
			}
			part := full[args.Offset:end]
			next := -1
			if end < len(full) {
				next = end
			}
			item.Content = ""
			item.Arguments = ""
			item.Error = ""
			if args.Part == "content" {
				item.Content = part
			} else if args.Part == "arguments" {
				item.Arguments = part
			} else {
				item.Error = part
			}
			item.Arguments = boundedText(item.Arguments, 5000)
			item.Error = boundedText(item.Error, 5000)
			if err := t.svc.store.AccumulateContextScopes(ctx, t.sessionID, []string{item.Scope}); err != nil {
				return "", err
			}
			payload, _ := json.Marshal(map[string]any{"trace": item, "nextOffset": next, "part": args.Part})
			return string(payload), nil
		}
		return "", errors.New("过程不属于本根任务、属于自身或不存在")
	}
	if name != "local__search_worker_traces" && name != "local__list_worker_traces" {
		return "", errors.New("未知过程工具")
	}
	summaries := []storage.WorkerTrace{}
	for i, item := range items {
		if i >= 20 {
			break
		}
		item.Content = boundedText(item.Content, 120)
		item.Arguments = ""
		item.Error = ""
		summaries = append(summaries, item)
		if err := t.svc.store.AccumulateContextScopes(ctx, t.sessionID, []string{item.Scope}); err != nil {
			return "", err
		}
	}
	rawOut, _ := json.Marshal(map[string]any{"traces": summaries, "capped": len(items) > 20, "more": "按更精确 q 检索；get 按 ID 分页"})
	return string(rawOut), nil
}
func boundedText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}
