package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"icetudio/internal/ipc"
	"icetudio/internal/storage"
)

func cmdTasks(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("tasks 需要子命令 status|review|retrospect|continue|cancel")
	}
	switch args[0] {
	case "review", "retrospect":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || len(parsed.positional) != 2 || parsed.arg(0) == "" || parsed.arg(1) == "" {
			return usage("用法: tasks " + args[0] + " <sessionID> <runID>")
		}
		var run storage.Run
		if err := client.Call("tasks."+args[0], map[string]any{"sessionId": parsed.arg(0), "runId": parsed.arg(1)}, &run); err != nil {
			return fail("tasks."+args[0], err)
		}
		printRun(run)
		return 0
	case "status":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: tasks status <runID>")
		}
		var run storage.Run
		if err := client.Call("runs.get", map[string]any{"runId": parsed.arg(0)}, &run); err != nil {
			return fail("runs.get", err)
		}
		printRun(run)
		printChildRuns(client, run, "")
		var graph storage.TaskGraph
		if err := client.Call("tasks.graph", map[string]any{"runId": parsed.arg(0)}, &graph); err == nil {
			fmt.Printf("执行计划修订 %d · 调度 %s · 规划 %d 轮 · 事件 %d/%d\n", graph.Revision, graph.Coordination.State, graph.Coordination.Rounds, graph.Coordination.PlannedSeq, graph.Coordination.EventSeq)
			fmt.Printf("根预算：模型 %d/%d 次 · 工具 %d/%d 次 · token %d · 用量未知 %d 次\n", graph.Budget.ModelCalls, graph.Budget.Limits.MaxModelCalls, graph.Budget.ToolCalls, graph.Budget.Limits.MaxToolCalls, graph.Budget.Tokens, graph.Budget.UnknownUsage)
			if graph.WaitingTaskID != "" {
				fmt.Printf("等待阶段 %s：%s\n", graph.WaitingTaskID, graph.WaitingPrompt)
			}
			for _, item := range graph.Tasks {
				fmt.Printf("%s · %s · %s · %s\n", item.Spec.ID, item.Spec.Worker, item.State, item.Spec.Task)
			}
			for _, continuation := range graph.Continuations {
				fmt.Printf("续跑 %s · 原状态 %s · 阶段 %s · %s\n", continuation.ID, continuation.PreviousStatus, continuation.TaskID, continuation.CreatedAt)
			}
			for _, attempt := range graph.Attempts {
				fmt.Printf("尝试 %s · 任务 %s · %s · 运行 %s\n", attempt.ID, attempt.TaskID, attempt.Status, attempt.RunID)
			}
		}
		var reviews []storage.TaskReview
		if err := client.Call("tasks.reviews", map[string]any{"runId": parsed.arg(0)}, &reviews); err == nil {
			for _, r := range reviews {
				fmt.Printf("审查 %s · 当前 %t · %s\n%s\n", r.Result.Status, r.Current, r.Binding, r.Result.Summary)
				for _, d := range r.Result.Defects {
					fmt.Printf("  %s：%s\n", d.Reference, d.Description)
				}
			}
		}

		var reports []storage.TaskRetrospective
		if err := client.Call("tasks.retrospectives", map[string]any{"runId": parsed.arg(0)}, &reports); err == nil {
			for _, r := range reports {
				fmt.Printf("复盘 · 记录仍匹配 %t\n%s\n", r.Current, r.Result.Summary)
				for _, o := range r.Result.Observations {
					fmt.Printf("  %s：%s\n", o.Reference, o.Description)
				}
				for _, text := range r.Result.Recommendations {
					fmt.Printf("  建议：%s\n", text)
				}
				for _, text := range r.Result.Limitations {
					fmt.Printf("  限制：%s\n", text)
				}
			}
		}
		return 0
	case "continue":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("stage", "input")
		}
		if err != nil || len(parsed.positional) != 1 {
			return usage("用法: tasks continue <runID> [--stage <stageID> --input <补充信息>]")
		}
		var run storage.Run
		if err := client.Call("tasks.continue", map[string]any{"runId": parsed.arg(0), "taskId": parsed.value("stage"), "input": parsed.value("input")}, &run); err != nil {
			return fail("tasks.continue", err)
		}
		printRun(run)
		return 0
	case "cancel":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: tasks cancel <runID>")
		}
		if err := client.Call("tasks.cancel", map[string]any{"runId": parsed.arg(0)}, nil); err != nil {
			return fail("tasks.cancel", err)
		}
		fmt.Println("已请求取消，宿主将在安全点落盘结果。")
		return 0
	default:
		return usage(fmt.Sprintf("未知 tasks 子命令 %q", args[0]))
	}
}

