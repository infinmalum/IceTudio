import { useCallback, useEffect, useState } from "react";
import { PlaybackService } from "../../bindings/icetudio";
import type { FontList, FontState } from "../../bindings/icetudio/internal/playback/models";

function fontState(f: FontState): string {
  if (f.active) return "在用";
  if (f.exists) return "可用";
  return f.error || "不可用";
}

// SoundFont Manager：打包默认/用户音色列表/Basic Waves 兜底；添加、移除、默认切换与优先级。
// 音频配置不参与音乐撤销（audio.json，见 docs/p2-daw-workspace.md §4）。
export function SoundFonts({ available, unsupported, onReset }: {
  available: boolean; unsupported: boolean; onReset: () => Promise<boolean>;
}) {
  const [list, setList] = useState<FontList | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const refresh = useCallback(async () => {
    try { setList(await PlaybackService.SoundFonts()); setError(""); } catch (e) { setError(String(e)); }
  }, []);
  useEffect(() => { void refresh(); }, [refresh]);
  const run = async (action: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true); setError("");
    try { await action(); await refresh(); } catch (e) { setError(String(e)); } finally { setBusy(false); }
  };
  const disabled = busy || !available || unsupported;
  // 绑定生成器的接口模式把可空切片标为 T[] | null（Go nil 切片）。
  const userFonts = list?.fonts ?? [];
  return <div className="soundfont-manager" aria-label="音色管理">
    {unsupported
      ? <p role="alert">检测到不受支持的音频配置（v1 或更旧），原文件已保留。重置后使用默认配置并可重新添加音色。
        <button className="secondary" disabled={busy} onClick={() => void run(async () => { if (!await onReset()) throw new Error("重置失败"); })}>重置音频配置</button></p>
      : <>
        <div className="editor-toolbar">
          <button disabled={disabled} title={available ? "选择本地 SF2/SF3，添加并设为默认音源" : "原生音频不可用"} onClick={() => void run(async () => {
            const path = await PlaybackService.PickSoundFont();
            if (!path) return;
            const entry = await PlaybackService.AddSoundFont(path);
            await PlaybackService.SetDefaultSoundFont(entry.id);
          })}>添加本地音色…</button>
          <button className="secondary" disabled={disabled || !list || list.bundled.isDefault || !list.bundled.exists}
            title={!list?.bundled.exists ? "未找到打包默认音色（发行物应内嵌；开发环境运行 scripts/fetch-soundfont.sh）" : "使用随应用提供的默认 GM 音色"}
            onClick={() => void run(() => PlaybackService.SetDefaultSoundFont(""))}>使用打包默认</button>
        </div>
        {list && <div className="soundfont-list">
          <div className="soundfont-item" data-default={list.bundled.isDefault}>
            <span className="name" title="随应用提供，离线可用">{list.bundled.name}</span>
            <span className="state">{fontState(list.bundled)}</span>
            {!list.bundled.isDefault && <button className="secondary" disabled={disabled || !list.bundled.exists} onClick={() => void run(() => PlaybackService.SetDefaultSoundFont(""))}>设为默认</button>}
          </div>
          {userFonts.map((f, i) => <div key={f.id} className="soundfont-item" data-default={f.isDefault} title={f.path}>
            <span className="name">{f.name}</span>
            <span className="state">{fontState(f)}</span>
            <span className="ops">
              <button className="secondary" disabled={disabled || f.isDefault} onClick={() => void run(() => PlaybackService.SetDefaultSoundFont(f.id))}>设为默认</button>
              <button className="secondary" aria-label={`提升 ${f.name} 优先级`} title="提升回退优先级" disabled={disabled || i === 0} onClick={() => void run(() => PlaybackService.MoveSoundFont(f.id, -1))}>↑</button>
              <button className="secondary" aria-label={`降低 ${f.name} 优先级`} title="降低回退优先级" disabled={disabled || i === userFonts.length - 1} onClick={() => void run(() => PlaybackService.MoveSoundFont(f.id, 1))}>↓</button>
              <button className="secondary" disabled={disabled} onClick={() => void run(() => PlaybackService.RemoveSoundFont(f.id))}>移除</button>
            </span>
          </div>)}
          <div className="soundfont-item basic" title="永远作为最后兜底，不可移除">
            <span className="name">{list.basic.name}</span>
            <span className="state">{fontState(list.basic)}</span>
          </div>
        </div>}
        {list?.notice && <p className="editor-help" role="status">{list.notice}</p>}
      </>}
    {error && <p role="alert">{error}</p>}
  </div>;
}
