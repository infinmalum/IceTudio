import { useEffect, useRef, useState } from "react";
import type { Project } from "../../bindings/icetudio";
import type { Clip, Op, Track } from "../../bindings/icetudio/internal/project/models";
import type { useProjects } from "../hooks/useProjects";
import { objectID, type EditTarget } from "../music/edits";
import { useDialog } from "../shared/dialogs";
import { Transport } from "./Transport";
import { PianoRoll } from "./PianoRoll";
import { TimeMapEditor } from "./TimeMapEditor";
import { Arrangement } from "./Arrangement";
import { useMusicField } from "../hooks/useMusicInputs";

type Projects = ReturnType<typeof useProjects>;

// A changed raw input keeps its original revision until accepted or discarded.
function NumberEdit({ label, value, min, max, disabled, target, title, trackID, clipID, commit }: {
  label: string; value: number; min: number; max: number; disabled: boolean;
  target: EditTarget; title: string; trackID: string; clipID?: string;
  commit: (value: number, target: EditTarget) => Promise<boolean>;
}) {
  const field = useMusicField({ label, title, target, trackID, clipID }, String(value));
  const frozen = useRef(target);
  const cancelled = useRef(false);
  const applying = useRef(false);
  const valid = field.raw !== "" && Number.isInteger(Number(field.raw)) && Number(field.raw) >= min && Number(field.raw) <= max;
  async function apply() {
    if (cancelled.current || applying.current || !field.entry || !valid || field.error) return;
    applying.current = true;
    try { if (Number(field.raw) === value || await commit(Number(field.raw), field.entry.target)) field.clear(); }
    finally { applying.current = false; }
  }
  return <label>{label}<input aria-label={label} type="number" min={min} max={max} step={1} aria-invalid={!valid || undefined} disabled={disabled || field.locked || !!field.error}
    value={field.raw} onFocus={() => { cancelled.current = false; frozen.current = { ...target }; }}
    onChange={e => field.change(e.target.value, { label, title, target: frozen.current, trackID, clipID })} onBlur={() => void apply()}
    onKeyDown={e => {
      if (e.nativeEvent.isComposing) return;
      if (e.key === "Enter") e.currentTarget.blur();
      if (e.key === "Escape") { cancelled.current = true; field.clear(); e.currentTarget.blur(); }
    }}/>{!valid && <span role="alert">请输入 {min}–{max} 的整数；输入已保留，Escape 丢弃</span>}</label>;
}