func joinArgs(parts []string) string {
	return trimText(strings.Join(parts, " "))
}

func cmdApprovals(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("approvals 需要子命令 list|approve|deny")
	}
	switch args[0] {
	case "list":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues("context")
		}
		if err != nil {
			return usage(err.Error())
		}
		return listApprovals(client, parsed.value("context"))
	case "approve":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: approvals approve <callID>")
		}
		var call storage.ToolCall
		if err := client.Call("calls.approve", map[string]any{"callId": parsed.arg(0)}, &call); err != nil {
			return fail("calls.approve", err)
		}
		fmt.Printf("已批准 %s（%s）\n", call.ToolName, call.ID)
		return 0
	case "deny":
		parsed, err := parseCLI(args[1:])
		if err == nil {
			err = parsed.allowValues()
		}
		if err != nil || parsed.arg(0) == "" {
			return usage("用法: approvals deny <callID> [原因]")
		}
		note := joinArgs(parsed.positional[1:])
		var call storage.ToolCall
		if err := client.Call("calls.deny", map[string]any{"callId": parsed.arg(0), "note": note}, &call); err != nil {
			return fail("calls.deny", err)
		}
		fmt.Printf("已拒绝 %s（%s）\n", call.ToolName, call.ID)
		return 0
	default:
		return usage(fmt.Sprintf("未知 approvals 子命令 %q", args[0]))
	}
}

// listApprovals prints every call needing user action for the context — normal
// sessions and hidden delegation sessions alike — via the host-side sweep.
func listApprovals(client *ipc.Client, contextID string) int {
	var pending []storage.ToolCall
	if err := client.Call("approvals.pending", map[string]any{"contextId": contextID}, &pending); err != nil {
		return fail("approvals.pending", err)
	}
	for _, call := range pending {
		if call.State == storage.CallPending {
			fmt.Printf("[待审批] %s (run %s)\n  工具: %s\n  参数: %s\n", call.ID, call.RunID, call.ToolName, call.Arguments)
		}
		if call.State == storage.CallExecutionUnknown {
			fmt.Printf("[结果未知] %s (run %s)\n  工具: %s\n  说明: %s\n", call.ID, call.RunID, call.ToolName, call.Error)
		}
	}
	if len(pending) == 0 {
		fmt.Println("当前没有待处理的审批或结果未知调用。")
	}
	return 0
}

// approvalPrompter decides one pending call: approve=true grants it;
// approve=false with a note denies it. The skipNote sentinel (or a nil
// prompter, i.e. non-interactive mode) reports the request and leaves it
// pending — nothing is ever auto-approved.
type approvalPrompter func(call storage.ToolCall) (bool, string)

const skipNote = "__skip__"

