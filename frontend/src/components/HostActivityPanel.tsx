import { useState } from "react";
import { StatusService } from "../../bindings/icetudio";
import type { ActivityEntry } from "../hooks/useApp";
import { ErrorNotice } from "../shared/errors";
const activityLabels: Record<string, string> = {
  "run.started": "任务开始",
  "run.updated": "任务更新",
  "stream.delta": "输出中",
  "conversation.updated": "对话更新",
  "message.appended": "消息追加",
};
export function HostActivityPanel({
  activity,
}: {
  activity: ActivityEntry[];
}) {
  const [logs, setLogs] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function loadLogs() {
    setBusy(true);
    try {
      setLogs((await StatusService.ReadHostLogs(200)) ?? []);
      setError("");
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <details>
        <summary>宿主活动（最近 {activity.length} 条）</summary>
        {activity.length === 0 ? (
          <p>暂无事件。发起一次任务后，这里会显示宿主正在发生什么。</p>
        ) : (
          <ul className="activity-list">
            {activity.slice(0, 60).map((entry, index) => (
              <li key={entry.at + "-" + index}>
                <span className="memory-meta">
                  {new Date(entry.at).toLocaleTimeString()} ·{" "}
                  {activityLabels[entry.type] ?? entry.type}
                  {entry.sessionId ? ` · 会话 ${entry.sessionId.slice(0, 8)}` : ""}
                </span>
                {entry.detail && <span> {entry.detail}</span>}
              </li>
            ))}
          </ul>
        )}
      </details>
      <details>
        <summary>宿主日志</summary>
        <p className="hint">
          与命令行 `icetudio logs` 相同的本机日志，用于排查启动或连接问题。
        </p>
        <button type="button" className="secondary" disabled={busy} onClick={loadLogs}>
          {busy ? "正在读取…" : "读取最近 200 行"}
        </button>
        {logs.length > 0 && (
          <pre className="host-log">{logs.join("\n")}</pre>
        )}
        <ErrorNotice error={error} />
      </details>
    </>
  );
}
