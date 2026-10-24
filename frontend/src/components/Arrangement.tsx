import { useEffect, useRef, useState } from "react";
import type { Project } from "../../bindings/icetudio";
import type { Clip, Op, Track } from "../../bindings/icetudio/internal/project/models";
import { PAGE_BEATS, type EditTarget } from "../music/edits";
import { trackInstrumentLabel } from "../music/gm";
import { auditionInstrument } from "../music/audition";
import { InstrumentPicker } from "./InstrumentPicker";
import { TRACK_COLORS, colorById, displayColor } from "../music/palette";
import { useCloseBlocker, useCloseFrozen } from "../shared/closeGuard";
import { MusicRuler } from "./MusicRuler";

type Gesture = {
  target: EditTarget; trackID: string; clip: Clip; mode: "move" | "resize";
  pointer: number; x: number; zoom: number; snap: number; ppq: number; node: HTMLElement;
};
type Preview = { trackID: string; clipID: string; start: number; length: number; error: string };

export function Arrangement({ project, trackID, clipID, busy, select, edit, reportError, addTrack, snap, openClip }: {
  project: Project; trackID?: string; clipID?: string; busy: boolean;
  select: (trackID: string, clipID: string) => void;
  edit: (target: EditTarget, ops: Op[]) => Promise<boolean>;
  reportError: (error: unknown) => void; addTrack: () => void;
  snap: number; openClip: () => void;
}) {
  const tracks = project.state.tracks ?? [];
  const [requestedPage, setPage] = useState(0), [zoom, setZoom] = useState(64);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [colorTrack, setColorTrack] = useState("");
  const [instTrack, setInstTrack] = useState("");
  const gesture = useRef<Gesture | null>(null);
  const frozen = useCloseFrozen();
  const latest = useRef({ edit, reportError, busy, frozen });
  latest.current = { edit, reportError, busy, frozen };
  useCloseBlocker(() => gesture.current ? { kind: "busy", message: "片段手势尚未结束，请完成编辑或按 Escape 取消后再退出。" } : null);
  const pageTicks = PAGE_BEATS * project.ppq;
  const endTick = tracks.reduce((end, t) => (t.clips ?? []).reduce((n, c) => Math.max(n, c.start + c.length), end), pageTicks);
  const pageCount = Math.ceil(endTick / pageTicks);
  // Keep the drag's coordinate window stable even if another editor shortens
  // the project while the pointer is down. The stale commit must be rejected.
  const page = gesture.current ? requestedPage : Math.min(requestedPage, pageCount - 1);
  const start = page * pageTicks, end = start + pageTicks;
  const selected = tracks.find(t => t.id === trackID)?.clips?.find(c => c.id === clipID);

  function cancel() {
    const g = gesture.current;
    gesture.current = null; setPreview(null);
    if (g?.node.hasPointerCapture(g.pointer)) g.node.releasePointerCapture(g.pointer);
  }
  function value(g: Gesture, x: number): Preview {
    const steps = (x - g.x) / g.zoom / g.snap;
    const nearest = Math.sign(steps) * Math.round(Math.abs(steps));
    const delta = Math.sign(nearest) * Math.round(Math.abs(nearest) * g.snap * g.ppq);
    const next = { trackID: g.trackID, clipID: g.clip.id, start: g.clip.start, length: g.clip.length, error: "" };
    if (g.mode === "move") next.start += delta;
    else next.length += delta;
    if (next.start < 0 || next.start > 2147483647 || next.length < 1 || next.length > 2147483647)
      next.error = "起点须为 0–2147483647 tick，长度须为 1–2147483647 tick；本次拖动不会提交。";
    return next;
  }
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const g = gesture.current;
      if (g && e.pointerId === g.pointer && !latest.current.frozen) setPreview(value(g, e.clientX));
    };
    const up = (e: PointerEvent) => {
      const g = gesture.current;
      if (!g || e.pointerId !== g.pointer || latest.current.frozen) return;
      const next = value(g, e.clientX);
      cancel();
      if (next.error) { latest.current.reportError(next.error); return; }
      if (next.start === g.clip.start && next.length === g.clip.length) return;
      void latest.current.edit(g.target, [{ kind: "clip.update", trackId: g.trackID, clipId: g.clip.id,
        clipPatch: g.mode === "move" ? { start: next.start } : { length: next.length } }]);
    };
    const pointerCancel = (e: PointerEvent) => { if (gesture.current?.pointer === e.pointerId) cancel(); };
    const escape = (e: KeyboardEvent) => { if (e.key === "Escape" && !e.isComposing && gesture.current) { e.preventDefault(); cancel(); } };
    const hidden = () => { if (document.hidden) cancel(); };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", pointerCancel);
    window.addEventListener("blur", cancel);
    window.addEventListener("keydown", escape);
    document.addEventListener("visibilitychange", hidden);
    return () => {
      cancel();
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", pointerCancel);
      window.removeEventListener("blur", cancel);
      window.removeEventListener("keydown", escape);
      document.removeEventListener("visibilitychange", hidden);
    };
  }, []);
  useEffect(() => {
    const g = gesture.current;
    if (g && (g.trackID !== trackID || g.clip.id !== clipID || busy)) cancel();
  }, [trackID, clipID, busy]);

  function down(e: React.PointerEvent<HTMLElement>, track: Track, clip: Clip, mode: Gesture["mode"]) {
    if (e.button !== 0 || !e.isPrimary || busy || frozen || gesture.current) return;
    e.preventDefault(); e.stopPropagation();
    e.currentTarget.focus();
    select(track.id, clip.id);
    setPage(page);
    gesture.current = { target: { id: project.id, revision: project.revision }, trackID: track.id,
      clip: structuredClone(clip), mode, pointer: e.pointerId, x: e.clientX, zoom, snap, ppq: project.ppq, node: e.currentTarget };
    e.currentTarget.setPointerCapture(e.pointerId);
    setPreview(value(gesture.current, e.clientX));
  }
  const disabled = busy || frozen || !!preview;
  return <section aria-label="片段编排">
    <div className="editor-toolbar">
      <button disabled={busy || frozen || tracks.length >= 64} title={tracks.length >= 64 ? "已达到 64 轨上限" : "创建音轨及 16 个四分拍长度的片段"} onClick={addTrack}>添加音轨</button>
      <button className="secondary" disabled={disabled || page === 0} onClick={() => setPage(page - 1)} title="上一页（片段跨页显示）">编排上一页</button>
      <span className="transport-readout" title={`四分拍 ${page * PAGE_BEATS + 1}–${(page + 1) * PAGE_BEATS}；拖动片段移动，拖动右端拉伸，双击进钢琴卷帘`}>{page + 1}/{pageCount} 页</span>
      <button className="secondary" disabled={disabled || page >= pageCount - 1} onClick={() => setPage(page + 1)}>编排下一页</button>
      <button className="secondary" disabled={disabled || !selected} onClick={() => selected && setPage(Math.floor(selected.start / pageTicks))}>显示选中片段</button>
      <label>缩放 <input aria-label="编排缩放" type="range" min={16} max={200} value={zoom} disabled={disabled} onChange={e => setZoom(Number(e.target.value))}/></label>
    </div>
    <div className="arrangement" aria-label="轨道与片段" data-page-start={start}>
      <div className="arrangement-row arrangement-header"><span className="arrangement-label">{tracks.length} 条音轨</span>
        {project.state.meter?.length && project.state.tempo?.length ? <MusicRuler ppq={project.ppq} start={start} zoom={zoom} meter={project.state.meter} tempo={project.state.tempo}/> : null}</div>
      {tracks.map((t, ti) => {
        const clips = (t.clips ?? []).map(c => {
          const g = gesture.current;
          // Preserve the original displayed target through external updates.
          return g?.clip.id === c.id ? g.clip : c;
        });
        const g = gesture.current;
        if (g?.trackID === t.id && !clips.some(c => c.id === g.clip.id)) clips.push(g.clip);
        const laneEnds: number[] = [];
        const visible = clips.filter(c => c.start < end && c.start + c.length > start)
          .sort((a, b) => a.start - b.start || a.start + a.length - b.start - b.length || a.id.localeCompare(b.id)).map(c => {
            let lane = laneEnds.findIndex(n => n <= c.start);
            if (lane < 0) lane = laneEnds.length;
            laneEnds[lane] = c.start + c.length;
            return { clip: c, lane };
          });
        return <div className={`arrangement-row arrangement-track ${t.id === trackID ? "selected" : ""}`} key={t.id}>
          <div className="arrangement-label" data-color={displayColor(t.color ?? "", ti).id}>
            <div className="track-head">
              <button className="track-color" aria-label={`轨道颜色：${displayColor(t.color ?? "", ti).zh}`} title={`轨道颜色：${displayColor(t.color ?? "", ti).zh}（点击更改）`}
                style={{ background: displayColor(t.color ?? "", ti).css }}
                disabled={busy || frozen} onClick={() => { setColorTrack(colorTrack === t.id ? "" : t.id); setInstTrack(""); }}/>
              <button className="track-name secondary" aria-pressed={t.id === trackID} onClick={() => select(t.id, "")}>{t.name || "未命名音轨"}</button>
              <button className="secondary" aria-label={`静音 ${t.name}`} aria-pressed={!!t.muted} disabled={busy || frozen} onClick={() => void edit(project, [{ kind: "track.update", trackId: t.id, trackPatch: { muted: !t.muted } }])}>M</button>
              <button className="secondary" aria-label={`独奏 ${t.name}`} aria-pressed={!!t.solo} disabled={busy || frozen} onClick={() => void edit(project, [{ kind: "track.update", trackId: t.id, trackPatch: { solo: !t.solo } }])}>S</button>
            </div>
            <button className="track-instrument" title={`乐器 ${trackInstrumentLabel(t.program, t.channel)} · 通道 ${t.channel + 1}${t.channel === 9 ? "（GM 打击组）" : ""}；点击更换并试听`}
              disabled={busy || frozen} onClick={() => { setInstTrack(instTrack === t.id ? "" : t.id); setColorTrack(""); }}>
              {trackInstrumentLabel(t.program, t.channel)} · CH {t.channel + 1}{t.channel === 9 ? " 打击" : ""} ▾
            </button>
            {instTrack === t.id && <div className="track-inst-popover" role="dialog" aria-label="选择轨道乐器">
              <InstrumentPicker program={t.program} channel={t.channel} disabled={busy || frozen}
                onPick={program => {
                  setInstTrack("");
                  if (program !== t.program) void edit(project, [{ kind: "track.update", trackId: t.id, trackPatch: { program } }]);
                  void auditionInstrument(program, t.channel, t.channel === 9 ? [36, 38] : [60]);
                }}/>
              <div className="editor-toolbar">
                <button className="secondary" disabled={busy || frozen}
                  onClick={() => void auditionInstrument(t.program, t.channel, t.channel === 9 ? [36, 38] : [60])}>试听当前音色</button>
              </div>
            </div>}
            {colorTrack === t.id && <div className="track-color-picker" role="group" aria-label="选择轨道颜色">
              {TRACK_COLORS.map(c => <button key={c.id} className="swatch" title={c.zh} aria-label={`颜色 ${c.zh}`} aria-pressed={t.color === c.id} style={{ background: c.css }}
                disabled={busy || frozen} onClick={() => { setColorTrack(""); if (t.color !== c.id) void edit(project, [{ kind: "track.update", trackId: t.id, trackPatch: { color: c.id } }]); }}/>)}
              <button className="secondary" title="清除显式颜色，恢复按轨道序的默认色" disabled={busy || frozen}
                onClick={() => { setColorTrack(""); if (t.color) void edit(project, [{ kind: "track.update", trackId: t.id, trackPatch: { color: "" } }]); }}>清除</button>
            </div>}
          </div>
          <div className="arrangement-lane" style={{ width: PAGE_BEATS * zoom, height: Math.max(1, laneEnds.length) * 36, backgroundSize: `${zoom}px 100%` }}>
            {visible.map(({ clip: c, lane }) => {
              const v = preview?.clipID === c.id ? preview : c;
              const left = Math.max(start, v.start), right = Math.min(end, v.start + v.length);
              const resize = (g?.clip.id === c.id && g.mode === "resize") || v.start + v.length <= end && v.start + v.length > start && (right - left) / project.ppq * zoom >= 18;
              return <div key={c.id} className={`arrangement-block ${c.id === clipID && t.id === trackID ? "selected" : ""} ${preview?.clipID === c.id ? "dragging" : ""} ${preview?.clipID === c.id && preview.error ? "invalid" : ""}`}
                data-clip={c.id} data-track={t.id} data-lane={lane} data-crop-left={v.start < start} data-crop-right={v.start + v.length > end}
                data-color={(colorById(c.color ?? "") ?? colorById(t.color ?? "") ?? displayColor("", ti)).id}
                style={{ left: (left - start) / project.ppq * zoom, width: Math.max(2, (right - left) / project.ppq * zoom), top: lane * 36 + 2 }}>
                <button className="arrangement-clip" aria-pressed={c.id === clipID && t.id === trackID} disabled={busy || frozen}
                  title={`${c.name || "片段"} · 起点 ${v.start} tick · 长度 ${v.length} tick`}
                  onPointerDown={e => down(e, t, c, "move")} onLostPointerCapture={() => { if (gesture.current?.clip.id === c.id) cancel(); }}
                  onClick={e => { if (e.detail === 0) select(t.id, c.id); }}
                  onDoubleClick={() => { select(t.id, c.id); openClip(); }}>{v.start < start ? "◀ " : ""}{c.name || "片段"}{v.start + v.length > end ? " ▶" : ""}</button>
                {resize && <button className="arrangement-resize" aria-label={`拉伸 ${c.name || "片段"}`} title="拖动调整片段长度；不能截掉已有事件" disabled={busy || frozen}
                  onPointerDown={e => down(e, t, c, "resize")} onLostPointerCapture={() => { if (gesture.current?.clip.id === c.id) cancel(); }}/>}
              </div>;
            })}
          </div>
        </div>;
      })}
      {!tracks.length && <div className="arrangement-empty">
        <p>还没有音轨。添加一条音轨就开始写音符；或导入 MIDI 继续。</p>
        <div className="hero-actions"><button disabled={busy || frozen || tracks.length >= 64} onClick={addTrack}>添加第一条音轨</button></div>
      </div>}
    </div>
    {preview && <p className="editor-help" role="status">{preview.error || `预览：起点 ${preview.start} tick · 长度 ${preview.length} tick；松开提交，Escape 取消`}</p>}
  </section>;
}
