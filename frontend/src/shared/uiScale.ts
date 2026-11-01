// 界面缩放：作用于根元素的 CSS zoom，等比缩放整个应用（含画布与对话框）。
// 用预设档位避免连续拖动产生的非整数布局；立即生效并持久化到本机。
const KEY = "icetudio.ui-scale";
export const UI_SCALES = [0.9, 1, 1.1, 1.25, 1.4];
export function loadUIScale(): number {
  try {
    const raw = localStorage.getItem(KEY);
    const value = raw ? Number.parseFloat(raw) : 1;
    return UI_SCALES.includes(value) ? value : 1;
  } catch {
    return 1;
  }
}
export function applyUIScale(scale: number) {
  document.documentElement.style.zoom = scale === 1 ? "" : String(scale);
}
export function saveUIScale(scale: number) {
  try {
    localStorage.setItem(KEY, String(scale));
  } catch {
    // 存储不可用时只对当前会话生效。
  }
}
