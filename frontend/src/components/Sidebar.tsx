import { useTheme } from "../shared/theme";
import type { CSSProperties } from "react";
import type { useProjects } from "../hooks/useProjects";

type Page = "workspace" | "assistants" | "settings";

// The left rail owns projects; conversations and agent settings live in the
// right dock so the center stays editor-only.
export function Sidebar({
  projects: p,
  page,
  setPage,
  style,
}: {
  projects: ReturnType<typeof useProjects>;
  page: Page;
  setPage: (p: Page) => void;
  style?: CSSProperties;
}) {
  const { theme, toggle } = useTheme();
  return (
    <aside className="sidebar" style={style}>
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
      <button className="new-project" onClick={p.create} disabled={p.busy}>
        ＋ 新建工程
      </button>
      <button className="secondary" onClick={p.importMIDI} disabled={p.busy}>
        导入 MIDI
      </button>
      {p.error ? (
        <p className="project-error" role="alert">
          {p.error}
        </p>
      ) : null}
      <ul className="project-list" aria-label="工程列表">
        {p.projects.length === 0 ? (
          <li className="project-list-empty">还没有工程</li>
        ) : (
          p.projects.map((item) => (
            <li
              key={item.id}
              className={item.id === p.activeID ? "active" : ""}
            >
              <button
                className="project-select"
                onClick={() => p.select(item.id)}
                title={item.id}
              >
                <span>{item.title}</span>
                <span className="project-meta">
                  修订 {item.revision} · PPQ {item.ppq}
                </span>
              </button>
              <button
                className="project-remove"
                onClick={() => p.remove(item.id)}
                disabled={p.busy}
                title={`删除 ${item.title}`}
              >
                删除
              </button>
            </li>
          ))
        )}
      </ul>
      <div className="sidebar-bottom">
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
