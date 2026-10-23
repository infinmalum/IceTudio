import { useState } from "react";
import { StatusService } from "../../bindings/icetudio";
import type { MCPServer } from "../../bindings/icetudio/internal/config";
import type { MCPEndpointStatus } from "../../bindings/icetudio/internal/app";
import type { RuntimeSettings } from "../hooks/useRuntimeSettings";
import { ErrorNotice } from "../shared/errors";
import { useDialog } from "../shared/dialogs";
type ServerDraft = {
  name: string;
  command: string;
  args: string;
  workingDir: string;
  env: string;
  webSearchTools: string;
  timeoutSeconds: string;
};
const emptyDraft: ServerDraft = {
  name: "",
  command: "",
  args: "",
  workingDir: "",
  env: "",
  webSearchTools: "",
  timeoutSeconds: "",
};
const lines = (value: string) =>
  value
    .split("\n")
    .map((item) => item.trim())
    .filter(Boolean);
function shortHash(hash: string) {
  return hash ? hash.slice(0, 12) + "…" : "—";
}
export function MCPServersPanel({
  servers,
  runtime: r,
  onChanged,
}: {
  servers: MCPServer[];
  runtime: RuntimeSettings;
  onChanged: () => Promise<void>;
}) {
  const dialog = useDialog();
  const [draft, setDraft] = useState<ServerDraft>(emptyDraft);
  const [editing, setEditing] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [savedNote, setSavedNote] = useState("");
  const endpoints: MCPEndpointStatus[] = r.mcpServers;
  function startEdit(server: MCPServer) {
    setEditing(server.name);
    setDraft({
      name: server.name,
      command: server.command,
      args: (server.args ?? []).join("\n"),
      workingDir: server.workingDir ?? "",
      env: (server.env ?? []).join("\n"),
      webSearchTools: (server.webSearchTools ?? []).join("\n"),
      timeoutSeconds: server.timeoutSeconds ? String(server.timeoutSeconds) : "",
    });
    setSavedNote("");
  }
  function startAdd() {
    setEditing("new");
    setDraft(emptyDraft);
    setSavedNote("");
  }
  function cancelEdit() {
    setEditing("");
    setDraft(emptyDraft);
    setSavedNote("");
  }
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!draft.name.trim() || !draft.command.trim()) return;
    setBusy(true);
    setSavedNote("");
    try {
      const timeout = Number.parseInt(draft.timeoutSeconds, 10);
      const server: MCPServer = {
        name: draft.name.trim(),
        command: draft.command.trim(),
        args: lines(draft.args),
        workingDir: draft.workingDir.trim(),
        env: lines(draft.env),
        webSearchTools: lines(draft.webSearchTools),
        timeoutSeconds: Number.isFinite(timeout) && timeout > 0 ? timeout : 0,
      };
      await StatusService.SaveMCPServer(server);
      await Promise.all([r.refreshEndpoints(), onChanged()]);
      setEditing("");
      setDraft(emptyDraft);
      setSavedNote("已保存并连接；助手里现在可以选择这个服务。");
      setError("");
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }
  async function remove(server: MCPServer) {
    const live = endpoints.find((item) => item.name === server.name);
    if (
      !(await dialog.confirm({
        title: `移除 MCP 服务「${server.name}」？`,
        body: live?.tools?.length
          ? `它的 ${live.tools.length} 个工具会停止，正在使用它的助手将无法再调用；配置文件中的条目一并删除。`
          : "配置文件中的条目会删除，助手将无法再使用它的工具。",
        confirm: "移除服务",
        danger: true,
      }))
    )
      return;
    setBusy(true);
    try {
      await StatusService.DeleteMCPServer(server.name);
      await Promise.all([r.refreshEndpoints(), onChanged()]);
      if (editing === server.name) cancelEdit();
      setSavedNote("");
      setError("");
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }
  async function reconfirm(name: string) {
    const live = endpoints.find((item) => item.name === name);
    if (
      !(await dialog.confirm({
        title: `重新信任「${name}」？`,
        body: (
          <>
            <p>
              服务程序指纹 {shortHash(live?.binarySha256 ?? "")}，工具结构指纹{" "}
              {shortHash(live?.schemaSha256 ?? "")}。
            </p>
            <p>
              确认后，基于这次指纹的自动执行会恢复；如果这不是你预期的变化，请先移除该服务。
            </p>
          </>
        ),
        confirm: "确认并恢复自动执行",
      }))
    )
      return;
    setBusy(true);
    try {
      await StatusService.ConfirmMCPEndpoint(name);
      await r.refreshEndpoints();
      setError("");
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="session-card" aria-label="MCP 服务管理">
      <h2>MCP 服务</h2>
      <p className="hint">
        这里接入本机的 MCP 服务进程；助手编辑里只能选择已接入的服务，不能安装。
      </p>
      {servers.length === 0 && <p>尚未接入任何 MCP 服务。</p>}
      {servers.map((server) => {
        const live = endpoints.find((item) => item.name === server.name);
        const searchTools = server.webSearchTools ?? [];
        return (
          <div className="mcp-server-row" key={server.name}>
            <div>
              <strong>{server.name}</strong>{" "}
              <span className="memory-meta">
                {server.command} {(server.args ?? []).join(" ")}
              </span>
            </div>
            {live?.changed ? (
              <p className="error-notice" role="alert">
                服务程序或工具结构与上次登记不一致，相关工具已降级为逐条审批。
                <button
                  type="button"
                  className="text-button"
                  disabled={busy}
                  onClick={() => reconfirm(server.name)}
                >
                  查看并重新信任
                </button>
              </p>
            ) : null}
            <p className="memory-meta">
              {live?.tools?.length
                ? `已连接 · ${live.tools.length} 个工具${live.version ? ` · ${live.version}` : ""}`
                : "未连接；下次任务使用时会自动启动"}
              {(server.webSearchTools ?? []).length
                ? ` · 搜索工具：${searchTools.join("、")}`
                : ""}
            </p>
            <div className="model-actions">
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => startEdit(server)}
              >
                编辑
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => remove(server)}
              >
                移除
              </button>
            </div>
          </div>
        );
      })}
      {editing && (
        <form className="model-form" onSubmit={save}>
          <h3>{editing === "new" ? "接入 MCP 服务" : `编辑 ${editing}`}</h3>
          <label>
            服务名称
            <input
              required
              value={draft.name}
              disabled={busy || editing !== "new"}
              onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              placeholder="例如 filesystem"
            />
            {editing !== "new" && (
              <small>名称是服务的唯一标识；改名请移除后重新接入。</small>
            )}
          </label>
          <label>
            启动命令
            <input
              required
              value={draft.command}
              disabled={busy}
              onChange={(e) => setDraft({ ...draft, command: e.target.value })}
              placeholder="例如 npx 或 /usr/bin/mcp-server"
            />
            <small>按 PATH 解析后以独立进程启动，不经过 shell。</small>
          </label>
          <label>
            启动参数（每行一个）
            <textarea
              rows={2}
              value={draft.args}
              disabled={busy}
              onChange={(e) => setDraft({ ...draft, args: e.target.value })}
            />
          </label>
          <label>
            工作目录（可选）
            <input
              value={draft.workingDir}
              disabled={busy}
              onChange={(e) =>
                setDraft({ ...draft, workingDir: e.target.value })
              }
            />
          </label>
          <label>
            继承的环境变量名（每行一个，可选）
            <textarea
              rows={2}
              value={draft.env}
              disabled={busy}
              onChange={(e) => setDraft({ ...draft, env: e.target.value })}
            />
            <small>
              只继承列出的变量名，值在本机读取、不会保存；留空仅传 PATH 和 HOME。
            </small>
          </label>
          <label>
            搜索工具名（每行一个，可选）
            <textarea
              rows={2}
              value={draft.webSearchTools}
              disabled={busy}
              onChange={(e) =>
                setDraft({ ...draft, webSearchTools: e.target.value })
              }
            />
            <small>注册为网页搜索工具后，对话的联网开关会使用它。</small>
          </label>
          <label>
            连接超时秒数（可选，默认 10）
            <input
              type="number"
              min={1}
              value={draft.timeoutSeconds}
              disabled={busy}
              onChange={(e) =>
                setDraft({ ...draft, timeoutSeconds: e.target.value })
              }
            />
          </label>
          <div className="actions">
            <button disabled={busy}>
              {busy ? "正在连接…" : "保存并连接"}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={cancelEdit}
            >
              取消
            </button>
          </div>
          <p className="hint">
            保存会立即启动该服务验证配置；连接失败会显示原因，配置仍会保留供修正。
          </p>
        </form>
      )}
      {!editing && (
        <button type="button" onClick={startAdd} disabled={busy}>
          ＋ 接入 MCP 服务
        </button>
      )}
      {savedNote && (
        <p className="success" role="status">
          {savedNote}
        </p>
      )}
      <ErrorNotice error={error || r.error} />
    </section>
  );
}
