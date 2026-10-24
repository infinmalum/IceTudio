import { useEffect, useState } from "react";
export type Theme = "light" | "dark";
const KEY = "icetudio.theme";
function storedTheme(): Theme | "" {
  const saved = localStorage.getItem(KEY);
  return saved === "light" || saved === "dark" ? saved : "";
}
export function useTheme() {
  // 深色是默认 DAW 主题（P2 工作区契约 §2）；浅色保留为用户可选项，不再跟随系统。
  const [theme, setTheme] = useState<Theme>(() => storedTheme() || "dark");
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  const toggle = () => {
    const next: Theme = theme === "dark" ? "light" : "dark";
    try {
      localStorage.setItem(KEY, next);
    } catch {}
    setTheme(next);
  };
  return { theme, toggle };
}
