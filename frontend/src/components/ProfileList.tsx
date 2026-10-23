import type { Configs } from "../hooks/useConfigs";
export function ProfileList({
  configState,
  startConversation,
}: {
  configState: Configs;
  startConversation: (id: string) => void;
}) {
  const p = configState;
  return (
    <section className="session-card" aria-label="你的助手">
      <h2>你的助手</h2>
      {p.configs.length === 0 && (
        <p className="hint">还没有自己的助手；可以在下方创建。</p>
      )}
      <div className="assistant-list">
        {p.configs.map((item) => (
          <div
            className={`assistant-row ${p.configEditID === item.id ? "selected" : ""}`}
            key={item.id}
          >
            <div className="assistant-row-main">
              <strong>
                {item.name}
                {item.isDefault && <span className="memory-meta">（默认）</span>}
              </strong>
              <span className="memory-meta">
                {item.data.description || "暂无说明"}
              </span>
              <span className="memory-meta">
                技能：
                {item.data.allSkills
                  ? "全部"
                  : `${item.data.enabledSkills?.length ?? 0} 个`}{" "}
                · MCP：
                {item.data.allMcpServers
                  ? "全部"
                  : `${item.data.enabledMcpServers?.length ?? 0} 个`}
              </span>
            </div>
            <div className="assistant-row-actions">
              <button
                type="button"
                className="text-button"
                disabled={p.configBusy}
                onClick={() => startConversation(item.id)}
              >
                开始新对话
              </button>
              <button
                type="button"
                className="text-button"
                disabled={p.configBusy}
                onClick={() => {
                  void p.setConfigEditID(item.id).then((ok) => {
                    if (ok)
                      document
                        .getElementById("assistant-editor")
                        ?.scrollIntoView({ block: "start", behavior: "smooth" });
                  });
                }}
              >
                编辑
              </button>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
