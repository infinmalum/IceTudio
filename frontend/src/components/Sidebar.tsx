import type { CSSProperties } from "react";
import { useTheme } from "../shared/theme";

type Page = "workspace" | "assistants" | "settings";

// 左侧导航栏：只负责页面切换与主题。工程管理已迁入工作区左侧浏览器
// （P2 工作区契约 §1.2），设置与开发者工具不进入创作界面（§1.6）。
export function Sidebar({
  page,
  setPage,
  style,
}: {
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
      <nav className="sidebar-nav" aria-label="页面">
        <button
          className={page === "workspace" ? "selected" : ""}
          onClick={() => setPage("workspace")}
        >
          ♪ 工程工作区
        </button>
        <button
          className={page === "assistants" ? "selected" : ""}
          onClick={() => setPage("assistants")}
        >
          ✎ 创作助手
        </button>
        <button
          className={page === "settings" ? "selected" : ""}
          onClick={() => setPage("settings")}
        >
          ⚙ 设置
        </button>
      </nav>
      <div className="sidebar-bottom">
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
