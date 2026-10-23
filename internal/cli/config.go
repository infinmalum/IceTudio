package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"icetudio/internal/ipc"
	"icetudio/internal/storage"
)

// cmdConfig reads and updates execution configs over the same IPC surface
// the desktop settings use: show prints the stored JSON document, update
// submits an edited document (expected revision included, stale edits are
// refused), restore-default resets the built-in default combination.
func cmdConfig(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("config 需要子命令 list|show|update|restore-default")
	}
	switch args[0] {
	case "list":
		var configs []storage.RuntimeConfig
		if err := client.Call("configs.list", nil, &configs); err != nil {
			return fail("configs.list", err)
		}
		rows := [][]string{{"ID", "名称", "版本", "流程", "更新时间"}}
		for _, c := range configs {
			rows = append(rows, []string{c.ID, c.Name, fmt.Sprint(c.Revision), c.Data.Execution.Workflow, c.UpdatedAt})
		}
		printTable(rows)
		return 0
	case "show":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil {
			return usage(err.Error())
		}
		config, err := fetchConfig(client, parsed.arg(0))
		if err != nil {
			return fail("configs.get", err)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(config); err != nil {
			return fail("输出配置", err)
		}
		fmt.Println("提示：编辑后用 `icetudio config update --file <文件>` 提交；版本不一致会被拒绝。")
		return 0
	case "update":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("file")
		}
		if err != nil || parsed.value("file") == "" {
			return usage("用法: config update --file <配置JSON文件>")
		}
		raw, err := os.ReadFile(parsed.value("file"))
		if err != nil {
			return fail("读取配置文件", err)
		}
		var next storage.RuntimeConfig
		if err := json.Unmarshal(raw, &next); err != nil {
			return fail("解析配置文件", err)
		}
		var updated storage.RuntimeConfig
		if err := client.Call("configs.update", map[string]any{"config": next}, &updated); err != nil {
			return fail("configs.update", err)
		}
		fmt.Printf("已保存执行配置 %s（%s，修订 %d）。\n", updated.ID, updated.Name, updated.Revision)
		return 0
	case "restore-default":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("revision")
		}
		if err != nil {
			return usage(err.Error())
		}
		revision, err := fetchRevision(client, parsed.value("revision"))
		if err != nil {
			return fail("configs.get", err)
		}
		var restored storage.RuntimeConfig
		if err := client.Call("configs.restoreDefault", map[string]any{"revision": revision}, &restored); err != nil {
			return fail("configs.restoreDefault", err)
		}
		fmt.Printf("已恢复内置默认执行配置（修订 %d）。\n", restored.Revision)
		return 0
	default:
		return usage(fmt.Sprintf("未知 config 子命令 %q", args[0]))
	}
}

// fetchConfig loads a config by ID, defaulting to the built-in default.
func fetchConfig(client *ipc.Client, id string) (storage.RuntimeConfig, error) {
	if strings.TrimSpace(id) == "" {
		id = storage.DefaultConfigID
	}
	var config storage.RuntimeConfig
	err := client.Call("configs.get", map[string]any{"id": id}, &config)
	return config, err
}

// fetchRevision resolves the expected current revision: an explicit --revision
// value, or the config's current revision when omitted.
func fetchRevision(client *ipc.Client, explicit string) (int, error) {
	if strings.TrimSpace(explicit) != "" {
		var parsed int
		if _, err := fmt.Sscanf(explicit, "%d", &parsed); err != nil || parsed < 1 {
			return 0, fmt.Errorf("无效修订号 %q", explicit)
		}
		return parsed, nil
	}
	config, err := fetchConfig(client, "")
	if err != nil {
		return 0, err
	}
	return config.Revision, nil
}
