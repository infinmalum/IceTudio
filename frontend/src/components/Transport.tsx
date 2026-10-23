import { useEffect, useRef, useState } from "react";
import type { Project } from "../../bindings/icetudio";
import { useMusicField } from "../hooks/useMusicInputs";
import { usePlayback } from "../hooks/usePlayback";

export function Transport({ project }: { project: Project }) {
  const audio = usePlayback(project.id, project.revision);
  const status = audio.status;
  const current = status?.projectId === project.id ? status : null;
  const endTick = current?.endTick ?? Math.max(project.ppq * 4, ...(project.state.tracks ?? []).flatMap(t => (t.clips ?? []).map(c => c.start + c.length)));
  const [livePosition, setLivePosition] = useState(0);
  const focused = useRef(false);
  const field = useMusicField({ label: "播放与音频设置", title: project.title, target: { id: project.id, revision: project.revision } }, JSON.stringify({ position: String(livePosition), loop: current?.loop ?? false, start: String(current?.loopStart ?? 0), end: String(current?.loopEnd || endTick), driver: status?.settings.driver || "pulseaudio" }));
  const values = JSON.parse(field.raw) as { position: string; loop: boolean; start: string; end: string; driver: string };
  const position = Number(values.position), loop = values.loop, loopStart = Number(values.start), loopEnd = Number(values.end), driver = values.driver;
  const change = (patch: Partial<typeof values>) => field.change(JSON.stringify({ ...values, ...patch }));
  const [inputError, setInputError] = useState("");
  async function apply(action: () => Promise<boolean | undefined>) {
    if (field.locked || field.error) return;
    if (field.entry && field.entry.target.revision !== project.revision) { setInputError("工程已修改；播放设置仍按原修订保留，请丢弃这项输入后重新设置。"); return; }
    if (await action()) { field.clear(); setInputError(""); }
  }
  useEffect(() => { if (!focused.current) setLivePosition(Math.floor(current?.tick ?? 0)); }, [current?.tick]);
  const validPosition = values.position !== "" && Number.isInteger(position) && position >= 0 && position <= endTick;
  const validLoop = !loop || (values.start !== "" && values.end !== "" && Number.isInteger(loopStart) && Number.isInteger(loopEnd) && loopStart >= 0 && loopEnd > loopStart && loopEnd <= endTick);
  const disabled = audio.busy || !status?.available || field.locked || !!field.error;
  const seek = (tick: number) => { if (current && validPosition && validLoop && Number.isInteger(tick) && tick >= 0 && tick <= endTick) void apply(() => audio.seek(tick, loop, loopStart, loopEnd)); };
  const soundfont = status?.settings.soundFont || "";
  return <section className="transport" aria-label="工程播放" data-busy={audio.busy}>
    <div className="editor-toolbar">
      <button disabled={disabled || !validPosition || !validLoop} title={!status?.available ? status?.error || "正在读取音频状态" : "从当前位置播放工程；无需连接模型"}
        onClick={() => void apply(() => audio.play(position, loop, loopStart, loopEnd))}>{current?.playing ? "重新播放" : "播放"}</button>
      <button className="secondary" disabled={!current && !audio.busy} onClick={() => void audio.stop()}>停止</button>
      <button className="secondary" disabled={audio.busy || field.locked || !!field.error} onClick={() => { setLivePosition(0); seek(0); }}>回到开头</button>
      <label>播放位置（tick）<input aria-label="播放位置" disabled={audio.busy || field.locked || !!field.error} type="number" min={0} max={endTick} step={1} value={values.position}
        onFocus={() => { focused.current = true; }} onChange={e => change({ position: e.target.value })} onBlur={() => { focused.current = false; }}
        onKeyDown={e => { if (!e.nativeEvent.isComposing && e.key === "Enter") { seek(Number(e.currentTarget.value)); e.currentTarget.blur(); } }}/></label>
      <button className="secondary" disabled={audio.busy || !current || !validPosition || !validLoop} title={!current ? "先播放当前工程，再定位" : "定位到输入的 tick"} onClick={() => seek(position)}>定位</button>
      <span role="status">{current?.playing ? `播放中 · 修订 ${current.revision}` : "已停止"} · 第 {(position / project.ppq).toFixed(2)} 四分拍</span>
    </div>
    <input className="transport-position" aria-label="播放进度" type="range" min={0} max={Math.max(1, endTick)} step={1} value={position} disabled={audio.busy || field.locked || !!field.error}
      onPointerDown={() => { focused.current = true; }} onChange={e => change({ position: e.target.value })}
      onPointerUp={e => { focused.current = false; seek(Number(e.currentTarget.value)); }}
      onKeyUp={e => { if (!e.nativeEvent.isComposing) seek(Number(e.currentTarget.value)); }}/>
    <div className="editor-toolbar">
      <label><input aria-label="启用循环" disabled={audio.busy || field.locked || !!field.error} type="checkbox" checked={loop} onChange={e => change({ loop: e.target.checked })}/>循环</label>
      <label>从<input aria-label="循环起点" disabled={audio.busy || field.locked || !!field.error} type="number" step={1} min={0} max={endTick} value={values.start} onChange={e => change({ start: e.target.value })}/></label>
      <label>到<input aria-label="循环终点" disabled={audio.busy || field.locked || !!field.error} type="number" step={1} min={1} max={endTick} value={values.end} onChange={e => change({ end: e.target.value })}/></label>
      <button className="secondary" disabled={audio.busy || !current || !validLoop} title={!current ? "请先播放当前工程，再应用循环设置" : "应用循环范围"} onClick={() => seek(position)}>应用循环</button>
      <span>{current?.loop ? `当前循环 ${current.loopStart}–${current.loopEnd}` : "当前不循环"}（tick）</span>
      {!validLoop && <span role="alert">循环须满足 0 ≤ 起点 &lt; 终点 ≤ {endTick}</span>}
    </div>
    <details className="audio-settings"><summary>音频与音色 · {soundfont ? soundfont.split(/[/\\]/).pop() : "内置基础波形"}</summary>
      <p className="editor-help">内置音色用于基础合成器试听：程序 1–32 正弦、33–64 三角、65–96 锯齿、97–128 方波。它不模拟 GM 乐器；可选择你有权使用的本地 SF2/SF3。</p>
      <div className="editor-toolbar">
        <label>音频驱动<select aria-label="音频驱动" value={driver} disabled={audio.busy || field.locked || !!field.error} onChange={e => change({ driver: e.target.value })}><option value="pulseaudio">PulseAudio / PipeWire 兼容</option><option value="pipewire">PipeWire 原生</option><option value="alsa">ALSA</option></select></label>
        <button className="secondary" disabled={disabled} onClick={() => void apply(() => audio.configure({ version: 1, driver, soundFont: soundfont }))}>初始化/重连音频</button>
        <button className="secondary" disabled={disabled} onClick={() => void apply(() => audio.chooseFont(driver))}>选择本地音色</button>
        <button className="secondary" disabled={disabled} onClick={() => void apply(() => audio.configure({ version: 1, driver, soundFont: "" }))}>使用基础波形</button>
      </div>
      <p className="editor-help">{status?.engine || "原生音频状态读取中"} · {status?.sampleRate || 48000} Hz · 跳过 {current?.skippedEvents || 0} 个不执行的原始事件。播放中的修改由后台更新快照，不依赖页面计时器。</p>
      <p className="editor-help">音频回调 {status?.callbacks || 0} 次 · 控制争用静音块 {status?.missedBlocks || 0} 次 · 循环 {status?.loops || 0} 次。这些计数不代表物理输出延迟。</p>
    </details>
    {inputError && <p role="alert">{inputError}</p>}
    {(audio.error || status?.error) && <p className="project-error" role="alert">{audio.error || status?.error}</p>}
    {current && current.revision !== project.revision && <p className="editor-help">界面修订 {project.revision}，当前播放修订 {current.revision}；等待后台快照更新。</p>}
  </section>;
}
