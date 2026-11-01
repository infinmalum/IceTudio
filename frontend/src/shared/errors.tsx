import { Alert } from "antd";

export function friendlyError(error: unknown) {
  const raw = String(error);
  if (/数据库|存储|sqlite|数据初始化/i.test(raw))
    return "无法打开本机数据。请查看技术详情并保留原数据目录，避免丢失记录。";
  if (/根任务开始时|新增授权用于新任务/.test(raw))
    return "补充内容超出了原任务的发送授权。请开始新任务并重新确认授权，或使用原先已允许的资料。";
  if (/未授权|外发|授权范围|grant/i.test(raw))
    return "需要确认访问或发送权限。请检查此对话的权限后再试。";
  if (/401|API.?key|authentication|密钥|凭据/i.test(raw))
    return "模型密钥无效或尚未填写，请在模型设置中检查。";
  if (/429|quota|rate.?limit|额度/i.test(raw))
    return "模型服务当前额度不足或请求太频繁，请稍后重试或更换模型。";
  if (/fetch|connect|network|timeout|连接|超时/i.test(raw))
    return "暂时无法连接服务。请检查地址和网络后重试。";
  if (/预算|耗尽/i.test(raw))
    return "本次任务已达到使用上限。你可以查看已完成的内容或开始新任务。";
  if (/修订|冲突|changed|conflict/i.test(raw))
    return "内容已发生变化，请刷新并核对后再保存。";
  if (
    /附件|文件|字节|MiB|格式|阶段|先|请|找不到|不存在|不能|未启用|尚未/i.test(
      raw,
    ) &&
    !raw.includes("\n")
  )
    return raw
      .replace(/^Error:\s*/, "")
      .replace(/Profile/g, "助手")
      .replace(/Context/g, "对话");
  // Not a recognized pattern: quote the backend's own first line so the
  // actual reason is visible, and give a concrete next step. The full text
  // stays available under 技术详情.
  const detail = raw
    .replace(/^Error:\s*/, "")
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)[0];
  if (!detail) return "操作暂时没有完成，请重试；仍失败时可重启应用后再试。";
  const short = detail.length > 160 ? `${detail.slice(0, 160)}…` : detail;
  return `操作没有完成：${short}。可以重试一次；如果反复出现，请重启应用，并查看下方技术详情了解完整原因。`;
}
export function ErrorNotice({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <Alert
      className="error-notice"
      role="alert"
      type="error"
      showIcon
      message={friendlyError(error)}
      description={
        <details>
          <summary>技术详情</summary>
          <pre>{String(error)}</pre>
        </details>
      }
    />
  );
}
export const stateLabel = (state: string) =>
  ({
    running: "正在处理",
    pending: "尚未开始",
    succeeded: "已处理",
    completed: "已完成",
    failed: "未完成",
    cancelled: "已停止",
    interrupted: "已中断",
    waiting: "等待补充",
    blocked: "需要处理",
    execution_unknown: "等待核对",
    planning: "正在安排",
    complete: "已完成",
    passed: "检查通过",
    changes_requested: "需要修改",
    inconclusive: "暂无法确认",
    draft: "待采用",
    applied: "已采用",
    discarded: "已丢弃",
    withdrawn: "已撤销",
    // Unknown states fall back to the raw token instead of a misleading
    // "处理中": a status this build does not know is not evidence of progress.
  })[state] ?? state;
