// Package cli is the command-line client of the running IceTudio host. It
// owns no run state: every command goes over IPC to the same application
// service the desktop UI drives, so approvals, ownership checks and the
// exfiltration gate behave identically in both frontends.
package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/host"
	"icetudio/internal/ipc"
)

// Run dispatches one CLI invocation and returns the process exit code.
// `serve` boots (or, for a data directory that already has one, refuses to
// run) the headless host; every other command connects to the running host
// of the user's data directory.
func Run(args []string) int {
	if len(args) == 0 {
		return usage("")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "help", "-h", "--help":
		return usage("")
	case "-v", "--version":
		fmt.Println("IceTudio CLI (host protocol", ipc.ProtocolVersion, ")")
		return 0
	}
	client, code := connect()
	if client == nil {
		return code
	}
	defer client.Close()
	return dispatch(client, args)
}

func usage(errDetail string) int {
	out := os.Stdout
	code := 0
	if errDetail != "" {
		out = os.Stderr
		fmt.Fprintf(out, "%s\n\n", errDetail)
		code = 2
	}
	fmt.Fprintln(out, `用法：
  icetudio serve                    启动无头宿主（含 IPC；桌面端同时内置）
  icetudio status                   查看宿主与模型状态
  icetudio skill-drafts generate <对话ID> <需求> [--source <Skill名称>] [--evidence <runID,runID>]
  icetudio skill-drafts list | show <ID> | edit <ID> <proposal.json> --revision N | apply|discard|withdraw <ID> --revision N
  icetudio conversations list | create [--reuse <对话ID>]
  icetudio conversations start <对话ID> <任务内容…> [--wait] | show <对话ID>
  icetudio conversations search <对话ID> [on|off|records]
  icetudio conversations permissions <对话ID> [--mode semi] [--workspace <路径>] [--command <可执行文件>]
  icetudio tasks status <runID> | review <sessionID> <runID> | cancel <runID>
  icetudio config list | show [--id <配置ID>] | update --file <配置JSON文件> | restore-default
  icetudio projects list | show <ID> | create [--title <名称>] [--ppq N] | delete <ID>
  icetudio projects edit <ID> --file <ops.json> --op-id <唯一ID> | undo|redo <ID> --op-id <唯一ID>
  icetudio projects transaction <ID> <opId> | history <ID> | import <MIDI文件> | export <ID> <MIDI文件>
  icetudio projects bind <对话ID> <工程ID|off>
  icetudio approvals list [--context <contextID>] | approve <callID> | deny <callID> [原因]
  icetudio logs [行数]`)
	return code
}

// serve runs the headless host: full startup, IPC listener, then wait for a
// termination signal. Client disconnects never cancel running tasks.
func serve(args []string) int {
	h, err := host.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "宿主启动失败:", err)
		return 1
	}
	defer h.Close()
	if h.InitError != nil {
		fmt.Fprintln(os.Stderr, "宿主初始化失败:", h.InitError)
		return 1
	}
	server, err := ipc.Start(h.Service, h.DataDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "IPC 启动失败:", err)
		return 1
	}
	defer server.Close()
	fmt.Println("IceTudio 宿主已启动（数据目录", h.DataDir(), "）；Ctrl+C 退出。")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	fmt.Println("正在退出宿主…")
	return 0
}

// connect dials the host with a short retry so a host that is still booting
// does not produce spurious failures.
func connect() (*ipc.Client, int) {
	dataDir, err := dataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, 1
	}
	client, err := ipc.Connect(dataDir, 2*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "提示：先运行 `icetudio serve`（或桌面端）再执行 CLI 命令。")
		return nil, 1
	}
	return client, 0
}

func dispatch(client *ipc.Client, args []string) int {
	switch args[0] {
	case "conversations":
		return cmdConversations(client, args[1:])
	case "status":
		return cmdStatus(client)
	case "skill-drafts":
		return cmdSkillDrafts(client, args[1:])
	case "tasks":
		return cmdTasks(client, args[1:])
	case "config":
		return cmdConfig(client, args[1:])
	case "projects":
		return cmdProjects(client, args[1:])
	case "approvals":
		return cmdApprovals(client, args[1:])
	case "logs":
		return cmdLogs(args[1:])
	case "events":
		return cmdEvents(client)
	default:
		return usage(fmt.Sprintf("未知命令 %q", args[0]))
	}
}

func cmdEvents(client *ipc.Client) int {
	client.OnEvent(func(evt app.Event) {
		fmt.Printf("[%s] run=%s session=%s %s\n", evt.Type, evt.RunID, evt.SessionID, evt.Detail)
	})
	fmt.Println("已订阅宿主事件，Ctrl+C 退出。")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-signals:
	case <-client.Closed():
		fmt.Fprintln(os.Stderr, "宿主连接已断开。")
		return 1
	}
	return 0
}

// dataDir resolves and creates the user's data directory; the CLI needs it to
// find the IPC socket and the host log file.
func dataDir() (string, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return "", err
	}
	if err := config.EnsureDirs(paths); err != nil {
		return "", fmt.Errorf("初始化数据目录失败: %w", err)
	}
	return paths.Data, nil
}

func cmdStatus(client *ipc.Client) int {
	var status app.AppStatus
	if err := client.Call("status", nil, &status); err != nil {
		fmt.Fprintln(os.Stderr, "status:", err)
		return 1
	}
	fmt.Printf("存储: %s\nAgent: %s\n模型: %s\n提供方: %s\n模型地址: %s\n模型 ID: %s\n",
		boolText(status.StorageReady, "已就绪", "异常"),
		boolText(status.RuntimeReady, "已就绪", "未配置"),
		boolText(status.ModelConnected, "已连接", "未连接"),
		status.Model.Provider, status.Model.EffectiveBaseURL(), status.Model.Name)
	if status.Message != "" {
		fmt.Println("消息:", status.Message)
	}
	if len(status.AvailableModels) > 0 {
		fmt.Println("可见模型:", strings.Join(status.AvailableModels, "、"))
	}
	return 0
}

func boolText(value bool, whenTrue, whenFalse string) string {
	if value {
		return whenTrue
	}
	return whenFalse
}

func fail(operation string, err error) int {
	fmt.Fprintf(os.Stderr, "%s 失败: %v\n", operation, err)
	return 1
}

func printTable(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if n := len([]rune(cell)); i < len(widths) && n > widths[i] {
				widths[i] = n
			}
		}
	}
	for _, row := range rows {
		line := make([]string, 0, len(row))
		for i, cell := range row {
			pad := widths[i] - len([]rune(cell))
			if pad < 0 {
				pad = 0
			}
			line = append(line, cell+strings.Repeat(" ", pad))
		}
		fmt.Println(strings.Join(line, "  "))
	}
}
