import { useEffect, useState } from "react";
export type Theme = "light" | "dark";
const KEY = "icetudio.theme";
function systemTheme(): Theme {
  return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
function storedTheme(): Theme | "" {
  const saved = localStorage.getItem(KEY);
  return saved === "light" || saved === "dark" ? saved : "";
}
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(
    () => storedTheme() || systemTheme(),
  );
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    const mq = matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      if (!storedTheme()) setTheme(systemTheme());
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  const toggle = () => {
    const next: Theme = theme === "dark" ? "light" : "dark";
    try {
      localStorage.setItem(KEY, next);
    } catch {}
    setTheme(next);
  };
  return { theme, toggle };
}
