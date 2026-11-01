import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
export type Theme = "light" | "dark";
const KEY = "icetudio.theme";
function storedTheme(): Theme | "" {
  const saved = localStorage.getItem(KEY);
  return saved === "light" || saved === "dark" ? saved : "";
}
type ThemeState = { theme: Theme; toggle: () => void };
const ThemeContext = createContext<ThemeState>({
  theme: "dark",
  toggle: () => {},
});
// 主题在 Provider 里只存一份：浏览器栏、设置页和 antd ConfigProvider
// 必须显示同一个明暗状态，不能再各建一份 state。
export function ThemeProvider({ children }: { children: ReactNode }) {
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
  return (
    <ThemeContext.Provider value={{ theme, toggle }}>
      {children}
    </ThemeContext.Provider>
  );
}
export function useTheme() {
  return useContext(ThemeContext);
}
