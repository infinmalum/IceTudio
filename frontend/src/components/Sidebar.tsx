import type { Workspace } from "../hooks/useWorkspace";
import { useTheme } from "../shared/theme";
type Page = "workspace" | "assistants" | "settings";
export function Sidebar({
  workspace: w,
  page,
  setPage,
  createSession,
}: {
  workspace: Workspace;
  page: Page;
  setPage: (p: Page) => void;
  createSession: () => Promise<void>;
}) {
  const { theme, toggle } = useTheme();
  return (
    <aside className="sidebar">
      <a
        className="brand"
        href="#"
        onClick={(e) => {
          e.preventDefault();
          setPage("workspace");
        }}
      >
        <span>✳</span> IceTudio
      </a>
      <button
        className="new-session"
        onClick={() => void createSession()}
        disabled={w.creating}
      >
        {w.creating ? "正在创建…" : "＋ 新对话"}
      </button>
      <label className="conversation-search">
        <span className="sr-only">搜索对话</span>
        <input
          placeholder="搜索对话"
          value={w.query}
          onChange={(e) => w.setQuery(e.target.value)}
        />
      </label>
      <nav className="session-navigation" aria-label="对话列表">
        {w.sessions
          .filter(
            (s) =>
              s.kind === "normal" &&
              s.title.toLowerCase().includes(w.query.trim().toLowerCase()),
          )
          .map((s) => (
            <div
              className={`session-row ${page === "workspace" && w.selected === s.id ? "selected" : ""}`}
              key={s.id}
            >
              <button
                className="session-link"
                onClick={() => {
                  w.openConversation(s);
                  setPage("workspace");
                }}
                title={s.title}
              >
                {s.title}
              </button>
              <details className="conversation-menu">
                <summary aria-label={`管理对话 ${s.title}`}>⋯</summary>
                <div>
                  <button
                    onClick={(e) => {
                      e.currentTarget
                        .closest("details")
                        ?.removeAttribute("open");
                      void w.renameConversation(s);
                    }}
                  >
                    重命名
                  </button>
                  <button
                    onClick={(e) => {
                      e.currentTarget
                        .closest("details")
                        ?.removeAttribute("open");
                      void w.deleteConversation(s);
                    }}
                  >
                    删除对话
                  </button>
                </div>
              </details>
            </div>
          ))}
        {w.query &&
          !w.sessions.some(
            (s) =>
              s.kind === "normal" &&
              s.title.toLowerCase().includes(w.query.trim().toLowerCase()),
          ) && <p className="hint">没有找到匹配的对话</p>}
      </nav>
      <div className="sidebar-bottom">
        <button
          className={page === "workspace" ? "selected" : ""}
          onClick={() => setPage("workspace")}
        >
          ♬ 工程
        </button>
        <button
          className={page === "assistants" ? "selected" : ""}
          onClick={() => setPage("assistants")}
        >
          ◇ 助手
        </button>
        <button
          className={page === "settings" ? "selected" : ""}
          onClick={() => setPage("settings")}
        >
          ⚙ 设置
        </button>
        <button
          className="theme-toggle"
          onClick={toggle}
          aria-label={theme === "dark" ? "切换到浅色主题" : "切换到深色主题"}
          title={theme === "dark" ? "切换到浅色主题" : "切换到深色主题"}
        >
          {theme === "dark" ? "☀ 浅色模式" : "🌙 深色模式"}
        </button>
      </div>
    </aside>
  );
}
