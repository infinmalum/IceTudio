import { useEffect, useRef, useState } from "react";
import type { Project } from "../../bindings/icetudio";
import type { useProjects } from "../hooks/useProjects";
import { DENOMINATORS, draftMaps, MAP_PAGE_SIZE, mapOps, MAX_TICK, tempoUS, type MapDraft } from "../music/timeMap";
import { useDialog } from "../shared/dialogs";
import { useCloseBlocker, useCloseFrozen } from "../shared/closeGuard";
import { useMapCheckpoint, type MapSnapshot } from "../hooks/useTimeMapDraft";

type Projects = ReturnType<typeof useProjects>;
type Base = { id: string; title: string; ppq: number; revision: number; tempo: NonNullable<Project["state"]["tempo"]>; meter: NonNullable<Project["state"]["meter"]>; serial: number; restored?: MapDraft };

export function TimeMapEditor({ project, projects }: { project: Project; projects: Projects }) {
  const [base, setBase] = useState<Base | null>(null);
  const serial = useRef(0);
  const tempo = project.state.tempo ?? [], meter = project.state.meter ?? [];
  const open = () => setBase({ id: project.id, title: project.title, ppq: project.ppq, revision: project.revision, tempo: structuredClone(tempo), meter: structuredClone(meter), serial: ++serial.current });
  const saved = projects.timeMapDrafts.snapshot;
  const restore = () => { if (saved?.projectID === project.id) setBase({ id: saved.projectID, title: saved.title, ppq: saved.ppq, revision: saved.baseRevision, tempo: saved.tempo, meter: saved.meter, restored: saved.draft, serial: ++serial.current }); };
  const firstTempo = tempo[0], firstMeter = meter[0];
  if (!firstTempo || !firstMeter) return <p role="alert">工程时间图缺少起始点，请刷新工程；未自动改写音乐数据。</p>;
  return <section className="time-map-summary" aria-label="工程时间图">
    <div className="editor-toolbar time-map-bar">
      <button className="secondary" disabled={projects.busy || !projects.timeMapDrafts.ready || projects.timeMapDrafts.writing || !!projects.timeMapDrafts.error || !!saved} title={saved ? "先恢复或丢弃已保留的时间图草稿" : `编辑工程时间图（当前 ${tempo.length} 个速度点 · ${meter.length} 个拍号点）`} onClick={open}>编辑速度与拍号</button>
      {saved?.projectID === project.id && <button className="secondary" disabled={projects.busy || !projects.timeMapDrafts.ready || projects.timeMapDrafts.writing || !!projects.timeMapDrafts.error} onClick={restore}>恢复时间图草稿</button>}
    </div>
    {base && <MapDialog key={base.serial} base={base} project={project} projects={projects} close={() => setBase(null)} reload={open}/>}
  </section>;
}

