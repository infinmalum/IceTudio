import type { ProjectEditPreview } from "./previewTypes";
import type { ClipTarget } from "./edits";
import { WINDOW_BEATS,intersectNotes,coincidentNotes } from "./pianoViewport";
import type { GridElement, GridNote } from "./gridsound";
import { ghostNotes, gridNote, inViewport, type PianoViewport } from "./pianoProjection";

// The only DOM-specific projection adapter. Ghosts never enter vendor data,
// callbacks or selection; authoritative notes always come from the project.
export function renderPianoProjection(p: GridElement, data: Record<string,GridNote>, target: ClipTarget, origin: number, selection: Set<string>, preview: ProjectEditPreview | null) {
  const start = origin;
  const notes = intersectNotes(target.clip.notes ?? [],start,start+WINDOW_BEATS*target.ppq);
  const active = preview?.projectId === target.id && preview.baseRevision === target.revision ? preview : null;
  let viewport: PianoViewport | undefined;
  const area = p.querySelector<HTMLElement>(".gsuiTimewindow-scrollArea");
  if (area?.clientHeight) {
    const bounds=area.getBoundingClientRect(), pitches=new Set<number>();
    p.querySelectorAll<HTMLElement>(".gsui-row[data-midi]").forEach(row => {
      const r=row.getBoundingClientRect();
      if (r.bottom>=bounds.top-40 && r.top<=bounds.bottom+40) pitches.add(Number(row.dataset.midi));
    });
    const origin=p.querySelector(".gsuiTimewindow-mainContent")?.getBoundingClientRect().left ?? bounds.left;
    const px=Number(p.querySelector("gsui-timewindow")?.getAttribute("pxperbeat"))||64;
    viewport={pitches,left:(bounds.left-origin-80)/px*target.ppq+start,right:(bounds.right-origin+80)/px*target.ppq+start};
  }
  const coincident=coincidentNotes(notes.filter(n=>inViewport(n,viewport)),selection);
  const current=new Map(coincident.map(g=>[g.note.id,g.note]));
  const counts=new Map(coincident.map(g=>[g.note.id,g.count]));
  const allIDs=new Set((target.clip.notes??[]).map(n=>n.id));
  for (const id of selection) if (!allIDs.has(id)) selection.delete(id);
  if (p.querySelector('[data-id="preview"]')) p.$removeKey("preview");
  for (const id of Object.keys(data)) if (!current.has(id)) { p.$removeKey(id);delete data[id]; }
  for (const [id,note] of current) {
    const value=gridNote(note,start,target.ppq,selection.has(id)), previous=data[id];
    value.duration=Math.min(value.duration,WINDOW_BEATS-value.when);
    data[id]=value;
    if (!previous) p.$addKey(id,value);
    else for (const prop of ["key","when","duration","gain","selected"] as const) if (previous[prop]!==value[prop]) p.$changeKeyProp(id,prop,value[prop]);
  }
  const pixels=Number(p.querySelector("gsui-timewindow")?.getAttribute("pxperbeat"))||64;
  p.querySelectorAll<HTMLElement>(".gsuiPianoroll-block[data-id]").forEach(block=>{const note=current.get(block.dataset.id??"");if(!note)return;block.dataset.layers=String(counts.get(note.id)??1);block.title=(counts.get(note.id)??1)>1?`完全重合 ${counts.get(note.id)} 个音符；框选或全选包含全部音符`:"";block.classList.toggle("piano-note-labelled",Number(data[note.id]?.duration??0)*pixels>24);const end=block.lastElementChild as HTMLElement|null;if(end)end.style.display=note.start+note.duration>start+WINDOW_BEATS*target.ppq?"none":"";});
  p.querySelectorAll("[data-hidden]").forEach(el=>el.removeAttribute("data-hidden"));
  p.querySelectorAll(".piano-ghost").forEach(el=>el.remove());
  if (active) for (const ghost of ghostNotes(active.diff.changes??[],target.clip.id,start,start+WINDOW_BEATS*target.ppq,target.ppq,viewport)) {
    const row=p.querySelector<HTMLElement>(`.gsui-row[data-midi="${ghost.note.pitch}"] > div`);
    if (!row) continue;
    const el=document.createElement("span");
    el.className=`piano-ghost piano-ghost-${ghost.side}`;
    el.style.left=`${ghost.when}em`;el.style.width=`${ghost.duration}em`;
    el.setAttribute("aria-hidden","true");
    row.append(el);
  }
  p.dataset.mountedNotes=String(current.size);
}
