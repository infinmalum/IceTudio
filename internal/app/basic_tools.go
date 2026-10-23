package app

import (
	"context"
	"encoding/json"
	"errors"
	"icetudio/internal/agent"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"io"
	"strings"
)

func basicDefinitions() []model.Tool {
	specs := []struct{ name, description, schema string }{
		{"local__write_file", "新建或写入 UTF-8 文件（绝对路径，最多256KiB）。默认仅新建；覆盖必须提供完整 expectedContent。", `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"expectedContent":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`},
		{"local__edit_file", "精确替换文件中的唯一 oldText；缺失或多处匹配拒绝。path 必须为绝对路径。", `{"type":"object","properties":{"path":{"type":"string"},"oldText":{"type":"string"},"newText":{"type":"string"}},"required":["path","oldText","newText"],"additionalProperties":false}`},
		{"local__exec", "本机执行 executable + args，cwd 为绝对工作目录。默认120秒，最多600秒；返回退出码、stdout/stderr、截断/超时及 passed。需要 shell 时明确调用 shell。此工具不是沙箱。", `{"type":"object","properties":{"executable":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"cwd":{"type":"string"},"timeoutSeconds":{"type":"integer"}},"required":["executable","args","cwd"],"additionalProperties":false}`},
	}
	out := []model.Tool{}
	for _, s := range specs {
		d := model.Tool{Type: "function"}
		d.Function.Name = s.name
		d.Function.Description = s.description
		d.Function.Parameters = json.RawMessage(s.schema)
		out = append(out, d)
	}
	return out
}
func decodeOperation(raw string, out any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("参数后存在额外内容")
	}
	return nil
}
func decodeRequiredOperation(raw string, out any, keys ...string) error {
	if err := decodeOperation(raw, out); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return err
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || strings.TrimSpace(string(value)) == "null" {
			return errors.New("缺少必需参数：" + key)
		}
	}
	return nil
}
func (t bookkeepingTools) basicCall(ctx context.Context, name, raw string) (string, error) {
	gate := toolGate{svc: t.svc, contextID: t.profile.ContextID, snapshot: &t.profile, runID: t.runID}
	p, err := gate.effectiveSnapshot(ctx)
	if err != nil {
		return "", err
	}
	if deny := gate.hardLimits(ctx, p, agent.ToolRequest{Tool: name, Arguments: raw}); deny != "" {
		return "", errors.New(deny)
	}
	switch name {
	case "local__write_file":
		var args mcptools.WriteArgs
		if err := decodeRequiredOperation(raw, &args, "path", "content"); err != nil {
			return "", err
		}
		return mcptools.WriteFile(ctx, args, p.Policy.Permissions.Unrestricted, p.Policy.Permissions.WorkspacePaths)
	case "local__edit_file":
		var args mcptools.EditArgs
		if err := decodeRequiredOperation(raw, &args, "path", "oldText", "newText"); err != nil {
			return "", err
		}
		return mcptools.EditFile(ctx, args, p.Policy.Permissions.Unrestricted, p.Policy.Permissions.WorkspacePaths)
	case "local__exec":
		var args mcptools.ExecArgs
		if err := decodeRequiredOperation(raw, &args, "executable", "args", "cwd"); err != nil {
			return "", err
		}
		return mcptools.Exec(ctx, args)
	}
	return "", errors.New("未知基础工具")
}
