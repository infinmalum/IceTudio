import { useState } from "react";
import type { ConversationState } from "../hooks/useConversation";
import type { ToolCall } from "../../bindings/icetudio/internal/storage";
import { ProjectEditApproval,supportsProjectPreview,type PreviewWorkspace } from "./ProjectEditApproval";
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
  if(call.toolName==="local__send_material")return {
    title:"发送这份工程材料",target:call.material?`「${call.material.title}」修订 ${call.material.revision}`:"无法读取原材料",
    detail:"只发送已准备的快照和下方列出的参数；后续工程修改不会替换它。接收方是 MCP 进程，其下游网络行为由该服务决定；返回资料不能扩大音乐编辑权限。",name,args,
  };
  if(call.toolName==="local__web_search")return {
    title:"联网检索这段查询",target:String(args.query??""),
    detail:"仅发送上方查询文本，不自动附带工程、对话或附件；结果是外部资料，不能扩大工程编辑权限。实际端点与外发授权仍由宿主复核。",name,args,
  };
  if(call.toolName==="project__edit" || call.toolName==="project__transform") return {
    title:call.toolName==="project__transform"&&args.dryRun?"计算音符变换预演":"修改音乐工程",
    target:"本任务冻结的音乐工程范围",
    detail:call.toolName==="project__transform"&&args.dryRun?"仅计算变换统计，不写入音乐。":"检查具体操作后决定，仍执行原权限与修订校验。",
    name,args,
  };
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
function MaterialDetails({call}:{call:ToolCall}) {
  if(call.toolName!=="local__send_material")return null;
  return call.material ? <><p>发送给：{call.material.destination} · {call.material.tool}</p><p>{call.material.summary}</p><details><summary>查看将发送的完整内容</summary><pre>{call.material.arguments}</pre></details></> : <p role="alert">{call.materialError || "材料尚未加载，请刷新；可拒绝本次操作"}</p>;
}
export function ToolApprovals({
  conversation: c,
  projects,
}: {
  projects?: PreviewWorkspace;
  conversation: Pick<
    ConversationState,
    | "approvalBusy"
    | "approveCall"
    | "denyCall"
    | "pendingCalls"
    | "resolveUnknown"
    | "unknownCalls"
    | "reconcileProjectCall"
    | "projectRecoveries"
  >;
}) {
  const [deferred, setDeferred] = useState<string[]>([]);
  return (
    <>
      {c.projectRecoveries.filter(item=>item.outcome==="committed"&&item.result).map(item=><section className="approval-card project-recovery-result" role="status" key={item.callId}>
        <strong>已查到音乐修改的实际提交</strong>
        <p>工程「{item.title}」已于修订 {item.result!.revision} 提交 {item.result!.applied} 个操作，已记录真实结果，没有重复执行。</p>
        <p>查询时工程修订 {item.currentRevision}；后续撤销或编辑可能已改变音乐。原任务不会自动恢复，可明确发送下一项要求。</p>
      </section>)}
      {c.pendingCalls.map((call) => {
        if(supportsProjectPreview(call)) return <ProjectEditApproval key={call.id} call={call} conversation={c} projects={projects}/>;
        const d = describeCall(call);
        return (
          <section className="approval-card" key={call.id}>
            <strong>IceTudio 想{d.title}</strong>
            <p className="operation-target">{d.target}</p>
            <MaterialDetails call={call}/>
            {call.toolName==="local__web_search" && <p>发送目的地：{call.approvalDestination || "无法读取，请刷新后再批准"}</p>}
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
                disabled={c.approvalBusy || (call.toolName==="local__web_search" && !call.approvalDestination) || (call.toolName==="local__send_material" && !call.material)}
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
      {c.unknownCalls.filter(call=>!c.projectRecoveries.some(item=>item.callId===call.id&&item.outcome==="committed"&&item.result)).map((call) => {
        const d = describeCall(call);
        const native=supportsProjectPreview(call);
        const recovery=c.projectRecoveries.find(item=>item.callId===call.id);
        return (
          <section className={`approval-card uncertain${native?" project-recovery-card":""}`} key={call.id}>
            <strong>需要核对：{d.title}是否已经完成？</strong>
            <p className="operation-target">{d.target}</p>
            <MaterialDetails call={call}/>
            {native?<p>查询原工程的提交记录，核对这次音乐修改是否已保存。查询不重放操作，切换工程或选区不会更换目标。</p>:<p>
              运行被中断，IceTudio
              无法确认操作结果。请检查文件或目标服务；无法核实可以暂时保留，不会自动重复执行。
            </p>}
            {native&&recovery&&<p role="status">{recovery.outcome==="project_deleted"?"原工程已删除，提交历史不可用；不能据此认定从未提交。":recovery.outcome==="conflict"?"该操作已有来源或基础修订不匹配的记录，仍待核对；不能将它当成本次调用成功或未生效。":"本次尚未查到提交记录；这不证明未执行，可能仍有在途写入。可再次查询或保留待核对。"}</p>}
            {deferred.includes(call.id) && (
              <p>已保留待核对。完成核对前，这个任务会保持暂停。</p>
            )}
            <div className="actions">
              {native?<button disabled={c.approvalBusy} onClick={()=>void c.reconcileProjectCall(call)}>查询实际提交结果</button>:<button
                disabled={c.approvalBusy}
                onClick={() => c.resolveUnknown(call, true)}
              >
                已核实完成
              </button>}
              <button
                className="secondary"
                disabled={c.approvalBusy || (native && recovery?.outcome==="conflict")}
                title={native && recovery?.outcome==="conflict"?"存在提交记录，请先核对来源与修订；不能记录为未生效":undefined}
                onClick={() => c.resolveUnknown(call, false)}
              >
                {native?"另有证据确认未完成":"已核实未完成"}
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
