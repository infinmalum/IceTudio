package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"icetudio/internal/ipc"
)

// cmdSkills manages the P4 skill surface: listing with disable state,
// reversible disable/enable, feedback review and user-recorded evaluations.
func cmdSkills(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("skills list | disable|enable <名称> | feedback <对话ID> | evaluations <名称> | evaluate <名称> <对话ID> <worked|partial|failed> [--run <runID>] [--note <备注>]")
	}
	switch args[0] {
	case "list":
		var skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Managed     bool   `json:"managed"`
			Disabled    bool   `json:"disabled"`
		}
		if err := client.Call("skills.list", nil, &skills); err != nil {
			return fail("Skill列表", err)
		}
		rows := [][]string{{"名称", "状态", "来源", "说明"}}
		for _, s := range skills {
			state := "启用"
			if s.Disabled {
				state = "已停用"
			}
			source := "安装"
			if s.Managed {
				source = "AI 草案副本"
			}
			rows = append(rows, []string{s.Name, state, source, s.Description})
		}
		printTable(rows)
		return 0
	case "disable", "enable":
		if len(args) != 2 || args[1] == "" {
			return usage("skills " + args[0] + " <名称>")
		}
		params := map[string]any{"skillName": args[1], "disabled": args[0] == "disable"}
		if err := client.Call("skills.setDisabled", params, nil); err != nil {
			return fail("切换 Skill 状态", err)
		}
		fmt.Printf("已%s %s；在途任务仍使用其创建时冻结的版本。\n", map[string]string{"disable": "停用", "enable": "恢复启用"}[args[0]], args[1])
		return 0
	case "feedback":
		if len(args) != 2 || args[1] == "" {
			return usage("skills feedback <对话ID>")
		}
		var notes []struct {
			ID        string `json:"id"`
			MessageID string `json:"messageId"`
			Quote     string `json:"quote"`
			Note      string `json:"note"`
			RunID     string `json:"runId"`
			ProjectID string `json:"projectId"`
			State     string `json:"state"`
		}
		if err := client.Call("feedback.list", map[string]any{"sessionId": args[1]}, &notes); err != nil {
			return fail("反馈记录", err)
		}
		if len(notes) == 0 {
			fmt.Println("该对话还没有反馈记录。")
			return 0
		}
		rows := [][]string{{"ID", "状态", "关联任务", "工程", "备注", "原话"}}
		for _, n := range notes {
			quote := strings.Join(strings.Fields(n.Quote), " ")
			if len(quote) > 60 {
				quote = quote[:60] + "…"
			}
			rows = append(rows, []string{n.ID, n.State, n.RunID, n.ProjectID, n.Note, quote})
		}
		printTable(rows)
		return 0
	case "evaluations":
		if len(args) != 2 || args[1] == "" {
			return usage("skills evaluations <名称>")
		}
		var evals []struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
			RunID     string `json:"runId"`
			Outcome   string `json:"outcome"`
			Note      string `json:"note"`
		}
		if err := client.Call("skillEvaluations.list", map[string]any{"skillName": args[1]}, &evals); err != nil {
			return fail("评估记录", err)
		}
		if len(evals) == 0 {
			fmt.Println("该 Skill 还没有评估记录；未实测不能宣称质量提升。")
			return 0
		}
		rows := [][]string{{"ID", "对话", "任务", "结论", "备注"}}
		for _, e := range evals {
			rows = append(rows, []string{e.ID, e.SessionID, e.RunID, e.Outcome, e.Note})
		}
		printTable(rows)
		return 0
	case "evaluate":
		p, err := parseCLI(args[1:])
		if err == nil {
			err = p.allowValues("run", "note")
		}
		if err != nil || len(p.positional) != 3 {
			return usage("skills evaluate <名称> <对话ID> <worked|partial|failed> [--run <runID>] [--note <备注>]")
		}
		outcome := p.arg(2)
		if outcome != "worked" && outcome != "partial" && outcome != "failed" {
			return usage("评估结论必须是 worked、partial 或 failed")
		}
		params := map[string]any{"skillName": p.arg(0), "sessionId": p.arg(1), "runId": p.value("run"), "outcome": outcome, "note": p.value("note")}
		var result json.RawMessage
		if err = client.Call("skillEvaluations.record", params, &result); err != nil {
			return fail("记录评估", err)
		}
		fmt.Println("已记录评估；它不改变启用状态，也不代表已证明质量提升。")
		return 0
	}
	return usage("未知子命令 " + args[0])
}
