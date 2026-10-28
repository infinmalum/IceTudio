import type { Note, NoteChange } from "../../bindings/icetudio/internal/project/models";
import { PAGE_BEATS } from "./edits.ts";
import type { GridNote } from "./gridsound";

export type PianoViewport = { pitches: Set<number>; left: number; right: number };
export function pageNotes(notes: Note[], page: number, ppq: number) {
  const start = page * PAGE_BEATS * ppq;
  return notes.filter(n => n.start >= start && n.start < start + PAGE_BEATS * ppq);
}
export function inViewport(note: Note, viewport?: PianoViewport) {
  return !viewport || (viewport.pitches.has(note.pitch) && note.start + note.duration >= viewport.left && note.start <= viewport.right);
}
export function gridNote(note: Note, offset: number, ppq: number, selected: boolean): GridNote {
  return { key: note.pitch, when: (note.start-offset)/ppq, duration: note.duration/ppq, gain: note.velocity/127, pan: 0, lowpass: 1, highpass: 1, selected };
}
export function ghostNotes(changes: NoteChange[], clipID: string, page: number, ppq: number, viewport?: PianoViewport) {
  const offset = page * PAGE_BEATS * ppq, end = offset + PAGE_BEATS * ppq;
  return changes.filter(c => c.clipId === clipID).flatMap(c => (["before", "after"] as const).flatMap(side => {
    const n = c[side];
    if (!n || n.start >= end || n.start+n.duration <= offset || !inViewport(n,viewport)) return [];
    return [{ side, note: n, when: (Math.max(offset,n.start)-offset)/ppq, duration: (Math.min(end,n.start+n.duration)-Math.max(offset,n.start))/ppq }];
  }));
}
