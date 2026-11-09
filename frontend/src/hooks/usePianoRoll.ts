import { useMusicServices } from "../music/services";
import type { TransformSpec } from "../music/projectTypes";
import { useEffect, useMemo, useRef, useState } from "react";
import type { MeterPoint, Op, TempoPoint } from "../music/projectTypes";
import type { ProjectEditPreview } from "../music/previewTypes";
import { loadGridSound, type GridElement, type GridNote } from "../music/gridsound";
import { noteIntent, type ClipTarget, type EditTarget } from "../music/edits";

import { meterIndex } from "../music/timeMap";
import { renderPianoProjection } from "../music/pianoGridAdapter";
import { WINDOW_BEATS,intersectNotes,boxSelection,copyNotes,pasteNotes,pianoClipboard } from "../music/pianoViewport";
import { useMusicField } from "./useMusicInputs";
import { useCloseBlocker } from "../shared/musicClose";

export type PianoRollProps = {
  title: string; target: ClipTarget; busy: boolean;
  meter: MeterPoint[]; tempo: TempoPoint[];
  edit: (target: EditTarget, ops: Op[]) => Promise<boolean>;
  shift: (direction: "undo" | "redo") => void;
  reportError: (error: unknown) => void;
  navigate?:(name:string,tick?:number,end?:number)=>void;
  snap: number; preview?: ProjectEditPreview | null;
};

