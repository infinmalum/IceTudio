import { useState } from "react";
import type { ConversationState } from "../hooks/useConversation";
import type { ToolCall } from "../../bindings/icetudio/internal/storage";
export function describeCall(call: ToolCall) {
  let args: Record<string, unknown> = {};
  try {
    args = JSON.parse(call.arguments);
  } catch {}
  const path = String(args.path ?? args.file_path ?? args.filename ?? "");
  const command = args.executable
    ? String(args.executable) +
      " " +
      (Array.isArray(args.args)
        ? args.args.map((v) => JSON.stringify(v)).join(" ")
        : "")
    : String(args.command ?? args.cmd ?? "");
  const name = call.toolName.split("__").slice(-1)[0] ?? "";
  const labels: Record<string, string> = {
    read_file: "读取文件或文件夹",
    write_file: "写入文件",
    edit_file: "修改文件",
    exec: "执行本机命令",
    delegate_task: "安排一项协作工作",
    load_skill: "使用工作方法",
    memory_save: "记住一条信息",
    memory_delete: "删除一条记忆",
    tool_search: "查找可用能力",
  };
  return {
    title: labels[name] ?? "使用扩展服务",
    target:
      path ||
      command ||
      String(
        args.url ??
          args.destination ??
          args.task ??
          (name === "delegate_task"
            ? "安排当前工作的协作步骤"
            : "已连接的扩展服务"),
      ),
    detail:
      name === "exec"
        ? "命令可能修改文件或访问网络，请核对后允许。"
        : ["write_file", "edit_file"].includes(name)
          ? "这会改变文件内容。"
          : "只允许本次列出的具体操作。",
    name,
    args,
  };
}
export function ToolApprovals({
  conversation: c,
}: {
  conversation: Pick<
    ConversationState,
    | "approvalBusy"
    | "approveCall"
    | "denyCall"
    | "pendingCalls"
    | "resolveUnknown"
    | "unknownCalls"
  >;
}) {
  const [deferred, setDeferred] = useState<string[]>([]);
  return (
    <>
      {c.pendingCalls.map((call) => {
        const d = describeCall(call);
        return (
          <section className="approval-card" key={call.id}>
            <strong>IceTudio 想{d.title}</strong>
            <p className="operation-target">{d.target}</p>
            <p>{d.detail}</p>
            {!!d.args.cwd && <p>执行位置：{String(d.args.cwd)}</p>}
            <details>
              <summary>查看具体内容</summary>
              {d.name === "edit_file" ? (
                <>
                  <p>原内容</p>
                  <pre>{String(d.args.oldText ?? "")}</pre>
                  <p>改为</p>
                  <pre>{String(d.args.newText ?? "")}</pre>
                </>
              ) : (
                <pre>
                  {typeof d.args.content === "string"
                    ? d.args.content
                    : JSON.stringify(d.args, null, 2)}
                </pre>
              )}
            </details>
            <div className="actions">
              <button
                disabled={c.approvalBusy}
                onClick={() => c.approveCall(call)}
              >
                允许这次操作
              </button>
              <button
                className="secondary"
                disabled={c.approvalBusy}
                onClick={() => c.denyCall(call)}
              >
                拒绝
              </button>
            </div>
          </section>
        );
      })}
      {c.unknownCalls.map((call) => {
        const d = describeCall(call);
        return (
          <section className="approval-card uncertain" key={call.id}>
            <strong>需要核对：{d.title}是否已经完成？</strong>
            <p className="operation-target">{d.target}</p>
            <p>
              运行被中断，IceTudio
              无法确认操作结果。请检查文件或目标服务；无法核实可以暂时保留，不会自动重复执行。
            </p>
            {deferred.includes(call.id) && (
              <p>已保留待核对。完成核对前，这个任务会保持暂停。</p>
            )}
            <div className="actions">
              <button
                disabled={c.approvalBusy}
                onClick={() => c.resolveUnknown(call, true)}
              >
                已核实完成
              </button>
              <button
                className="secondary"
                disabled={c.approvalBusy}
                onClick={() => c.resolveUnknown(call, false)}
              >
                已核实未完成
              </button>
              <button
                className="secondary"
                onClick={() => setDeferred((v) => [...v, call.id])}
              >
                暂不确定
              </button>
            </div>
            <details>
              <summary>技术详情</summary>
              <pre>{call.error}</pre>
            </details>
          </section>
        );
      })}
    </>
  );
}
