import type { Note, NoteChange } from "./projectTypes";
import type { GridNote } from "./gridsound";

export type PianoViewport = { pitches: Set<number>; left: number; right: number };
export function inViewport(note: Note, viewport?: PianoViewport) {
  return !viewport || (viewport.pitches.has(note.pitch) && note.start + note.duration >= viewport.left && note.start <= viewport.right);
}
export function gridNote(note: Note, offset: number, ppq: number, selected: boolean): GridNote {
  return { key: note.pitch, when: Math.max(0,note.start-offset)/ppq, duration: (note.duration-Math.max(0,offset-note.start))/ppq, gain: note.velocity/127, pan: 0, lowpass: 1, highpass: 1, selected };
}
export function ghostNotes(changes: NoteChange[], clipID: string, offset: number,end:number, ppq: number, viewport?: PianoViewport) {
  return changes.filter(c => c.clipId === clipID).flatMap(c => (["before", "after"] as const).flatMap(side => {
    const n = c[side];
    if (!n || n.start >= end || n.start+n.duration <= offset || !inViewport(n,viewport)) return [];
    return [{ side, note: n, when: (Math.max(offset,n.start)-offset)/ppq, duration: (Math.min(end,n.start+n.duration)-Math.max(offset,n.start))/ppq }];
  }));
}
