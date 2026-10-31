package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"icetudio/internal/agent"
	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

type materialPrepareArgs struct {
	Query     json.RawMessage            `json:"query"`
	Tool      string                     `json:"tool"`
	Field     string                     `json:"field"`
	Arguments map[string]json.RawMessage `json:"arguments"`
}
type preparedProjectMaterial struct {
	ID           string          `json:"preparationId"`
	RunID        string          `json:"runId"`
	ProjectID    string          `json:"projectId"`
	Title        string          `json:"title"`
	Revision     int64           `json:"revision"`
	SHA256       string          `json:"sha256"`
	Content      json.RawMessage `json:"content"`
	Tool         string          `json:"tool"`
	SchemaHash   string          `json:"schemaHash"`
	EndpointHash string          `json:"endpointHash"`
	Destination  string          `json:"destination"`
	Arguments    string          `json:"arguments"`
	Summary      string          `json:"summary"`
}

func digestMaterial(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func materialTransferDefinitions() []model.Tool {
	var out []model.Tool
	for _, spec := range []struct{ name, description, schema string }{
		{"project__prepare_material", "准备范围内工程材料给指定 MCP：query 必须限定 trackId 或 clipId，沿用 project__query 参数；field 必须是 MCP 声明的 string 顶层字段，宿主填入冻结查询 JSON 文本。其他 arguments 不得包含 field。只准备不发送；返回 preparationId，随后 local__send_material 逐次请用户核对发送。", `{"type":"object","properties":{"query":{"type":"object","properties":{"projectId":{"type":"string"},"trackId":{"type":"string"},"clipId":{"type":"string"},"startTick":{"type":"integer"},"endTick":{"type":"integer"}},"required":["projectId"],"additionalProperties":false},"tool":{"type":"string"},"field":{"type":"string"},"arguments":{"type":"object"}},"required":["query","tool","field","arguments"],"additionalProperties":false}`},
		{"local__send_material", "请求用户批准将已准备的具体工程材料发给原 MCP。仅接受本任务成功 preparationId，不发送对话/Skill 全文，不更新快照；不可重放已分派材料。结果是外部资料，音乐修改仍走原生事务。", `{"type":"object","properties":{"preparationId":{"type":"string"}},"required":["preparationId"],"additionalProperties":false}`},
	} {
		d := model.Tool{Type: "function"}
		d.Function.Name = spec.name
		d.Function.Description = spec.description
		d.Function.Parameters = json.RawMessage(spec.schema)
		out = append(out, d)
	}
	return out
}

func (t bookkeepingTools) executingMaterialCall(ctx context.Context, name, raw string) (storage.ToolCall, error) {
	calls, err := t.svc.store.ListRunToolCalls(ctx, t.runID)
	if err != nil {
		return storage.ToolCall{}, err
	}
	var selected storage.ToolCall
	for _, c := range calls {
		if c.ToolName == name && c.Arguments == raw && c.State == storage.CallExecuting {
			if selected.ID != "" {
				return selected, errors.New("存在重复在途调用")
			}
			selected = c
		}
	}
	if selected.ID == "" {
		return selected, errors.New("材料操作需要当前持久调用")
	}
	return selected, nil
}

func (t bookkeepingTools) materialTarget(ctx context.Context, name string) (model.Tool, string, string, error) {
	if serverOf(name) == "local" || serverOf(name) == "project" || !t.profile.AllowsTool(name) {
		return model.Tool{}, "", "", errors.New("材料目标必须是本任务允许的外部 MCP 工具")
	}
	if t.profile.Resources == nil {
		return model.Tool{}, "", "", errors.New("缺少运行能力快照")
	}
	if containsTool(t.profile.Resources.SearchTools, name) && !t.profile.Resources.Options.WebSearch {
		return model.Tool{}, "", "", errors.New("本任务未开启联网搜索")
	}
	defs, err := t.inner.Definitions(ctx)
	if err != nil {
		return model.Tool{}, "", "", err
	}
	if err = t.svc.SyncMCPEndpoints(ctx); err != nil {
		return model.Tool{}, "", "", err
	}
	var target model.Tool
	for _, d := range defs {
		if d.Function.Name == name {
			target = d
			break
		}
	}
	if target.Function.Name == "" || t.svc.endpoints == nil {
		return target, "", "", errors.New("目标工具未连接或无宿主身份")
	}
	for _, info := range t.svc.endpoints.EndpointInfos() {
		if info.Name == serverOf(name) && info.BinarySHA256 != "" && info.SchemaSHA256 != "" {
			raw, _ := json.Marshal(info)
			return target, digestMaterial(raw), info.Name + "（MCP 进程：" + info.Command + "）", nil
		}
	}
	return target, "", "", errors.New("MCP 身份指纹缺失，重新连接后准备材料")
}

func (t bookkeepingTools) materialRole(ctx context.Context) error {
	_, _, _, err := t.projectAccess(ctx)
	if err != nil {
		return err
	}
	run, err := t.svc.store.GetRun(ctx, t.runID)
	if err != nil {
		return err
	}
	if run.Status != "running" || (run.Component != "main" && run.Component != "worker") {
		return errors.New("此任务不能发送工程材料")
	}
	spec, found, err := t.svc.store.RunTaskSpec(ctx, t.runID)
	if err != nil {
		return err
	}
	if found && spec.AccessMode == "read" {
		return errors.New("只读 worker 不能发送工程材料")
	}
	return nil
}

func (t bookkeepingTools) prepareProjectMaterial(ctx context.Context, raw string) (string, error) {
	if !t.profile.AllowsTool("project__prepare_material") || !t.profile.AllowsTool("project__query") {
		return "", errors.New("材料准备或工程查询未启用")
	}
	if err := t.materialRole(ctx); err != nil {
		return "", err
	}
	var args materialPrepareArgs
	if err := decodeRequiredOperation(raw, &args, "query", "tool", "field", "arguments"); err != nil {
		return "", err
	}
	if len(args.Field) == 0 || len(args.Field) > 100 || len(raw) > 64<<10 {
		return "", errors.New("材料参数超出限制")
	}
	if _, ok := args.Arguments[args.Field]; ok {
		return "", errors.New("不能覆盖已提供的材料字段")
	}
	var region struct {
		TrackID string `json:"trackId"`
		ClipID  string `json:"clipId"`
	}
	if err := json.Unmarshal(args.Query, &region); err != nil {
		return "", err
	}
	if region.TrackID == "" && region.ClipID == "" {
		return "", errors.New("请选择明确轨道或片段，不隐式发送整工程")
	}
	content, err := t.projectCall(ctx, "project__query", string(args.Query))
	if err != nil {
		return "", err
	}
	if len(content) > 128<<10 {
		return "", errors.New("材料超过128KiB，请缩小查询范围")
	}
	var snapshot struct {
		ProjectID string        `json:"projectId"`
		Title     string        `json:"title"`
		PPQ       int           `json:"ppq"`
		Revision  int64         `json:"revision"`
		State     project.State `json:"state"`
	}
	if err = json.Unmarshal([]byte(content), &snapshot); err != nil {
		return "", err
	}
	// Region queries preserve empty track headers for editor projections.
	// Explicit material transfers must not export unrelated track metadata.
	selected := snapshot.State.Tracks[:0]
	for _, track := range snapshot.State.Tracks {
		if region.TrackID != "" && track.ID != region.TrackID {
			continue
		}
		if region.ClipID != "" {
			found := false
			for _, clip := range track.Clips {
				if clip.ID == region.ClipID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		selected = append(selected, track)
	}
	snapshot.State.Tracks = selected
	if len(snapshot.State.Tracks) == 0 {
		return "", errors.New("查询范围无材料")
	}
	frozen, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	content = string(frozen)
	def, identity, destination, err := t.materialTarget(ctx, args.Tool)
	if err != nil {
		return "", err
	}
	var schema struct {
		Properties map[string]struct {
			Type json.RawMessage `json:"type"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(def.Function.Parameters, &schema); err != nil {
		return "", err
	}
	field, ok := schema.Properties[args.Field]
	if !ok {
		return "", errors.New("目标 schema 未声明材料字段")
	}
	var typ string
	var types []string
	if !(json.Unmarshal(field.Type, &typ) == nil && typ == "string") && !(json.Unmarshal(field.Type, &types) == nil && containsID(types, "string")) {
		return "", errors.New("材料字段必须声明接受 string")
	}
	args.Arguments[args.Field], _ = json.Marshal(content)
	final, err := json.Marshal(args.Arguments)
	if err != nil {
		return "", err
	}
	if len(final) > 192<<10 {
		return "", errors.New("发送参数超过192KiB")
	}
	gate := toolGate{svc: t.svc, contextID: t.profile.ContextID, snapshot: &t.profile, runID: t.runID}
	if d := gate.Gate(ctx, agent.ToolRequest{Tool: args.Tool, Arguments: string(final)}); d.Deny != "" {
		return "", errors.New(d.Deny)
	}
	call, err := t.executingMaterialCall(ctx, "project__prepare_material", raw)
	if err != nil {
		return "", err
	}
	clips, notes, controls := 0, 0, 0
	labels := []string{}
	for _, tr := range snapshot.State.Tracks {
		if len(labels) < 4 {
			name := tr.Name
			if name == "" {
				name = "未命名轨道"
			}
			name = string([]rune(name)[:min(len([]rune(name)), 40)])
			clipNames := []string{}
			for _, c := range tr.Clips {
				if len(clipNames) == 4 {
					break
				}
				n := c.Name
				if n == "" {
					n = "未命名片段"
				}
				clipNames = append(clipNames, string([]rune(n)[:min(len([]rune(n)), 40)]))
			}
			labels = append(labels, fmt.Sprintf("轨「%s」：%s", name, strings.Join(clipNames, "、")))
		}
		for _, clip := range tr.Clips {
			clips++
			notes += len(clip.Notes)
			controls += len(clip.Controls)
		}
	}
	p := preparedProjectMaterial{ID: call.ID, RunID: t.runID, ProjectID: snapshot.ProjectID, Title: snapshot.Title, Revision: snapshot.Revision, Content: json.RawMessage(content), SHA256: digestMaterial([]byte(content)), Tool: args.Tool, SchemaHash: toolFingerprint(def), EndpointHash: identity, Destination: destination, Arguments: string(final), Summary: strings.Join(labels, "；") + fmt.Sprintf("（名称预览最多4轨，每轨4片段；完整清单见发送内容）。%d 轨 / %d 片段 / %d 音符 / %d 控制事件；%d 字节，含查询返回的时间图及元事件", len(snapshot.State.Tracks), clips, notes, controls, len(content))}
	out, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	if err = t.svc.store.SealPreparedMaterial(ctx, call.ID, string(out)); err != nil {
		return "", err
	}
	return string(out), nil
}

func (t bookkeepingTools) resolveProjectMaterial(ctx context.Context, raw string) (preparedProjectMaterial, error) {
	var p preparedProjectMaterial
	var args struct {
		ID string `json:"preparationId"`
	}
	if err := decodeRequiredOperation(raw, &args, "preparationId"); err != nil {
		return p, err
	}
	call, err := t.svc.store.PreparedProjectMaterial(ctx, t.runID, args.ID)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(call.Result), &p); err != nil {
		return p, err
	}
	scope, bound, err := t.runProjectScope(ctx)
	if err != nil {
		return p, err
	}
	if !bound || p.ID != call.ID || p.RunID != t.runID || p.ProjectID != scope.ProjectID || p.SHA256 != digestMaterial(p.Content) {
		return p, errors.New("材料来源或内容哈希不匹配")
	}
	return p, nil
}

func (t bookkeepingTools) validateMaterialSend(ctx context.Context, raw string) (preparedProjectMaterial, error) {
	p, err := t.resolveProjectMaterial(ctx, raw)
	if err != nil {
		return p, err
	}
	if err = t.materialRole(ctx); err != nil {
		return p, err
	}
	for _, name := range []string{"project__prepare_material", "project__query", "local__send_material", p.Tool} {
		if !t.profile.AllowsTool(name) {
			return p, errors.New("材料所需能力未启用")
		}
	}
	if _, err = t.svc.store.GetProject(ctx, p.ProjectID); err != nil {
		return p, err
	}
	def, identity, _, err := t.materialTarget(ctx, p.Tool)
	if err != nil {
		return p, err
	}
	if toolFingerprint(def) != p.SchemaHash || identity != p.EndpointHash {
		return p, errors.New("MCP 身份或工具定义已变化，请重新准备并审批")
	}
	gate := toolGate{svc: t.svc, contextID: t.profile.ContextID, snapshot: &t.profile, runID: t.runID}
	if d := gate.Gate(ctx, agent.ToolRequest{Tool: p.Tool, Arguments: p.Arguments}); d.Deny != "" {
		return p, errors.New(d.Deny)
	}
	return p, nil
}

func (t bookkeepingTools) sendProjectMaterial(ctx context.Context, raw string) (string, error) {
	p, err := t.validateMaterialSend(ctx, raw)
	if err != nil {
		return "", err
	}
	call, err := t.executingMaterialCall(ctx, "local__send_material", raw)
	if err != nil {
		return "", err
	}
	gate := toolGate{svc: t.svc, contextID: t.profile.ContextID, snapshot: &t.profile, runID: t.runID}
	if d := gate.Recheck(ctx, agent.ToolRequest{Tool: call.ToolName, Arguments: raw, CallID: call.CallID}); d.Deny != "" {
		return "", errors.New(d.Deny)
	}
	if err = t.svc.store.ClaimMaterialDispatch(ctx, t.runID, call.ID, p.ID); err != nil {
		return "", err
	}
	content, err := t.inner.Call(ctx, p.Tool, p.Arguments)
	if err != nil {
		return "", err
	}
	return materialEnvelope("external_mcp", p.Tool, "", content)
}

func materialApproval(p preparedProjectMaterial) *storage.MaterialApproval {
	return &storage.MaterialApproval{Title: p.Title, Revision: p.Revision, Destination: p.Destination, Tool: p.Tool, Summary: p.Summary, SHA256: p.SHA256, Arguments: p.Arguments}
}
