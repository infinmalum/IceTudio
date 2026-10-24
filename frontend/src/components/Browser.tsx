import { useEffect, useMemo, useState } from "react";
import type { CSSProperties } from "react";
import { PlaybackService } from "../../bindings/icetudio";
import type { Status } from "../../bindings/icetudio/internal/playback/models";
import type { useProjects } from "../hooks/useProjects";
import { SoundFonts } from "./SoundFonts";
import { useTheme } from "../shared/theme";

type Projects = ReturnType<typeof useProjects>;

// 左侧浏览器（P2 工作区契约 §1.2）：工程管理、音色管理、MIDI 导入、最近与搜索。
// Samples 暂未实现，不展示入口。设置与开发者工具不在此出现（§1.6）。
export function Browser({ projects: p, style, openSettings, openAssistants }: {
  projects: Projects; style?: CSSProperties; openSettings?: () => void; openAssistants?: () => void;
}) {
  const { theme, toggle } = useTheme();
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<Status | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    const tick = async () => {
      try { const s = await PlaybackService.Status(); if (alive) setStatus(s); } catch { /* 浏览器/宿主未就绪时静默重试 */ }
    };
    void tick();
    const timer = setInterval(tick, 1000);
    return () => { alive = false; clearInterval(timer); };
  }, []);
  const q = query.trim().toLowerCase();
  const matches = useMemo(() => p.projects.filter(item => !q || item.title.toLowerCase().includes(q) || item.id.toLowerCase().includes(q)), [p.projects, q]);
  const recent = useMemo(() => [...p.projects].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)).slice(0, 5), [p.projects]);
  const driver = status?.settings.driver || "pulseaudio";
  const unsupported = /音频配置版本不受支持/.test(status?.error ?? "");
  const reconnect = async () => {
    if (busy) return;
    setBusy(true); setError("");
    try {
      await PlaybackService.Configure({ ...(status?.settings ?? { version: 2, driver, fonts: [], defaultFont: "" }), driver });
      setStatus(await PlaybackService.Status());
    } catch (e) { setError(String(e)); } finally { setBusy(false); }
  };
  return <aside className="browser" style={style} aria-label="浏览器">
    <div className="browser-brand"><span aria-hidden>✳</span> IceTudio</div>
    <details open className="browser-section">
      <summary>工程</summary>
      <div className="editor-toolbar">
        <button className="new-project" onClick={p.create} disabled={p.busy}>＋ 新建工程</button>
        <button className="secondary" onClick={p.importMIDI} disabled={p.busy}>导入 MIDI</button>
      </div>
      <label className="browser-search">搜索<input aria-label="搜索工程" placeholder="名称或 ID" value={query} onChange={e => setQuery(e.target.value)}/></label>
      {p.error ? <p className="project-error" role="alert">{p.error}</p> : null}
      <ul className="project-list" aria-label="工程列表">
        {matches.length === 0 ? <li className="project-list-empty">{p.projects.length ? "没有匹配的工程" : "还没有工程"}</li> : matches.map(item => (
          <li key={item.id} className={item.id === p.activeID ? "active" : ""}>
            <button className="project-select" data-id={item.id} onClick={() => p.select(item.id)} title={`${item.title} · 更新于 ${item.updatedAt.slice(0, 19).replace("T", " ")}`}>
              <span>{item.title}</span>
              <span className="project-meta">{item.updatedAt.slice(0, 19).replace("T", " ")}</span>
            </button>
            <button className="project-remove" onClick={() => p.remove(item.id)} disabled={p.busy} title={`删除 ${item.title}`}>删除</button>
          </li>
        ))}
      </ul>
    </details>
    <details className="browser-section">
      <summary>音色</summary>
      <SoundFonts available={!!status?.available} unsupported={unsupported}
        onReset={async () => { try { await PlaybackService.ResetAudioSettings(); return true; } catch { return false; } }}/>
      <div className="editor-toolbar">
        <label>音频驱动<select aria-label="音频驱动" value={driver} disabled={busy || unsupported}
          onChange={e => void PlaybackService.Configure({ ...(status?.settings ?? { version: 2, driver: e.target.value, fonts: [], defaultFont: "" }), driver: e.target.value }).catch(err => setError(String(err)))}>
          <option value="pulseaudio">PulseAudio / PipeWire 兼容</option><option value="pipewire">PipeWire 原生</option><option value="alsa">ALSA</option>
        </select></label>
        <button className="secondary" disabled={busy || !status?.available || unsupported} onClick={() => void reconnect()}>初始化/重连音频</button>
      </div>
      <p className="editor-help">默认 GM 音色为打包的 MuseScore_General（MIT，署名链见 assets/soundfonts/NOTICE.md）；Basic Waves 永远最后兜底。音色配置不进入音乐撤销历史。</p>
      {error && <p role="alert">{error}</p>}
    </details>
    <details className="browser-section">
      <summary>最近</summary>
      <ul className="project-list recent" aria-label="最近工程">
        {recent.length === 0 ? <li className="project-list-empty">暂无记录</li> : recent.map(item => (
          <li key={item.id} className={item.id === p.activeID ? "active" : ""}>
            <button className="project-select" onClick={() => p.select(item.id)} title={`${item.title} · ${item.id}`}>
              <span>{item.title}</span>
              <span className="project-meta">{item.updatedAt.slice(0, 19).replace("T", " ")}</span>
            </button>
          </li>
        ))}
      </ul>
    </details>
    <div className="browser-foot">
      {openAssistants && <button onClick={openAssistants} title="编辑创作助手与工作方法">✎ 助手</button>}
      {openSettings && <button onClick={openSettings} title="模型连接、技能与权限等设置">⚙ 设置</button>}
      <button className="theme-toggle" onClick={toggle} aria-label={theme === "dark" ? "切换到浅色主题" : "切换到深色主题"}
        title={theme === "dark" ? "切换到浅色主题" : "切换到深色主题"}>{theme === "dark" ? "☀ 浅色" : "🌙 深色"}</button>
    </div>
  </aside>;
}
