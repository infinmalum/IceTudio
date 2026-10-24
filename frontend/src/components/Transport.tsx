import { useEffect, useMemo, useRef, useState } from "react";
import type { Project } from "../../bindings/icetudio";
import { useMusicField } from "../hooks/useMusicInputs";
import type { useProjects } from "../hooks/useProjects";
import { usePlayback } from "../hooks/usePlayback";
import { meterIndex, positionLabel } from "../music/timeMap";

type Projects = ReturnType<typeof useProjects>;
export type EditorView = "arrange" | "split" | "roll";

const VIEWS: [EditorView, string][] = [["arrange", "编排"], ["split", "拆分"], ["roll", "琴键"]];

// 顶部走带条：单行紧凑 + 细进度条 + 精确位置/循环行（P2 工作区契约 §1.1）。
// 暂停 = 现有停止路径并保留位置；停止 = 停止并回开头；不引入第二套时钟。
// 音频细节（引擎/采样率/计数）在音源状态 chip 的 tooltip；管理在左侧浏览器。
export function Transport({ project, projects: p, snap, setSnap, view, setView }: {
  project: Project; projects: Projects; snap: number; setSnap: (n: number) => void;
  view: EditorView; setView: (v: EditorView) => void;
}) {
  const audio = usePlayback(project.id, project.revision);
  const status = audio.status;
  const current = status?.projectId === project.id ? status : null;
  const endTick = current?.endTick ?? Math.max(project.ppq * 4, ...(project.state.tracks ?? []).flatMap(t => (t.clips ?? []).map(c => c.start + c.length)));
  const [livePosition, setLivePosition] = useState(0);
  const focused = useRef(false);
  const field = useMusicField({ label: "播放与音频设置", title: project.title, target: { id: project.id, revision: project.revision } }, JSON.stringify({ position: String(livePosition), loop: current?.loop ?? false, start: String(current?.loopStart ?? 0), end: String(current?.loopEnd || endTick) }));
  const values = JSON.parse(field.raw) as { position: string; loop: boolean; start: string; end: string };
  const position = Number(values.position), loop = values.loop, loopStart = Number(values.start), loopEnd = Number(values.end);
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
  const pause = async () => { await audio.stop(); };
  const stop = async () => { await audio.stop(); setLivePosition(0); seek(0); };
  const at = Math.floor(current?.playing && !focused.current ? current.tick : position);
  const shown = useMemo(() => {
    try {
      if (!project.state.meter?.length) return { label: "—", meter: "—" };
      const info = meterIndex(project.ppq, project.state.meter).position(Math.max(0, at));
      return { label: positionLabel(info), meter: `${info.numerator}/${info.denominator}` };
    } catch { return { label: "—", meter: "—" }; }
  }, [project.ppq, project.state.meter, at]);
  const bpm = useMemo(() => {
    const tempo = project.state.tempo ?? [];
    const us = [...tempo].reverse().find(t => t.tick <= at)?.usPerQuarter ?? tempo[0]?.usPerQuarter;
    return us ? (60e6 / us).toFixed(2) : "—";
  }, [project.state.tempo, at]);
  const inputDisabled = audio.busy || field.locked || !!field.error;
  const fontTitle = `音源：${status?.resolvedFont || "Basic Waves"}${status?.notice ? `\n${status.notice}` : ""}\n${status?.engine || ""} · ${status?.sampleRate || 48000} Hz\n管理与切换在左侧浏览器「音色」分区`;
  return <section className="transport transport-top" aria-label="工程播放与工具栏" data-busy={audio.busy}>
    <div className="transport-bar">
      <button className="transport-play" disabled={disabled || !validPosition || !validLoop} title={!status?.available ? status?.error || "正在读取音频状态" : "从当前位置播放；无需连接模型"}
        onClick={() => void apply(() => audio.play(position, loop, loopStart, loopEnd))}>{current?.playing ? "重播" : "播放"}</button>
      <button className="transport-btn" disabled={!current?.playing && !audio.busy} title="暂停：停止发声并保留当前位置" onClick={() => void pause()}>暂停</button>
      <button className="transport-btn" disabled={!current && !audio.busy} title="停止并回到开头" onClick={() => void stop()}>停止</button>
      <span className="transport-readout bar-beat" title={`tick ${Math.max(0, at)}`}>{shown.label}</span>
      <span className="transport-readout bpm">{bpm === "—" ? "BPM —" : `${bpm}`} <b>{shown.meter}</b></span>
      <label className="transport-snap" title="吸附网格（编排与钢琴卷帘共用）">吸附
        <select aria-label="编排吸附" disabled={inputDisabled} value={snap} onChange={e => setSnap(Number(e.target.value))}>
          {[0.25, 0.5, 1, 2, 4].map(n => <option key={n} value={n}>{n} 四分拍</option>)}
        </select></label>
      <span className="transport-spacer"/>
      <div className="view-switch" role="group" aria-label="工作区视图">
        {VIEWS.map(([id, label]) => <button key={id} className="transport-btn" aria-pressed={view === id}
          disabled={id === "roll" && !project.state.tracks?.length}
          title={id === "arrange" ? "只显示多轨编排" : id === "split" ? "编排与钢琴卷帘上下拆分" : "只显示钢琴卷帘"}
          onClick={() => setView(id)}>{label}</button>)}
      </div>
      <button className="transport-btn transport-metronome" disabled title="节拍器尚未实现；将随播放打点在后续版本提供，不做假开关。">节拍器</button>
      <button className="transport-btn" disabled={p.busy} title="撤销上一次编辑事务" onClick={() => p.shift("undo")}>撤销</button>
      <button className="transport-btn" disabled={p.busy} title="重做被撤销的事务" onClick={() => p.shift("redo")}>重做</button>
      <button className="transport-btn" disabled={p.busy} onClick={p.exportMIDI}>导出 MIDI</button>
      <span className="save-chip" role="status" title={p.saveStatus || "编辑自动保存到本地，无需手动保存"}>{p.saveStatus || "已自动保存"}</span>
      <span className={`font-chip kind-${status?.resolvedKind ?? "basic"}`} title={fontTitle}>{status?.resolvedFont || "Basic Waves"}</span>
      <button className="transport-title" disabled={p.busy} title="重命名工程" onClick={() => void p.rename()}>{project.title || "未命名工程"}</button>
    </div>
    <input className="transport-position" aria-label="播放进度" type="range" min={0} max={Math.max(1, endTick)} step={1} value={position} disabled={inputDisabled}
      onPointerDown={() => { focused.current = true; }} onChange={e => change({ position: e.target.value })}
      onPointerUp={e => { focused.current = false; seek(Number(e.currentTarget.value)); }}
      onKeyUp={e => { if (!e.nativeEvent.isComposing) seek(Number(e.currentTarget.value)); }}/>
    <div className="transport-row">
      <label className="transport-pos">位置<input aria-label="播放位置" disabled={inputDisabled} type="number" min={0} max={endTick} step={1} value={values.position}
        onFocus={() => { focused.current = true; }} onChange={e => change({ position: e.target.value })} onBlur={() => { focused.current = false; }}
        onKeyDown={e => { if (!e.nativeEvent.isComposing && e.key === "Enter") { seek(Number(e.currentTarget.value)); e.currentTarget.blur(); } }}/></label>
      <button className="transport-btn" disabled={inputDisabled || !current || !validPosition || !validLoop} title={!current ? "先播放当前工程，再定位" : "定位到输入的 tick"} onClick={() => seek(position)}>定位</button>
      <label className="transport-loop"><input aria-label="启用循环" disabled={inputDisabled} type="checkbox" checked={loop} onChange={e => change({ loop: e.target.checked })}/>循环</label>
      <label className="transport-pos">从<input aria-label="循环起点" disabled={inputDisabled} type="number" step={1} min={0} max={endTick} value={values.start} onChange={e => change({ start: e.target.value })}/></label>
      <label className="transport-pos">到<input aria-label="循环终点" disabled={inputDisabled} type="number" step={1} min={1} max={endTick} value={values.end} onChange={e => change({ end: e.target.value })}/></label>
      <button className="transport-btn" disabled={inputDisabled || !current || !validLoop} title={current?.loop ? `当前循环 ${current.loopStart}–${current.loopEnd}（tick）` : "应用循环范围"} onClick={() => seek(position)}>应用循环</button>
      {!validLoop && <span role="alert" className="transport-warn">循环须满足 0 ≤ 起点 &lt; 终点 ≤ {endTick}</span>}
      {inputError && <span role="alert" className="transport-warn">{inputError}</span>}
      {(audio.error || status?.error) && <span role="alert" className="transport-warn">{audio.error || status?.error}</span>}
      {current && current.revision !== project.revision && <span className="transport-readout" title="界面修订与播放修订暂不一致">快照更新中</span>}
    </div>
  </section>;
}
