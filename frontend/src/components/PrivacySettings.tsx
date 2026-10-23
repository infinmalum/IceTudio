import type { LocalContextState } from "../hooks/useLocalContext";
import type { Workspace } from "../hooks/useWorkspace";
import type { SearchAvailability } from "../../bindings/icetudio/internal/app";
import { ErrorNotice } from "../shared/errors";
export function PrivacySettings({
  local: l,
  workspace: w,
  searchAvailability,
}: {
  local: LocalContextState;
  workspace: Workspace;
  searchAvailability: SearchAvailability | null;
}) {
  return (
    <section className="session-card">
      <h2>当前对话的权限</h2>
      {!w.selected ? (
        <p>开始对话后，可以在这里管理它的权限。</p>
      ) : (
        <>
          <p>{w.activeConversation?.title}</p>
          {l.contextDraft && (
            <form className="model-form" onSubmit={l.saveContext}>
              <label>
                操作确认方式
                <select
                  value={l.contextDraft.policy.approvalMode}
                  onChange={(e) =>
                    l.updatePolicy({ approvalMode: e.target.value })
                  }
                >
                  <option value="semi">推荐：重要操作先询问</option>
                  <option value="manual">每次操作都询问</option>
                  {l.contextDraft.policy.approvalMode === "automatic" && (
                    <option value="automatic">自定义自动执行策略</option>
                  )}
                </select>
                <small>
                  文件修改、命令与敏感操作继续受本机权限和逐次检查限制。
                </small>
              </label>
              <p>
                已允许的文件夹：
                {l.contextDraft.policy.permissions.workspacePaths?.join("、") ||
                  "尚未选择。可在聊天输入框旁选择文件夹。"}
              </p>
              {l.contextDirty && (
                <p className="unsaved-note" role="status">
                  有未保存的修改。切换对话后会保留草稿，回来可继续编辑。
                  <button
                    type="button"
                    className="text-button"
                    onClick={l.discardContextEdits}
                  >
                    放弃修改
                  </button>
                </p>
              )}
              <button disabled={l.contextBusy}>保存偏好</button>
            </form>
          )}
          <h3>联网检索</h3>
          {!searchAvailability ? (
            <p>打开一个对话后，这里显示它的联网检索方式。</p>
          ) : searchAvailability.available ? (
            <p>
              可用（
              {searchAvailability.route === "native"
                ? "模型原生搜索"
                : "通过已接入的搜索 MCP 服务"}
              ）：{searchAvailability.reason}
            </p>
          ) : (
            <p>当前不可用：{searchAvailability.reason}</p>
          )}
          <h3>内容发送授权</h3>
          <p>发送到远端模型前会说明接收地址。你可以随时撤销。</p>
          {!l.grants.length && <p>尚未允许向远端发送此对话。</p>}
          {l.grants.map((g) => (
            <div className="permission-row" key={g.destination}>
              <span>{g.destination}</span>
              <button
                className="secondary"
                disabled={l.grantBusy}
                onClick={() => l.revokeGrant(g.destination)}
              >
                撤销
              </button>
            </div>
          ))}
          <ErrorNotice error={l.contextError || l.grantError} />
        </>
      )}
    </section>
  );
}
