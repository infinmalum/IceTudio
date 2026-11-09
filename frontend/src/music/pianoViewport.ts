import type {Note,Op} from "./projectTypes";
import type {ClipTarget} from "./edits";
export const WINDOW_BEATS=512;
export function intersectNotes(notes:Note[],left:number,right:number,pitches?:Set<number>){return notes.filter(n=>n.start<right&&n.start+n.duration>left&&(!pitches||pitches.has(n.pitch)))}
export function boxSelection(notes:Note[],a:number,b:number,pitchA:number,pitchB:number){const left=Math.min(a,b),right=Math.max(a,b);return notes.filter(n=>n.start<=right&&n.start+n.duration>=left&&n.pitch>=Math.min(pitchA,pitchB)&&n.pitch<=Math.max(pitchA,pitchB)).map(n=>n.id)}
export type NoteClipboard={ppq:number;notes:Omit<Note,"id">[]};
export function copyNotes(target:ClipTarget,ids:string[]):NoteClipboard|null {
 const chosen=new Set(ids),notes=(target.clip.notes??[]).filter(n=>chosen.has(n.id));if(!notes.length)return null;
 const start=Math.min(...notes.map(n=>n.start));return {ppq:target.ppq,notes:notes.map(({id,...n})=>({...n,start:n.start-start}))};
}
export function pasteNotes(target:ClipTarget,clipboard:NoteClipboard,tick:number):Op[]{
 if(!Number.isSafeInteger(tick)||tick<0)throw Error("粘贴位置须为有效整数 tick");
 const notes=clipboard.notes.map(n=>({...n,start:tick+Math.round(n.start/clipboard.ppq*target.ppq),duration:Math.max(1,Math.round(n.duration/clipboard.ppq*target.ppq))}));
 if(!notes.length)return [];
 const end=Math.max(target.clip.length,...notes.map(n=>n.start+n.duration));if(end>2147483647)throw Error("粘贴超出片段长度范围");
 const base={trackId:target.track.id,clipId:target.clip.id};return [...(end>target.clip.length?[{...base,kind:"clip.update",clipPatch:{length:end}}]:[]),{...base,kind:"note.add",notes}];
}
// Explicit in-process clipboard only; never serialized or sent to a model.
export const pianoClipboard:{value:NoteClipboard|null}={value:null};

// Exact coincident glyphs are visually occluded in the upstream DOM as well.
// Keep a representative for each visual state, while selection/edits retain
// every authoritative ID. Removing/moving it reveals the next layer.
export function coincidentNotes(notes:Note[],selected:Set<string>){
 const groups=new Map<string,{note:Note,count:number}>();
 for(const note of notes){const key=`${note.start}/${note.duration}/${note.pitch}/${note.velocity}/${note.channel}/${selected.has(note.id)}`;const group=groups.get(key);groups.set(key,{note,count:(group?.count??0)+1})}
 return [...groups.values()];
}
