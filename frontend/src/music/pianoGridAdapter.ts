import type { ProjectEditPreview } from "../../bindings/icetudio/internal/app/models";
import type { ClipTarget } from "./edits";
import { PAGE_BEATS } from "./edits";
import type { GridElement, GridNote } from "./gridsound";
import { ghostNotes, gridNote, inViewport, pageNotes, type PianoViewport } from "./pianoProjection";

// The only DOM-specific projection adapter. Ghosts never enter vendor data,
// callbacks or selection; authoritative notes always come from the project.
export function renderPianoProjection(p: GridElement, data: Record<string,GridNote>, target: ClipTarget, page: number, selection: Set<string>, preview: ProjectEditPreview | null) {
  const start = page*PAGE_BEATS*target.ppq;
  const notes = pageNotes(target.clip.notes ?? [],page,target.ppq);
  const active = preview?.projectId === target.id && preview.baseRevision === target.revision ? preview : null;
  let viewport: PianoViewport | undefined;
  const area = p.querySelector<HTMLElement>(".gsuiTimewindow-scrollArea");
  if ((notes.length>512 || active) && area?.clientHeight) {
    const bounds=area.getBoundingClientRect(), pitches=new Set<number>();
    p.querySelectorAll<HTMLElement>(".gsui-row[data-midi]").forEach(row => {
      const r=row.getBoundingClientRect();
      if (r.bottom>=bounds.top-40 && r.top<=bounds.bottom+40) pitches.add(Number(row.dataset.midi));
    });
    const origin=p.querySelector(".gsuiTimewindow-mainContent")?.getBoundingClientRect().left ?? bounds.left;
    const px=Number(p.querySelector("gsui-timewindow")?.getAttribute("pxperbeat"))||64;
    viewport={pitches,left:(bounds.left-origin-80)/px*target.ppq+start,right:(bounds.right-origin+80)/px*target.ppq+start};
  }
  const current=new Map(notes.filter(n=>inViewport(n,viewport)).map(n=>[n.id,n]));
  const allIDs=new Set((target.clip.notes??[]).map(n=>n.id));
  for (const id of selection) if (!allIDs.has(id)) selection.delete(id);
  if (p.querySelector('[data-id="preview"]')) p.$removeKey("preview");
  for (const id of Object.keys(data)) if (!current.has(id)) { p.$removeKey(id);delete data[id]; }
  for (const [id,note] of current) {
    const value=gridNote(note,start,target.ppq,selection.has(id)), previous=data[id];
    data[id]=value;
    if (!previous) p.$addKey(id,value);
    else for (const prop of ["key","when","duration","gain","selected"] as const) if (previous[prop]!==value[prop]) p.$changeKeyProp(id,prop,value[prop]);
  }
  p.querySelectorAll("[data-hidden]").forEach(el=>el.removeAttribute("data-hidden"));
  p.querySelectorAll(".piano-ghost").forEach(el=>el.remove());
  if (active) for (const ghost of ghostNotes(active.diff.changes??[],target.clip.id,page,target.ppq,viewport)) {
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
