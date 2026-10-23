package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"icetudio/internal/ipc"
	"icetudio/internal/storage"
)

// cmdProjects drives the music project over the same IPC surface as the
// desktop app: the host applies identical validation, opID idempotency and
// revision conflict checks to both frontends.
func cmdProjects(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("projects 需要子命令 list|show|create|delete|edit|undo|redo|transaction|import|export|bind")
	}
	switch args[0] {
	case "list":
		var projects []storage.ProjectSummary
		if err := client.Call("projects.list", nil, &projects); err != nil {
			return fail("projects.list", err)
		}
		rows := [][]string{{"ID", "名称", "PPQ", "修订", "更新时间"}}
		for _, p := range projects {
			rows = append(rows, []string{p.ID, p.Title, strconv.Itoa(p.PPQ), fmt.Sprint(p.Revision), p.UpdatedAt})
		}
		printTable(rows)
		return 0

	case "show":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("track", "clip", "start", "end")
		}
		if err != nil {
			return usage(err.Error())
		}
		if parsed.arg(0) == "" {
			return usage("用法: projects show <工程ID> [--track <轨道ID>] [--clip <片段ID>] [--start <tick>] [--end <tick>]")
		}
		var project struct {
			ID        string          `json:"id"`
			Title     string          `json:"title"`
			PPQ       int             `json:"ppq"`
			Revision  int64           `json:"revision"`
			State     json.RawMessage `json:"state"`
			UpdatedAt string          `json:"updatedAt"`
		}
		params := map[string]any{"projectId": parsed.arg(0)}
		if parsed.value("track") != "" || parsed.value("clip") != "" || parsed.value("start") != "" || parsed.value("end") != "" {
			// Scoped read: half-open [start,end); notes count when overlapping.
			params["trackId"], params["clipId"] = parsed.value("track"), parsed.value("clip")
			for name, key := range map[string]string{"start": "startTick", "end": "endTick"} {
				if v := parsed.value(name); v != "" {
					tick, err := strconv.ParseInt(v, 10, 64)
					if err != nil || tick < 0 {
						return usage("无效 tick " + v)
					}
					params[key] = tick
				}
			}
			if err := client.Call("projects.query", params, &project); err != nil {
				return fail("projects.query", err)
			}
		} else if err := client.Call("projects.get", map[string]any{"id": parsed.arg(0)}, &project); err != nil {
			return fail("projects.get", err)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(project); err != nil {
			return fail("输出工程", err)
		}
		fmt.Println("提示：编辑用 `icetudio projects edit <ID> --file <ops.json>`；ops.json 形如 {\"baseRevision\":N,\"ops\":[…]}。")
		return 0

	case "create":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("title", "ppq")
		}
		if err != nil {
			return usage(err.Error())
		}
		ppq := 0
		if v := parsed.value("ppq"); v != "" {
			if ppq, err = strconv.Atoi(v); err != nil || ppq < 1 {
				return usage("无效 PPQ " + v)
			}
		}
		var created storage.Project
		if err := client.Call("projects.create", map[string]any{"title": parsed.value("title"), "ppq": ppq}, &created); err != nil {
			return fail("projects.create", err)
		}
		fmt.Printf("已创建工程 %s（%s，PPQ %d，修订 0）。\n", created.ID, created.Title, created.PPQ)
		return 0

	case "delete":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: projects delete <工程ID>")
		}
		if err := client.Call("projects.delete", map[string]any{"id": parsed.arg(0)}, nil); err != nil {
			return fail("projects.delete", err)
		}
		fmt.Println("已删除工程；对话历史保留，绑定已解除。")
		return 0

	case "edit", "undo", "redo":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("file", "op-id")
		}
		if err != nil || parsed.arg(0) == "" {
			return usage(fmt.Sprintf("用法: projects %s <工程ID> --op-id <唯一操作ID>%s", args[0], map[bool]string{true: " --file <ops.json>"}[args[0] == "edit"]))
		}
		opID := parsed.value("op-id")
		if opID == "" {
			return usage("需要 --op-id：重试时复用同一 ID 可获得原结果，不会重复提交")
		}
		method := "projects." + args[0]
		params := map[string]any{"projectId": parsed.arg(0), "opId": opID}
		switch args[0] {
		case "edit":
			raw, err := os.ReadFile(parsed.value("file"))
			if err != nil {
				return fail("读取操作文件", err)
			}
			var body struct {
				BaseRevision *int64           `json:"baseRevision"`
				Ops          []map[string]any `json:"ops"`
			}
			if err := json.Unmarshal(raw, &body); err != nil || body.BaseRevision == nil || len(body.Ops) == 0 {
				return usage("ops.json 必须是 {\"baseRevision\":N,\"ops\":[…]}")
			}
			params["baseRevision"], params["ops"], params["source"] = *body.BaseRevision, body.Ops, "cli"
		default:
			var current struct {
				Revision int64 `json:"revision"`
			}
			if err := client.Call("projects.get", map[string]any{"id": parsed.arg(0)}, &current); err != nil {
				return fail("projects.get", err)
			}
			params["baseRevision"] = current.Revision
		}
		var result struct {
			Revision int64          `json:"revision"`
			Applied  int            `json:"applied"`
			Summary  map[string]int `json:"summary"`
			Created  []string       `json:"createdIds"`
			OpID     string         `json:"opId"`
		}
		if err := client.Call(method, params, &result); err != nil {
			return fail(method, err)
		}
		fmt.Printf("已提交（修订 %d，应用 %d 组）：%v 新建 %v\n", result.Revision, result.Applied, result.Summary, result.Created)
		return 0

	case "transaction":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" || parsed.arg(1) == "" {
			return usage("用法: projects transaction <工程ID> <opId>")
		}
		var out struct {
			Found  bool            `json:"found"`
			Result json.RawMessage `json:"result"`
		}
		if err := client.Call("projects.transaction", map[string]any{"projectId": parsed.arg(0), "opId": parsed.arg(1)}, &out); err != nil {
			return fail("projects.transaction", err)
		}
		if !out.Found {
			fmt.Println("未找到该操作 ID 的提交记录（尚未提交或 ID 有误）。")
			return 1
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(out.Result); err != nil {
			return fail("输出结果", err)
		}
		return 0

	case "history":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: projects history <工程ID>")
		}
		var history []struct {
			Revision int64  `json:"revision"`
			Kind     string `json:"kind"`
			OpID     string `json:"opId"`
			Source   string `json:"source"`
			Undone   bool   `json:"undone"`
		}
		if err := client.Call("projects.history", map[string]any{"projectId": parsed.arg(0)}, &history); err != nil {
			return fail("projects.history", err)
		}
		rows := [][]string{{"修订", "类型", "opId", "来源", "已撤销"}}
		for _, h := range history {
			rows = append(rows, []string{fmt.Sprint(h.Revision), h.Kind, h.OpID, h.Source, map[bool]string{true: "是"}[h.Undone]})
		}
		printTable(rows)
		return 0

	case "import":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: projects import <MIDI文件>")
		}
		var created storage.Project
		if err := client.Call("projects.import", map[string]any{"path": parsed.arg(0)}, &created); err != nil {
			return fail("projects.import", err)
		}
		fmt.Printf("已导入工程 %s（%s，PPQ %d）。\n", created.ID, created.Title, created.PPQ)
		return 0

	case "export":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" || parsed.arg(1) == "" {
			return usage("用法: projects export <工程ID> <MIDI文件>")
		}
		if err := client.Call("projects.export", map[string]any{"projectId": parsed.arg(0), "path": parsed.arg(1)}, nil); err != nil {
			return fail("projects.export", err)
		}
		fmt.Printf("已导出 %s。\n", parsed.arg(1))
		return 0

	case "bind":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: projects bind <对话ID> <工程ID|off>")
		}
		projectID := parsed.arg(1)
		if strings.EqualFold(projectID, "off") {
			projectID = ""
		}
		if err := client.Call("projects.bind", map[string]any{"conversationId": parsed.arg(0), "projectId": projectID}, nil); err != nil {
			return fail("projects.bind", err)
		}
		if projectID == "" {
			fmt.Println("已解除对话与工程的绑定。")
		} else {
			fmt.Printf("对话已绑定工程 %s；该对话的 Agent 只能编辑这个工程。\n", projectID)
		}
		return 0

	default:
		return usage(fmt.Sprintf("未知 projects 子命令 %q", args[0]))
	}
}
