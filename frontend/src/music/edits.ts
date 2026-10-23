import type { Clip, Note, Op, Track } from "../../bindings/icetudio/internal/project/models";

export type EditTarget = { id: string; revision: number };
export type ClipTarget = EditTarget & { track: Track; clip: Clip; ppq: number };
export const objectID = (prefix: string) => `${prefix}_${crypto.randomUUID().replace(/-/g, "").slice(0, 12)}`;
export const ticks = (beats: number, ppq: number) => Math.round(beats * ppq);
export const PAGE_BEATS = 16;

// Convert a complete musical intent to one batch. Untouched controls/raw
// events never enter these patches, and extending a clip is atomic with notes.
export function noteIntent(target: ClipTarget, kind: string, args: any[], offset = 0): Op[] {
  const { track, clip, ppq } = target;
  const base = { trackId: track.id, clipId: clip.id };
  const notes = new Map((clip.notes ?? []).map(n => [n.id, n]));
  const chosen = (ids: string[]): Note[] => ids.map(id => {
    const n = notes.get(id);
    if (!n) throw Error("选中的音符已不存在，请刷新后重试。");
    return n;
  });
  let op: Op;
  let end = clip.length;
  if (kind === "add") {
    const [pitch, when, duration] = args;
    const note = { start: offset + ticks(when, ppq), duration: Math.max(1, ticks(duration, ppq)), pitch, velocity: 100, channel: track.channel };
    end = Math.max(end, note.start + note.duration);
    op = { ...base, kind: "note.add", notes: [note] };
  } else if (kind === "clone") {
    const values = chosen(args[0]).map(({ id: _, ...n }) => ({ ...n, start: n.start + ticks(args[1], ppq) }));
    for (const n of values) end = Math.max(end, n.start + n.duration);
    op = { ...base, kind: "note.add", notes: values };
  } else if (kind === "remove") {
    chosen(args[0]);
    op = { ...base, kind: "note.remove", noteIds: args[0] };
  } else if (["move", "cropEnd", "velocity"].includes(kind)) {
    const deltas = chosen(args[0]).map(n => {
      if (kind === "velocity") return { id: n.id, velocity: Math.round(args[1]) };
      if (kind === "cropEnd") {
        const duration = n.duration + ticks(args[1], ppq);
        end = Math.max(end, n.start + duration);
        return { id: n.id, duration };
      }
      const start = n.start + ticks(args[1], ppq);
      end = Math.max(end, start + n.duration);
      return { id: n.id, start, pitch: n.pitch - args[2] };
    });
    op = { ...base, kind: "note.update", noteDeltas: deltas };
  } else throw Error(`暂不支持此编辑：${kind}`);
  if ((op.noteDeltas?.length ?? op.notes?.length ?? op.noteIds?.length ?? 0) === 0) return [];
  return end > clip.length
    ? [{ ...base, kind: "clip.update", clipPatch: { length: end } }, op]
    : [op];
}