export function usePianoRoll({ title,target,meter,busy,edit,shift,reportError,snap,navigate,preview=null }: PianoRollProps) {
  const ProjectService = useMusicServices();
  const mount = useRef<HTMLDivElement>(null);
  const latest = useRef({ target, busy, edit, shift, reportError, preview,navigate,snap });
  latest.current = { target, busy, edit, shift, reportError, preview,navigate,snap };
  const [transformBusy, setTransformBusy] = useState(false);
  const transformLock = useRef(false);
  const [quantizeUnit, setQuantizeUnit] = useState("1/16");
  const [loadError, setLoadError] = useState("");
  const [ready, setReady] = useState(false);
  const origin=useRef(0),cursor=useRef(0);
  const [viewport,setViewport]=useState({start:0,beats:16,inset:0,controlBeats:16});
  const locate=useRef<(tick:number)=>void>(()=>{});
  const keys=useRef({quantize:"Q",duplicate:"B"});
  const shortcuts=useRef<(key:string)=>void>(()=>{});
  const transport=useRef<(tick:number,playing:boolean,follow:string)=>void>(()=>{});
  const followPaused=useRef(false);
  const [rulerSlot, setRulerSlot] = useState<HTMLElement | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [zoom, setZoom] = useState(64);
  const velocityField = useMusicField({ label: "音符力度", title, target: { id: target.id, revision: target.revision }, trackID: target.track.id, clipID: target.clip.id, noteIDs: selected }, "100");
  const velocity = Number(velocityField.raw);
  const velocityIDs = velocityField.entry?.noteIDs ?? selected;
  const applyView = useRef<() => void>(() => {});
  const grid = useRef<GridElement | null>(null);
  const gesture = useRef<ClipTarget | null>(null);
  useCloseBlocker(() => gesture.current ? { kind: "busy", message: "音符手势尚未结束，请完成编辑或按 Escape 取消后再退出。" } : null);
  const action = useRef<(kind: string, ...args: any[]) => void>(() => {});
  const selection = useRef(new Set<string>());
  const inputTarget = useRef<ClipTarget | null>(null);
  const inputSelection = useRef<string[]>([]);
  const visible = intersectNotes(target.clip.notes??[],viewport.start,viewport.start+viewport.beats*target.ppq);

  useEffect(() => {
    let disposed = false;
    let cleanup = () => {};
    setLoadError(""); setReady(false);
    selection.current.clear(); setSelected([]);
    void loadGridSound().then(() => {
      if (disposed || !mount.current) return;
      const p = document.createElement("gsui-pianoroll") as GridElement;
      grid.current = p;
      p.setAttribute("tabindex", "0");
      p.setAttribute("icetudio-hosted", "");
      p.setAttribute("aria-label", "钢琴卷帘音符编辑区");
      mount.current.append(p);
      p.$octaves(0, 11);
      // Keep upstream row indices stable; host capture blocks the extra
      // four rows, and the authoritative model also rejects pitches >127.
      p.querySelectorAll<HTMLElement>(".gsui-row[data-midi]").forEach(row => {
        if (Number(row.dataset.midi) > 127) row.setAttribute("inert", "");
      });
      p.$timedivision("1/4");
      const slot = document.createElement("div");
      slot.className = "music-ruler-slot";
      p.querySelector(".gsuiTimewindow-time")!.append(slot);
      setRulerSlot(slot);
      p.$changeDuration(WINDOW_BEATS);
      const body=p.querySelector<HTMLElement>(".gsuiTimewindow-mainBody");
      if(body)body.style.width=`${WINDOW_BEATS*zoom}px`;
      const timewindow = p.querySelector("gsui-timewindow")!;
      const zoomObserver = new MutationObserver(() => {
        const value = Number(timewindow.getAttribute("pxperbeat"));
        if (!disposed && Number.isFinite(value) && value >= 16 && value <= 200) {setZoom(value);if(body)body.style.width=`${WINDOW_BEATS*value}px`}
      });
      zoomObserver.observe(timewindow, { attributes: true, attributeFilter: ["pxperbeat"] });
      const data: Record<string, GridNote> = {};
      p.$setData(data);
      const render = () => {
        if (disposed || gesture.current) return;
        const started = performance.now();
        renderPianoProjection(p,data,latest.current.target,origin.current,selection.current,latest.current.preview);
        p.dataset.projectionMs = String(performance.now() - started);p.dataset.windowOrigin=String(origin.current);
        setSelected(old=>old.length===selection.current.size&&old.every(id=>selection.current.has(id))?old:[...selection.current]);
      };
      applyView.current = render;
      const command = (kind: string, ...args: any[]) => {
        if (["selection", "unselection", "unselectionOne"].includes(kind)) {
          if (kind === "selection") (args[0] as string[]).forEach(id => selection.current.add(id));
          if (kind === "unselection") selection.current.clear();
          if (kind === "unselectionOne") selection.current.delete(args[0]);
          queueMicrotask(render);
          return;
        }
        const { target: currentTarget, busy, edit, reportError } = latest.current;
        if (busy) { queueMicrotask(render); return; }
        const frozen = gesture.current ?? currentTarget;
        try {
          if (kind === "add" && (args[1] < 0 || args[1] >= WINDOW_BEATS)) throw Error("添加位置超出当前编辑窗口，请先滚动到目标位置。");
          if (["move", "cropEnd", "remove", "clone"].includes(kind) && Array.isArray(args[0]) && args[0].length && args[0].every((id: string) => selection.current.has(id))) args[0] = [...selection.current];
          const ops = noteIntent(frozen, kind, args, origin.current);
          if (ops.length) void edit(frozen, ops).finally(render);
        } catch (e) { reportError(e); }
        queueMicrotask(render);
      };
      action.current = command;
      p.$setCallbacks(command);
      const cancel = () => {
        gesture.current = null;marquee=null;rulerDrag=null;cancelAnimationFrame(autoFrame);
        p.$cancel();
        for (const el of [p, ...p.querySelectorAll("*")]) {
          // Release any capture held by the block manager's root.
          if (pointerID !== null && el.hasPointerCapture(pointerID)) el.releasePointerCapture(pointerID);
        }
        pointerID = null;
        queueMicrotask(render);
      };
      let pointerID: number | null = null;
      let marquee:{tick:number,pitch:number,selection:string[]}|null=null;
      let rulerDrag:{tick:number,x:number}|null=null;
      let pointerX=0;
      const pointerTick=(x:number)=>Math.max(0,Math.round(origin.current+(x-(p.querySelector(".gsuiTimewindow-mainContent")?.getBoundingClientRect().left??0))/Number(timewindow.getAttribute("pxperbeat")??64)*latest.current.target.ppq));
      const pointerPitch=(y:number)=>{let nearest=60,dist=Infinity;p.querySelectorAll<HTMLElement>(".gsui-row[data-midi]").forEach(row=>{const r=row.getBoundingClientRect(),d=Math.abs(y-(r.top+r.height/2));if(d<dist){nearest=Number(row.dataset.midi);dist=d}});return nearest};
      const down = (e: PointerEvent) => {
        // The host ruler is display-only. Suppress the vendor's separate
        // cursor/loop state, which is not the native playback transport.
        if ((e.target as Element).closest(".gsuiTimewindow-time")) {
          const tick=pointerTick(e.clientX);cursor.current=tick;
          if(e.detail===2)latest.current.navigate?.("seek",latest.current.target.clip.start+tick);
          else rulerDrag={tick,x:e.clientX};
          e.preventDefault();e.stopImmediatePropagation();return;
        }
        if (latest.current.busy) { e.preventDefault(); e.stopImmediatePropagation(); return; }
        // The vendor keyboard includes four extra rows; do not let those start
        // a gesture even though the host would also reject the resulting pitch.
        const row = (e.target as Element).closest<HTMLElement>("[data-midi]");
        if (row && Number(row.dataset.midi) > 127) { e.stopImmediatePropagation(); return; }
        if (pointerID !== null) { e.stopImmediatePropagation(); return; }
        pointerID = e.pointerId;pointerX=e.clientX;autoFrame=requestAnimationFrame(autoscroll);
        cursor.current=pointerTick(e.clientX);
        if(e.shiftKey)marquee={tick:cursor.current,pitch:pointerPitch(e.clientY),selection:[...selection.current]};
        timewindow.setAttribute("step",String(e.altKey?4/latest.current.target.ppq:latest.current.snap*4));
        gesture.current = structuredClone(latest.current.target);
      };
      const up = (event:PointerEvent) => {
        if(rulerDrag){const start=rulerDrag.tick,end=pointerTick(event.clientX);if(Math.abs(event.clientX-rulerDrag.x)>4&&start!==end)latest.current.navigate?.("loop",latest.current.target.clip.start+Math.min(start,end),latest.current.target.clip.start+Math.max(start,end));rulerDrag=null;}
        if(marquee){const box=marquee;marquee=null;const ids=boxSelection(gesture.current?.clip.notes??latest.current.target.clip.notes??[],box.tick,pointerTick(event.clientX),box.pitch,pointerPitch(event.clientY));queueMicrotask(()=>{selection.current=new Set([...box.selection,...ids]);render()});}

        // Upstream's body listener commits first; release the frozen snapshot
        // only after it has generated the complete batch.
        queueMicrotask(() => { gesture.current = null; pointerID = null;cancelAnimationFrame(autoFrame); updateViewport(); });
      };
      const keydown = (e: KeyboardEvent) => {
        if (e.isComposing || (e.target as HTMLElement).closest("input,textarea,select,[contenteditable=true]")) { e.stopImmediatePropagation(); return; }
        if (e.key === "Escape") { cancel();e.preventDefault(); e.stopImmediatePropagation(); return; }
        if(e.code==="Space"||e.key==="Home"){e.preventDefault();e.stopImmediatePropagation();if(e.key==="Home"){if(!gesture.current)locate.current(0);latest.current.navigate?.("home")}else latest.current.navigate?.("toggle");return}
        if (latest.current.busy || gesture.current) { e.stopImmediatePropagation(); return; }
        const mod = e.ctrlKey || e.metaKey;
        if(!mod&&!e.altKey&&[keys.current.quantize,keys.current.duplicate].includes(e.key.toUpperCase())){e.preventDefault();e.stopImmediatePropagation();shortcuts.current(e.key.toUpperCase());return}


        if(mod&&["c","v"].includes(e.key.toLowerCase())){e.preventDefault();e.stopImmediatePropagation();if(e.key.toLowerCase()==="c")pianoClipboard.value=copyNotes(latest.current.target,[...selection.current]);else if(pianoClipboard.value){const frozen=structuredClone(latest.current.target);try{void latest.current.edit(frozen,pasteNotes(frozen,pianoClipboard.value,cursor.current)).finally(render)}catch(error){latest.current.reportError(error)}}return}

        if (mod && e.key.toLowerCase() === "z") {
          e.preventDefault(); e.stopImmediatePropagation(); latest.current.shift(e.shiftKey ? "redo" : "undo"); return;
        }
        const ids = [...selection.current];
        if (mod && ["a", "d"].includes(e.key.toLowerCase())) {
          e.preventDefault(); e.stopImmediatePropagation();
          selection.current = e.key.toLowerCase() === "a" ? new Set((latest.current.target.clip.notes ?? []).map(n => n.id)) : new Set(); render(); return;
        }
        if (ids.length && ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) {
          e.preventDefault(); e.stopImmediatePropagation();
          const step = e.altKey?1/latest.current.target.ppq:Number(timewindow.getAttribute("step") ?? 4) / 4;
          command("move", ids, e.key === "ArrowLeft" ? -step : e.key === "ArrowRight" ? step : 0, e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0);
        } else if (ids.length && ["Delete","Backspace"].includes(e.key)) {e.preventDefault();e.stopImmediatePropagation();command("remove",ids);
        } else if (ids.length && mod && e.key.toLowerCase() === "b") {
          e.preventDefault(); e.stopImmediatePropagation();
          const ns = latest.current.target.clip.notes?.filter(n => selection.current.has(n.id)) ?? [];
          const span = Math.max(...ns.map(n => n.start + n.duration)) - Math.min(...ns.map(n => n.start));
          command("clone", ids, span / latest.current.target.ppq);
        }
      };
      p.addEventListener("pointerdown", down, true);
      const wheel = (e: WheelEvent) => {
        if(e.ctrlKey && (gesture.current || latest.current.busy)) {e.preventDefault();e.stopImmediatePropagation();return}
        followPaused.current=true;latest.current.navigate?.("manual");
        if(e.shiftKey&&!e.ctrlKey){const area=p.querySelector<HTMLElement>(".gsuiTimewindow-scrollArea");if(area){area.scrollLeft+=e.deltaY||e.deltaX;e.preventDefault();e.stopImmediatePropagation()}}

      };
      p.addEventListener("wheel", wheel, { capture: true, passive: false });
      p.addEventListener("keydown", keydown, true);
      p.addEventListener("contextmenu", e => e.preventDefault());
      window.addEventListener("pointerup", up);
      window.addEventListener("pointercancel", cancel);
      window.addEventListener("blur", cancel);
      // Imported file drag/drop uses the native import button, not upstream's
      // unvalidated MIDI reader.
      p.ondrop = e => { e.preventDefault(); e.stopPropagation(); };
      let scrollFrame = 0;
      const scrollArea = p.querySelector<HTMLElement>(".gsuiTimewindow-scrollArea");
      const updateViewport=()=>{
        if(!scrollArea)return;
        const ppq=latest.current.target.ppq,px=Number(timewindow.getAttribute("pxperbeat"))||64;
        if(!gesture.current){
          const left=origin.current+scrollArea.scrollLeft/px*ppq;
          if(scrollArea.scrollLeft>WINDOW_BEATS*px-scrollArea.clientWidth*2 || scrollArea.scrollLeft<scrollArea.clientWidth&&origin.current>0){
            const next=Math.max(0,Math.floor(left/ppq/128-1)*128*ppq);
            if(next!==origin.current){origin.current=next;scrollArea.scrollLeft=(left-next)/ppq*px;}
          }
        }
        const nextStart = Math.round((origin.current + scrollArea.scrollLeft / px * ppq) * 32) / 32;
        const nextBeats = Math.max(1, scrollArea.clientWidth / px);
        const inset = p.querySelector<HTMLElement>(".gsuiTimewindow-panel")?.offsetWidth ?? 0;
        const controlBeats = Math.max(1 / ppq, (scrollArea.clientWidth - inset) / px);
        setViewport(old => old.start === nextStart && old.beats === nextBeats &&
          old.inset === inset && old.controlBeats === controlBeats ? old :
          {start: nextStart, beats: nextBeats, inset, controlBeats});
        slot.style.left = `${scrollArea.scrollLeft}px`;
        render();
      };
      const scroll = () => { if (!scrollFrame) scrollFrame = requestAnimationFrame(() => { scrollFrame = 0;updateViewport() }); };
      locate.current=(tick)=>{if(gesture.current)return;const ppq=latest.current.target.ppq,px=Number(timewindow.getAttribute("pxperbeat"))||64;cursor.current=Math.max(0,Math.min(2147483647,Math.round(tick)));origin.current=Math.max(0,Math.floor(cursor.current/ppq/128-1)*128*ppq);if(scrollArea)scrollArea.scrollLeft=(cursor.current-origin.current)/ppq*px;render();updateViewport()};
      const playhead=document.createElement("div");playhead.className="piano-playhead";p.querySelector(".gsuiTimewindow-mainContent")?.append(playhead);
      transport.current=(absolute,playing,follow)=>{const tick=absolute-latest.current.target.clip.start,px=Number(timewindow.getAttribute("pxperbeat"))||64;playhead.hidden=!playing||tick<0;playhead.style.left=`${(tick-origin.current)/latest.current.target.ppq*px}px`;
        if(!scrollArea||gesture.current||!playing)return;
        if(follow!=="关闭"&&follow){followPaused.current=false;const local=(tick-origin.current)/latest.current.target.ppq*px;if(follow==="连续跟随"||local<scrollArea.scrollLeft||local>scrollArea.scrollLeft+scrollArea.clientWidth-80){locate.current(Math.max(0,tick-(follow==="连续跟随"?scrollArea.clientWidth/px*latest.current.target.ppq/3:0)))}}
      };
      window.addEventListener("resize",scroll);
      const move=(e:PointerEvent)=>{pointerX=e.clientX};window.addEventListener("pointermove",move);
      let autoFrame=0;
      const autoscroll=()=>{if(gesture.current&&scrollArea){const bounds=scrollArea.getBoundingClientRect();if(pointerX>bounds.right-25)scrollArea.scrollLeft+=12;if(pointerX<bounds.left+80)scrollArea.scrollLeft-=12;}if(gesture.current)autoFrame=requestAnimationFrame(autoscroll)};
      scrollArea?.addEventListener("scroll", scroll, { passive: true });
      render(); setReady(true);
      p.querySelector("gsui-timewindow")?.setAttribute("pxperbeat", String(zoom));
      p.querySelector("gsui-timewindow")?.setAttribute("step", String(snap*4));
      p.toggleAttribute("disabled", latest.current.busy);
      // The upstream helper scrolls the custom-element wrapper; the actual
      // scroll container is nested. Center only on mount/clip change, after
      // layout, so imported notes are visible without scrolling from MIDI 131.
      requestAnimationFrame(() => {
        if (disposed) return;
        const area = p.querySelector<HTMLElement>(".gsuiTimewindow-scrollArea");
        const pitch = latest.current.target.clip.notes?.[0]?.pitch ?? 60;
        const row = p.querySelector<HTMLElement>(`.gsui-row[data-midi="${pitch}"]`);
        if (area && row) {
          area.scrollTo({ top: area.scrollTop + row.getBoundingClientRect().top - area.getBoundingClientRect().top - area.clientHeight / 2, left: 0, behavior: "instant" });
          updateViewport();
        }
      });
      cleanup = () => {
        disposed = true; gesture.current = null; p.$cancel();
        zoomObserver.disconnect();window.removeEventListener("resize",scroll);cancelAnimationFrame(autoFrame);window.removeEventListener("pointermove",move);
        cancelAnimationFrame(scrollFrame); scrollArea?.removeEventListener("scroll", scroll);
        window.removeEventListener("pointerup", up);
        window.removeEventListener("pointercancel", cancel);
        window.removeEventListener("blur", cancel);
        p.remove(); setRulerSlot(null); grid.current = null; applyView.current = () => {}; action.current = () => {};
      };
    }).catch(e => { if (!disposed) setLoadError(String(e)); });
    return () => { disposed = true; cleanup(); };
  }, [target.id, target.track.id, target.clip.id]);

  useEffect(() => { applyView.current(); }, [target,preview]);
  useEffect(() => {
    const p = grid.current;
    if (!p) return;
    p.toggleAttribute("disabled", busy);
  }, [busy]);
  // Also applied after async component loading via the update effect below.
  useEffect(() => {
    if (!grid.current) return;
    const tw = grid.current.querySelector("gsui-timewindow");
    tw?.setAttribute("pxperbeat", String(zoom));
    tw?.setAttribute("step", String(snap*4));
  }, [zoom, snap]);

  const absoluteStart = target.clip.start + viewport.start;
  const mapIndex = useMemo(() => meterIndex(target.ppq, meter), [target.ppq, meter]);
  // 点击左侧琴键直接试听当前轨道音色（P2 工作区契约 §7，同一试听 API）。
  useEffect(() => {
    const host = mount.current;
    if (!host) return;
      const down = (e: PointerEvent) => {
      if (e.button !== 0) return;
      const key = (e.target as Element | null)?.closest?.(".gsuiKeys-key[data-midi]");
      const midi = Number(key?.getAttribute("data-midi"));
      if (Number.isInteger(midi) && midi >= 0 && midi <= 127)
        void ProjectService.Audition({program:target.track.program,bank:target.track.channel===9?128:0,keys:[midi],velocity:100,durationMs:500}).catch(() => {});
    };
    host.addEventListener("pointerdown", down);
    return () => host.removeEventListener("pointerdown", down);
  }, [target.track.program, target.track.channel]);
  async function transformNotes(spec: TransformSpec) {
    if (busy || !ready || gesture.current || transformLock.current || !selection.current.size) return;
    const frozen = structuredClone(target);
    const ids = [...selection.current];
    transformLock.current = true; setTransformBusy(true);
    try {
      const ops = await ProjectService.PrepareNoteTransform(frozen.id, frozen.revision, frozen.clip.id, ids, spec);
      if (!ops?.length) throw Error("变换没有生成可提交的修改");
      await edit(frozen, ops);
    } catch (e) { reportError(e); }
    finally { transformLock.current = false; setTransformBusy(false); }
  }
  shortcuts.current=(key)=>{if(key===keys.current.quantize)void transformNotes({quantize:{unit:quantizeUnit,strength:100}});else {const ns=target.clip.notes?.filter(n=>selection.current.has(n.id))??[];if(ns.length)action.current("clone",[...selection.current],(Math.max(...ns.map(n=>n.start+n.duration))-Math.min(...ns.map(n=>n.start)))/target.ppq)}};
  return {
    keymap:(quantize:string,duplicate:string)=>{if(quantize!==duplicate&&/^[QWERTBL]$/.test(quantize)&&/^[QWERTBL]$/.test(duplicate))keys.current={quantize,duplicate}},
    currentSelection: () => [...selection.current],
    gestureActive: () => gesture.current !== null,
    transformBusy, quantizeUnit, setQuantizeUnit,
    transpose: (semitones: number) => void transformNotes({ transpose: { semitones } }),
    quantize: () => void transformNotes({ quantize: { unit: quantizeUnit, strength: 100 } }),
    quantizeTo: (unit: string) => void transformNotes({ quantize: { unit, strength: 100 } }),
    mount,rulerSlot,ready,loadError,zoom,setZoom,absoluteStart,mapIndex,selected,visible,velocityField,velocityIDs,velocity,viewport,
    locate:(tick:number)=>locate.current(tick),playhead:(tick:number,playing:boolean,follow:string)=>transport.current(tick,playing,follow),
    copy:()=>{pianoClipboard.value=copyNotes(target,[...selection.current])},paste:()=>{if(pianoClipboard.value){const frozen=structuredClone(target);void edit(frozen,pasteNotes(frozen,pianoClipboard.value,cursor.current)).catch(reportError)}},
    selectAll: () => action.current("selection",(target.clip.notes??[]).map(n=>n.id)),
    clearSelection: () => action.current("unselection"),
    removeNotes: () => action.current("remove",selected),
    cloneNotes: () => {
      const ns=target.clip.notes?.filter(n=>selection.current.has(n.id))??[];
      const span=Math.max(...ns.map(n=>n.start+n.duration))-Math.min(...ns.map(n=>n.start));
      action.current("clone",selected,span/target.ppq);
    },
    focusVelocity: () => { inputTarget.current=structuredClone(target);inputSelection.current=[...selection.current]; },
    changeVelocity: (raw: string) => velocityField.change(raw,{label:"音符力度",title,target:inputTarget.current??target,trackID:target.track.id,clipID:target.clip.id,noteIDs:inputSelection.current}),
    applyVelocity: () => {
      const frozen=velocityField.entry?.target??inputTarget.current??target;
      void edit(frozen,[{kind:"note.update",trackId:target.track.id,clipId:target.clip.id,noteDeltas:velocityIDs.map(id=>({id,velocity}))}]).then(ok=>{if(ok)velocityField.clear()});
      inputTarget.current=null;
    },
  };
}