// waitTask follows a run until it finishes, surfacing pending approvals.
// In non-interactive mode (prompter == nil) pending calls are printed and
// left untouched — they will time out or be handled on the desktop; nothing
// is auto-approved.
func waitTask(client *ipc.Client, sessionID, runID string, prompt approvalPrompter, out io.Writer) int {
	asked := map[string]bool{}
	for {
		var run storage.Run
		err := client.Call("runs.get", map[string]any{"runId": runID}, &run)
		if err != nil {
			fmt.Fprintf(out, "查询任务失败: %v\n", err)
			return 1
		}
		var calls []storage.ToolCall
		if err := client.Call("calls.list", map[string]any{"sessionId": sessionID}, &calls); err == nil {
			for _, call := range calls {
				if call.State != storage.CallPending || asked[call.ID] {
					continue
				}
				asked[call.ID] = true
				fmt.Fprintf(out, "需要审批：%s %s\n  参数: %s\n", call.ToolName, call.ID, call.Arguments)
				if prompt == nil {
					// 非交互：绝不默认放行，等待宿主超时或桌面端处理。
					fmt.Fprintln(out, "  非交互模式不会自动批准；可在桌面端或 approvals approve 命令中处理。")
					continue
				}
				approved, note := prompt(call)
				switch {
				case note == skipNote:
					fmt.Fprintln(out, "  保留待审批，稍后再处理。")
				case approved:
					if err := client.Call("calls.approve", map[string]any{"callId": call.ID}, nil); err != nil {
						fmt.Fprintf(out, "  批准失败: %v\n", err)
					} else {
						fmt.Fprintln(out, "  已批准。")
					}
				default:
					if err := client.Call("calls.deny", map[string]any{"callId": call.ID, "note": note}, nil); err != nil {
						fmt.Fprintf(out, "  拒绝失败: %v\n", err)
					} else {
						fmt.Fprintln(out, "  已拒绝。")
					}
				}
			}
			for _, call := range calls {
				if call.State == storage.CallExecutionUnknown && !asked[call.ID] {
					asked[call.ID] = true
					fmt.Fprintf(out, "结果未知：%s %s\n  说明: %s\n  请在桌面端说明依据后处置；任务在处置前无法继续。\n", call.ToolName, call.ID, call.Error)
				}
			}
		}
		if run.Status != "running" {
			printRun(run)
			printChildRuns(client, run, "")
			return runStatusExitCode(run)
		}
		select {
		case <-client.Closed():
			fmt.Fprintln(out, "宿主连接已断开；任务仍由宿主持有，重启宿主后可读取最终状态。")
			return 1
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func printRun(run storage.Run) {
	fmt.Printf("run %s\n状态: %s\n错误: %s\n执行配置: %s (v%d)\n深度: %d\n开始: %s\n更新: %s\n",
		run.ID, run.Status, run.Error, run.ConfigID, run.ConfigRevision, run.Depth, run.CreatedAt, run.UpdatedAt)
}

// printChildRuns queries the component tree by each parent run ID.
func printChildRuns(client *ipc.Client, run storage.Run, indent string) {
	var children []storage.Run
	if err := client.Call("runs.children", map[string]any{"runId": run.ID}, &children); err != nil || len(children) == 0 {
		return
	}
	fmt.Println(indent + "子任务:")
	for _, child := range children {
		line := fmt.Sprintf("%s  %s → %s [%s] 会话 %s", indent, child.ID, child.Component, child.Status, child.SessionID)
		if child.Error != "" {
			line += " · " + child.Error
		}
		fmt.Println(line)
		printChildRuns(client, child, indent+"    ")
	}
}

func runStatusExitCode(run storage.Run) int {
	if run.Status == "completed" {
		return 0
	}
	return 1
}

func trimText(text string) string {
	start, end := 0, len(text)
	for start < end && (text[start] == ' ' || text[start] == '\n' || text[start] == '\t' || text[start] == '\r') {
		start++
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\n' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return text[start:end]
}

// interactiveTerminal reports whether stdin is an interactive terminal, so
// only real users get approval prompts — pipes and CI never auto-approve.
func interactiveTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// terminalPrompter asks the user on the terminal for each pending call.
func terminalPrompter() approvalPrompter {
	reader := bufio.NewReader(os.Stdin)
	return func(call storage.ToolCall) (bool, string) {
		fmt.Printf("批准 %s 的这次调用？参数:\n%s\n[y/回车=批准, n=拒绝, s=跳过（保持待审批）] ", call.ToolName, call.Arguments)
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, "CLI 输入结束"
		}
		switch trimText(line) {
		case "", "y", "Y", "yes":
			return true, ""
		case "n", "N", "no":
			fmt.Print("拒绝原因（可留空）: ")
			note, _ := reader.ReadString('\n')
			return false, trimText(note)
		default:
			return false, skipNote
		}
	}
}

func cmdLogs(args []string) int {
	lines := 50
	if len(args) > 0 {
		if _, err := fmt.Sscanf(args[0], "%d", &lines); err != nil || lines <= 0 {
			return usage("用法: logs [行数]")
		}
	}
	dataDir, err := dataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data, err := os.ReadFile(logPath(dataDir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取日志失败:", err)
		return 1
	}
	all := splitLines(string(data))
	start := 0
	if len(all) > lines {
		start = len(all) - lines
	}
	for _, line := range all[start:] {
		fmt.Println(line)
	}
	return 0
}
