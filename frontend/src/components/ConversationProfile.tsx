import type { Workspace } from "../hooks/useWorkspace";
import type { Configs } from "../hooks/useConfigs";
import type { ConversationState } from "../hooks/useConversation";
export function ConversationProfile({
  workspace: w,
  configState: p,
  conversation: c,
  openAssistants,
}: {
  workspace: Workspace;
  configState: Configs;
  conversation: ConversationState;
  openAssistants: () => void;
}) {
  return w.activeConversation ? (
    <label className="assistant-selector">
      <span className="sr-only">当前对话的助手</span>
      <select
        aria-label="当前助手"
        value={
          w.activeConversation.configArchived
            ? ""
            : w.activeConversation.configId
        }
        disabled={
          c.configSwitchBusy ||
          c.sending ||
          w.activeConversation.kind === "delegation"
        }
        title="切换助手用于下一条消息，正在进行的工作继续使用原助手"
        onChange={(e) =>
          e.target.value === "__manage__"
            ? openAssistants()
            : c.selectConversationConfig(e.target.value)
        }
      >
        <option value="" disabled>
          请选择助手
        </option>
        {p.configs.map((v) => (
          <option key={v.id} value={v.id}>
            {v.name}
          </option>
        ))}
        <option value="__manage__">添加或管理助手…</option>
      </select>
    </label>
  ) : (
    <span>日常助手</span>
  );
}
