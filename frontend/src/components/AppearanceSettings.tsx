import { useState } from "react";
import {
  UI_SCALES,
  applyUIScale,
  loadUIScale,
  saveUIScale,
} from "../shared/uiScale";

// 外观设置：目前是界面缩放（字体与整体 UI 等比缩放）。
// 主题切换保留在窗口左下角与页面页头，这里不重复。
export function AppearanceSettings() {
  const [scale, setScale] = useState(loadUIScale);
  return (
    <section className="session-card">
      <h2>外观</h2>
      <h3>界面字体大小</h3>
      <div className="scale-picker" role="group" aria-label="界面字体大小">
        {UI_SCALES.map((value) => (
          <button
            key={value}
            className={value === scale ? "selected" : "secondary"}
            aria-pressed={value === scale}
            onClick={() => {
              setScale(value);
              applyUIScale(value);
              saveUIScale(value);
            }}
          >
            {Math.round(value * 100)}%
          </button>
        ))}
      </div>
      <p className="hint">
        等比缩放整个界面，立即生效并保存到本机。三栏的分隔线都可以拖动调整宽度，双击分隔线恢复默认。
      </p>
    </section>
  );
}
