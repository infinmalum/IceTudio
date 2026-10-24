// 即时试听前端入口（P2 工作区契约 §7）：绕过 timeline，不写工程。
// 失败静默（引擎级问题由 Transport 状态行呈现）；音符到时自动结束。
import { PlaybackService } from "../../bindings/icetudio";

export async function auditionInstrument(program: number, channel: number, keys: number[], velocity = 100, durationMs = 500): Promise<void> {
  const bank = channel === 9 ? 128 : 0;
  try {
    await PlaybackService.Audition({ program, bank, keys, velocity, durationMs });
  } catch {
    // 试听是瞬态操作：引擎不可用时由播放状态行说明，不弹窗打断编辑。
  }
}

export function stopAudition(): void {
  void PlaybackService.StopAudition().catch(() => {});
}
