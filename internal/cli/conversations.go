package cli

import (
	"fmt"
	"os"

	"icetudio/internal/app"
	"icetudio/internal/ipc"
	"icetudio/internal/storage"
)

func cmdConversations(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("conversations 需要 list|create|start|show|search")
	}
	parsed, err := parseCLI(args[1:], "wait", "unrestricted", "restricted")
	if err != nil {
		return usage(err.Error())
	}
	switch args[0] {
	case "search":
		if parsed.arg(0) == "" {
			return usage("conversations search <id> [on|off|records]")
		}
		var c storage.Conversation
		if err := client.Call("conversations.get", map[string]any{"id": parsed.arg(0)}, &c); err != nil {
			return fail("读取对话", err)
		}
		choice := parsed.arg(1)
		if choice == "records" {
			var items []storage.SearchRequest
			if err := client.Call("conversations.searchRecords", map[string]any{"id": c.ID}, &items); err != nil {
				return fail("读取搜索记录", err)
			}
			for _, item := range items {
				fmt.Printf("[%s] %s · %s · run %s\n", item.State, item.Provider, item.ID, item.RunID)
				for _, citation := range item.Report.Citations {
					fmt.Printf("  %s %s\n", citation.Title, citation.URL)
				}
				if item.Error != "" {
					fmt.Println(item.Error)
				}
			}
			return 0
		}
		if choice != "" && choice != "on" && choice != "off" {
			return usage("搜索选项应为 on|off|records")
		}
		if choice != "" {
			c.Options.WebSearch = choice == "on"
			if err := client.Call("conversations.options", map[string]any{"id": c.ID, "options": c.Options}, &c); err != nil {
				return fail("设置联网搜索", err)
			}
		}
		var availability app.SearchAvailability
		if err := client.Call("conversations.searchAvailability", map[string]any{"id": c.ID}, &availability); err != nil {
			return fail("读取搜索能力", err)
		}
		fmt.Printf("联网搜索: %t · 可用: %t · 路径: %s\n%s\n", c.Options.WebSearch, availability.Available, availability.Route, availability.Reason)
		return 0

	case "permissions":
		if parsed.arg(0) == "" {
			return usage("conversations permissions <id> [--mode manual|semi|automatic] [--workspace <路径>] [--command <可执行文件>] [--unrestricted|--restricted]")
		}
		var c storage.Conversation
		if err := client.Call("conversations.get", map[string]any{"id": parsed.arg(0)}, &c); err != nil {
			return fail("conversations.get", err)
		}
		var local storage.LocalContext
		if err := client.Call("contexts.get", map[string]any{"id": c.ContextID}, &local); err != nil {
			return fail("contexts.get", err)
		}
		changed := false
		if mode := parsed.value("mode"); mode != "" {
			local.Policy.ApprovalMode = mode
			changed = true
		}
		if path := parsed.value("workspace"); path != "" {
			local.Policy.Permissions.WorkspacePaths = []string{path}
			changed = true
		}
		if command := parsed.value("command"); command != "" {
			local.Policy.Permissions.Commands = []string{command}
			changed = true
		}
		if parsed.flags["unrestricted"] && parsed.flags["restricted"] {
			return usage("权限范围选项不能同时使用")
		}
		if parsed.flags["unrestricted"] {
			local.Policy.Permissions.Unrestricted = true
			changed = true
		}
		if parsed.flags["restricted"] {
			local.Policy.Permissions.Unrestricted = false
			changed = true
		}
		if changed {
			if err := client.Call("contexts.update", map[string]any{"id": c.ContextID, "policy": local.Policy}, &local); err != nil {
				return fail("contexts.update", err)
			}
		}
		fmt.Printf("审批: %s\n一般资源范围不限: %t\n工作区: %v\n可执行文件: %v\n", local.Policy.ApprovalMode, local.Policy.Permissions.Unrestricted, local.Policy.Permissions.WorkspacePaths, local.Policy.Permissions.Commands)
		return 0
	case "list":
		var items []storage.Conversation
		if err := client.Call("conversations.list", nil, &items); err != nil {
			return fail("conversations.list", err)
		}
		rows := [][]string{{"ID", "标题", "执行配置"}}
		for _, c := range items {
			name := c.ConfigName
			if c.ConfigArchived {
				name += "（已删除）"
			}
			rows = append(rows, []string{c.ID, c.Title, name})
		}
		printTable(rows)
		return 0
	case "create":
		if err := parsed.allowValues("reuse", "title"); err != nil {
			return usage(err.Error())
		}
		var c storage.Conversation
		// 新对话不带配置参数：宿主使用自动创建的默认执行配置。
		if err := client.Call("conversations.create", map[string]any{"reuseSession": parsed.value("reuse"), "title": parsed.value("title")}, &c); err != nil {
			return fail("conversations.create", err)
		}
		fmt.Printf("已创建对话 %s（%s）\n", c.ID, c.ConfigName)
		return 0
	case "start", "show":
		if parsed.arg(0) == "" {
			return usage("conversations start <id> <任务内容…> [--wait] | show <id>")
		}
		var c storage.Conversation
		if err := client.Call("conversations.get", map[string]any{"id": parsed.arg(0)}, &c); err != nil {
			return fail("conversations.get", err)
		}
		if args[0] == "show" {
			var messages []storage.Message
			if err := client.Call("sessions.messages", map[string]any{"sessionId": c.ID}, &messages); err != nil {
				return fail("读取对话", err)
			}
			fmt.Printf("%s（%s）\n", c.Title, c.ConfigName)
			for _, message := range messages {
				fmt.Printf("[%s] %s\n", message.Role, message.Content)
			}
			return 0
		}
		if len(parsed.positional) < 2 {
			return usage("conversations start <id> <任务内容…> [--wait]")
		}
		var run storage.Run
		if err := client.Call("conversations.start", map[string]any{"id": c.ID, "input": joinArgs(parsed.positional[1:])}, &run); err != nil {
			return fail("conversations.start", err)
		}
		fmt.Printf("任务已启动：run %s\n", run.ID)
		if !parsed.flags["wait"] {
			return 0
		}
		var prompt approvalPrompter
		if interactiveTerminal() {
			prompt = terminalPrompter()
		}
		return waitTask(client, c.ID, run.ID, prompt, os.Stdout)
	default:
		return usage("未知 conversations 子命令")
	}
}
