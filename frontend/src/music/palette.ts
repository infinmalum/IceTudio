// Track/Clip 颜色调色板（P1 §11 契约）。
// ID 列表必须与 internal/project/model.go 的 Palette 保持一致：ice mint violet amber coral rose slate moss。
// 空字符串 = 未设置，显示层按轨道序回退（模型不自动分配）。

export interface TrackColor {
  readonly id: string;
  readonly zh: string;
  readonly css: string;
}

export const TRACK_COLORS: readonly TrackColor[] = [
  { id: "ice", zh: "冰蓝", css: "#5ec8e8" },
  { id: "mint", zh: "薄荷", css: "#6fd6a3" },
  { id: "violet", zh: "紫罗兰", css: "#a78bfa" },
  { id: "amber", zh: "琥珀", css: "#f2b544" },
  { id: "coral", zh: "珊瑚", css: "#f28c6b" },
  { id: "rose", zh: "玫红", css: "#ec7bae" },
  { id: "slate", zh: "青灰", css: "#8fa3b8" },
  { id: "moss", zh: "苔绿", css: "#9db85c" },
];

export function colorById(id: string): TrackColor | undefined {
  return TRACK_COLORS.find(c => c.id === id);
}

// 显示色：显式设置用设置值；未设置按轨道序回退（当前产品没有轨道重排操作，序号稳定）。
export function displayColor(explicit: string | undefined, trackIndex: number): TrackColor {
  return colorById(explicit ?? "") ?? TRACK_COLORS[trackIndex % TRACK_COLORS.length];
}