export function ProjectEditor({ project, projects: p }: { project: Project; projects: Projects }) {
  const dialog = useDialog();
  const tracks = project.state.tracks ?? [];
  const [trackID, setTrackID] = useState("");
  const [clipID, setClipID] = useState("");
  const track = tracks.find(t => t.id === trackID) ?? tracks[0];
  const clip = track?.clips?.find(c => c.id === clipID) ?? track?.clips?.[0];
  useEffect(() => {
    const focus = p.inputs.focus;
    if (focus?.target.id === project.id) {
      if (focus.trackID) setTrackID(focus.trackID);
      if (focus.clipID) setClipID(focus.clipID);
    }
  }, [p.inputs.focus, project.id]);
  const target = { id: project.id, revision: project.revision };
  const edit = (ops: Op[], frozen: EditTarget = target) => p.edit(frozen, ops);
  function addTrack() {
    const id = objectID("t"), clipID = objectID("c");
    void edit([
      { kind: "track.add", track: { id, name: `音轨 ${tracks.length + 1}`, channel: tracks.length % 16, program: 0 } },
      { kind: "clip.add", trackId: id, clip: { id: clipID, trackId: id, name: "片段 1", start: 0, length: project.ppq * 16 } },
    ]).then(ok => { if (ok) { setTrackID(id); setClipID(clipID); } });
  }
  async function renameTrack(t: Track) {
    const frozen = target;
    const name = await dialog.ask({ title: "重命名音轨", label: "音轨名", initial: t.name, singleLine: true, confirm: "应用" });
    if (name !== null && name !== t.name) void edit([{ kind: "track.update", trackId: t.id, trackPatch: { name } }], frozen);
  }
  async function deleteTrack(t: Track) {
    const frozen = target;
    if (await dialog.confirm({ title: "删除音轨", body: `删除「${t.name || "未命名音轨"}」及其中全部片段和事件；可用撤销恢复。`, confirm: "删除", danger: true }))
      void edit([{ kind: "track.remove", trackId: t.id }], frozen);
  }
  function addClip() {
    if (!track) return;
    const id = objectID("c");
    const start = Math.max(0, ...(track.clips ?? []).map(c => c.start + c.length));
    void edit([{ kind: "clip.add", trackId: track.id, clip: { id, trackId: track.id, name: `片段 ${(track.clips?.length ?? 0) + 1}`, start, length: project.ppq * 16 } }])
      .then(ok => { if (ok) setClipID(id); });
  }
  async function renameClip(c: Clip) {
    const frozen = target, tid = track.id;
    const name = await dialog.ask({ title: "重命名片段", label: "片段名", initial: c.name, singleLine: true, confirm: "应用" });
    if (name !== null && name !== c.name) void edit([{ kind: "clip.update", trackId: tid, clipId: c.id, clipPatch: { name } }], frozen);
  }
  async function deleteClip(c: Clip) {
    const frozen = target, tid = track.id;
    if (await dialog.confirm({ title: "删除片段", body: `删除「${c.name || "未命名片段"}」及其所有音符、控制和原始事件；可用撤销恢复。`, confirm: "删除", danger: true }))
      void edit([{ kind: "clip.remove", trackId: tid, clipId: c.id }], frozen);
  }
  return <div className="music-editor" aria-label="音乐编辑工作区">
    <Transport key={project.id} project={project}/>
    <TimeMapEditor project={project} projects={p}/>
    <Arrangement project={project} trackID={track?.id} clipID={clip?.id} busy={p.busy} edit={p.edit} reportError={p.reportError} addTrack={addTrack}
      select={(tid, cid) => { setTrackID(tid); setClipID(cid); }}/>
    {track && <div className="editor-toolbar track-inspector" key={track.id}>
      <span>{track.name}</span>
      <label>片段 <select aria-label="选择片段" value={clip?.id ?? ""} onChange={e => setClipID(e.target.value)}>
        {(track.clips ?? []).map(c => <option key={c.id} value={c.id}>{c.name || "片段"} · {c.start} tick</option>)}
      </select></label>
      <button className="secondary" disabled={p.busy} onClick={() => void renameTrack(track)}>音轨重命名</button>
      <button className="secondary" disabled={p.busy} onClick={() => void deleteTrack(track)}>删除音轨</button>
      <NumberEdit label="新音符通道（1–16）" value={track.channel + 1} min={1} max={16} disabled={p.busy} target={target} title={project.title} trackID={track.id}
        commit={(value, frozen) => edit([{ kind: "track.update", trackId: track.id, trackPatch: { channel: value - 1 } }], frozen)}/>
      <NumberEdit label="乐器程序（1–128）" value={track.program + 1} min={1} max={128} disabled={p.busy} target={target} title={project.title} trackID={track.id}
        commit={(value, frozen) => edit([{ kind: "track.update", trackId: track.id, trackPatch: { program: value - 1 } }], frozen)}/>
      <button className="secondary" disabled={p.busy} onClick={addClip}>添加片段</button>
    </div>}
    {track && clip && <>
      <div className="editor-toolbar clip-inspector" key={clip.id}>
        <span>{clip.name || "未命名片段"}</span>
        <button className="secondary" disabled={p.busy} onClick={() => void renameClip(clip)}>片段重命名</button>
        <NumberEdit clipID={clip.id} label="片段起点（tick）" value={clip.start} min={0} max={2147483647} disabled={p.busy} target={target} title={project.title} trackID={track.id}
          commit={(value, frozen) => edit([{ kind: "clip.update", trackId: track.id, clipId: clip.id, clipPatch: { start: value } }], frozen)}/>
        <NumberEdit clipID={clip.id} label="片段长度（tick）" value={clip.length} min={1} max={2147483647} disabled={p.busy} target={target} title={project.title} trackID={track.id}
          commit={(value, frozen) => edit([{ kind: "clip.update", trackId: track.id, clipId: clip.id, clipPatch: { length: value } }], frozen)}/>
        <button className="secondary" disabled={p.busy} onClick={() => void deleteClip(clip)}>删除片段</button>
      </div>
      {project.state.meter?.length && project.state.tempo?.length ? <PianoRoll key={`${project.id}/${track.id}/${clip.id}`} title={project.title} target={{ ...target, track, clip, ppq: project.ppq }} meter={project.state.meter} tempo={project.state.tempo} busy={p.busy} edit={p.edit} shift={p.shift} reportError={p.reportError}/> : null}
    </>}
  </div>;
}