function MapDialog({ base, project, projects: p, close, reload }: { base: Base; project: Project; projects: Projects; close: () => void; reload: () => void }) {
  const dialog = useDialog();
  const box = useRef<HTMLDialogElement>(null);
  const initial = useRef(draftMaps(base.tempo, base.meter));
  const [draft, setDraft] = useState<MapDraft>(() => structuredClone(base.restored ?? initial.current));
  const [tempoPage, setTempoPage] = useState(0), [meterPage, setMeterPage] = useState(0);
  const nextKey = useRef(1 + draft.meter.reduce((n, r) => Math.max(n, r.key), draft.tempo.reduce((n, r) => Math.max(n, r.key), 0)));
  const [submitting, setSubmitting] = useState(false), [error, setError] = useState("");
  const [committed, setCommitted] = useState(false);
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial.current);
  const snapshot: MapSnapshot = { version: 1, projectID: base.id, title: base.title, ppq: base.ppq, baseRevision: base.revision, tempo: base.tempo, meter: base.meter, draft };
  const checkpoint = useMapCheckpoint(p.timeMapDrafts, dirty ? JSON.stringify(snapshot) : null);
  const frozen = useCloseFrozen();
  useCloseBlocker(() => submitting ? { kind: "busy", message: "时间图正在提交或清理，请等待完成后再退出。" } : checkpoint.pending || checkpoint.error ? { kind: "busy", message: "最新时间图草稿尚未保留，请在表单重试保留或明确丢弃。" } : dirty ? { kind: "draft", preserved: true, message: "有已保留、尚未应用的速度/拍号时间图草稿", version: JSON.stringify({ id: base.id, revision: base.revision, draft }) } : null);
  const disabled = p.busy || submitting || frozen || committed;
  useEffect(() => { const node = box.current!; node.showModal(); return () => node.close(); }, []);
  async function discard(action: () => void) {
    if (submitting) return;
    if (committed || !dirty || await dialog.confirm({ title: "丢弃时间图草稿？", body: "尚未应用的速度/拍号输入和已保留的草稿会丢弃。已保存的工程及其他编辑不受影响。", confirm: "丢弃草稿", cancel: "继续编辑", danger: true })) {
      setSubmitting(true);
      try { await checkpoint.discard(); action(); }
      catch (e) { setError(`草稿清理失败，当前输入仍保留。${String(e)}`); }
      finally { setSubmitting(false); }
    }
  }
  async function apply() {
    if (disabled || committed) return;
    let ops;
    try { ops = mapOps(draft, base); } catch (e) { setError(String(e)); return; }
    if (!ops.length) { await discard(close); return; }
    setSubmitting(true); setError("");
    try {
      await checkpoint.flush();
      if (await p.edit(base, ops)) {
        setCommitted(true);
        try { await checkpoint.discard(); close(); }
        catch (e) { setError(`修改已保存，草稿清理失败；请关闭表单重试清理，勿重复应用。${String(e)}`); }
      }
      else setError("本次应用未获成功确认；草稿已保留。请查看原因或查询保存结果，勿重复提交。可丢弃草稿并重新载入当前图。");
    } catch (e) { setError(String(e));
    } finally { setSubmitting(false); }
  }
  function add(kind: "tempo" | "meter") {
    const rows = draft[kind];
    const tick = String(Math.min(MAX_TICK, rows.reduce((n, r) => Math.max(n, Number(r.tick) || 0), 0) + project.ppq * 4));
    const key = nextKey.current++;
    if (kind === "tempo") {
      setDraft(d => ({ ...d, tempo: [...d.tempo, { key, tick, bpm: "120", originalBPM: "120", originalUS: 500000 }] }));
      setTempoPage(Math.floor(rows.length / MAP_PAGE_SIZE));
    } else {
      setDraft(d => ({ ...d, meter: [...d.meter, { key, tick, numerator: "4", denominator: "4" }] }));
      setMeterPage(Math.floor(rows.length / MAP_PAGE_SIZE));
    }
  }
  const pagination = (kind: "tempo" | "meter", page: number, setPage: (p: number) => void) => <div className="editor-toolbar">
    <button type="button" className="secondary" disabled={disabled || page === 0} onClick={() => setPage(page - 1)}>上页{kind === "tempo" ? "速度" : "拍号"}</button>
    <span>第 {page + 1}/{Math.max(1, Math.ceil(draft[kind].length / MAP_PAGE_SIZE))} 页 · {draft[kind].length} 点</span>
    <button type="button" className="secondary" disabled={disabled || (page + 1) * MAP_PAGE_SIZE >= draft[kind].length} onClick={() => setPage(page + 1)}>下页{kind === "tempo" ? "速度" : "拍号"}</button>
    <button type="button" disabled={disabled} onClick={() => add(kind)}>添加{kind === "tempo" ? "速度" : "拍号"}点</button>
  </div>;
  return <dialog ref={box} className="app-dialog time-map-dialog" aria-labelledby="time-map-title" onCancel={e => { e.preventDefault(); if (!frozen) void discard(close); }}
    onKeyDown={e => {
      e.stopPropagation();
      if (frozen) { e.preventDefault(); return; }
      if (e.key === "Escape" && !e.nativeEvent.isComposing) { e.preventDefault(); void discard(close); }
      if (e.key === "Enter" && (e.nativeEvent.isComposing || (e.target as HTMLElement).tagName === "INPUT")) e.preventDefault();
    }}>
    <form noValidate onSubmit={e => { e.preventDefault(); void apply(); }}>
      <h2 id="time-map-title">编辑速度与拍号</h2>
      <p className="editor-help" role="status">{dirty ? "尚未提交的时间图草稿" : "查看时间图"} · 基准修订 {base.revision} · 所有位置是工程绝对 tick</p>
      {dirty && <p className="map-checkpoint" role="status">{checkpoint.error || (checkpoint.pending ? "正在保留时间图草稿…" : "时间图草稿已保留，尚未应用到工程")}</p>}
      {checkpoint.error && <button type="button" disabled={disabled} onClick={() => void checkpoint.flush().catch(() => {})}>重试保留草稿</button>}
      {committed && <p role="status">本次修改已保存；请重试清理草稿，勿再次应用。</p>}
      {project.revision !== base.revision && <p role="alert">工程已变为修订 {project.revision}；此表仍基于修订 {base.revision}，应用将进行冲突检查。可丢弃草稿并重新载入。</p>}
      <p className="editor-help">速度按整数微秒/四分音符保存；未改动的导入值原样保留。拍号变化点开启新小节，旧小节中途变化会截短旧小节。tick 0 起点必须保留。表单打开时普通工程导航不可用。</p>
      <section aria-label="速度变化点">
        <h3>速度变化点</h3>{pagination("tempo", tempoPage, setTempoPage)}
        <div className="time-map-table"><table><thead><tr><th>工程 tick</th><th>BPM</th><th>实际微秒/四分</th><th>操作</th></tr></thead><tbody>
          {draft.tempo.slice(tempoPage * MAP_PAGE_SIZE, (tempoPage + 1) * MAP_PAGE_SIZE).map(row => <tr key={row.key}>
            <td><input aria-label={`速度位置 ${row.key}`} type="number" min={0} max={MAX_TICK} step={1} value={row.tick} disabled={disabled || row.key === 0} onChange={e => setDraft(d => ({ ...d, tempo: d.tempo.map(r => r.key === row.key ? { ...r, tick: e.target.value } : r) }))}/></td>
            <td><input aria-label={`速度 BPM ${row.key}`} type="number" step="any" value={row.bpm} disabled={disabled} onChange={e => setDraft(d => ({ ...d, tempo: d.tempo.map(r => r.key === row.key ? { ...r, bpm: e.target.value } : r) }))}/></td>
            <td>{(() => { try { return tempoUS(row); } catch { return "无效速度"; } })()}</td>
            <td><button type="button" className="secondary" aria-label={`删除速度点 ${row.key}`} title={row.key === 0 ? "tick 0 起点必须保留" : "应用后删除；可撤销"} disabled={disabled || row.key === 0} onClick={() => {
              setDraft(d => ({ ...d, tempo: d.tempo.filter(r => r.key !== row.key) })); setTempoPage(Math.min(tempoPage, Math.max(0, Math.ceil((draft.tempo.length - 1) / MAP_PAGE_SIZE) - 1)));
            }}>删除</button></td>
          </tr>)}
        </tbody></table></div>
      </section>
      <section aria-label="拍号变化点">
        <h3>拍号变化点</h3>{pagination("meter", meterPage, setMeterPage)}
        <div className="time-map-table"><table><thead><tr><th>工程 tick</th><th>分子</th><th>分母</th><th>操作</th></tr></thead><tbody>
          {draft.meter.slice(meterPage * MAP_PAGE_SIZE, (meterPage + 1) * MAP_PAGE_SIZE).map(row => <tr key={row.key}>
            <td><input aria-label={`拍号位置 ${row.key}`} type="number" min={0} max={MAX_TICK} step={1} value={row.tick} disabled={disabled || row.key === 0} onChange={e => setDraft(d => ({ ...d, meter: d.meter.map(r => r.key === row.key ? { ...r, tick: e.target.value } : r) }))}/></td>
            <td><input aria-label={`拍号分子 ${row.key}`} type="number" min={1} max={255} step={1} value={row.numerator} disabled={disabled} onChange={e => setDraft(d => ({ ...d, meter: d.meter.map(r => r.key === row.key ? { ...r, numerator: e.target.value } : r) }))}/></td>
            <td><select aria-label={`拍号分母 ${row.key}`} value={row.denominator} disabled={disabled} onChange={e => setDraft(d => ({ ...d, meter: d.meter.map(r => r.key === row.key ? { ...r, denominator: e.target.value } : r) }))}>{DENOMINATORS.map(n => <option key={n} value={n}>{n}</option>)}</select></td>
            <td><button type="button" className="secondary" aria-label={`删除拍号点 ${row.key}`} title={row.key === 0 ? "tick 0 起点必须保留" : "应用后删除；可撤销"} disabled={disabled || row.key === 0} onClick={() => {
              setDraft(d => ({ ...d, meter: d.meter.filter(r => r.key !== row.key) })); setMeterPage(Math.min(meterPage, Math.max(0, Math.ceil((draft.meter.length - 1) / MAP_PAGE_SIZE) - 1)));
            }}>删除</button></td>
          </tr>)}
        </tbody></table></div>
      </section>
      {(error || p.error) && <p className="project-error" role="alert">{error}{p.error && ` ${p.error}`}</p>}
      {p.pending && <button type="button" className="secondary" disabled={submitting || frozen || !p.recoveryReady} onClick={() => void p.resolvePending()}>查询保存结果</button>}
      <div className="actions">
        {dirty && !committed && <button type="button" className="secondary" disabled={disabled} onClick={() => {
          setSubmitting(true);
          void checkpoint.flush().then(close).catch(e => setError(String(e))).finally(() => setSubmitting(false));
        }}>保留草稿并关闭</button>}
        <button type="button" className="secondary" disabled={submitting || frozen} onClick={() => void discard(close)}>关闭时间图</button>
        <button type="button" className="secondary" disabled={disabled} onClick={() => void discard(reload)}>重新载入当前图</button>
        <button type="submit" disabled={disabled || !dirty || committed}>应用时间图</button>
      </div>
    </form>
  </dialog>;
}
