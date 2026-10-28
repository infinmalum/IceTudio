import { useEffect, useMemo, useRef, useState } from "react";
import type { MeterPoint, Op, TempoPoint } from "../../bindings/icetudio/internal/project/models";
import type { ProjectEditPreview } from "../../bindings/icetudio/internal/app/models";
import { loadGridSound, type GridElement, type GridNote } from "../music/gridsound";
import { noteIntent, PAGE_BEATS, type ClipTarget, type EditTarget } from "../music/edits";
import { auditionInstrument } from "../music/audition";
import { meterIndex } from "../music/timeMap";
import { renderPianoProjection } from "../music/pianoGridAdapter";
import { pageNotes } from "../music/pianoProjection";
import { useMusicField } from "./useMusicInputs";
import { useCloseBlocker } from "../shared/closeGuard";

export type PianoRollProps = {
  title: string; target: ClipTarget; busy: boolean;
  meter: MeterPoint[]; tempo: TempoPoint[];
  edit: (target: EditTarget, ops: Op[]) => Promise<boolean>;
  shift: (direction: "undo" | "redo") => void;
  reportError: (error: unknown) => void;
  snap: number; preview?: ProjectEditPreview | null;
};

export function usePianoRoll({ title,target,meter,busy,edit,shift,reportError,snap,preview=null }: PianoRollProps) {
  const mount = useRef<HTMLDivElement>(null);
  const latest = useRef({ target, busy, edit, shift, reportError, preview });
  latest.current = { target, busy, edit, shift, reportError, preview };
  const [loadError, setLoadError] = useState("");
  const [ready, setReady] = useState(false);
  const [requestedPage, setPage] = useState(0);
  const pageCount = Math.max(1, Math.ceil(target.clip.length / (PAGE_BEATS * target.ppq)));
  const page = Math.min(requestedPage, pageCount - 1);
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
  const offset = page * PAGE_BEATS * target.ppq;
  const visible = pageNotes(target.clip.notes ?? [],page,target.ppq);

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
      p.$changeDuration(PAGE_BEATS);
      const timewindow = p.querySelector("gsui-timewindow")!;
      const zoomObserver = new MutationObserver(() => {
        const value = Number(timewindow.getAttribute("pxperbeat"));
        if (!disposed && Number.isFinite(value) && value >= 16 && value <= 200) setZoom(value);
      });
      zoomObserver.observe(timewindow, { attributes: true, attributeFilter: ["pxperbeat"] });
      const data: Record<string, GridNote> = {};
      p.$setData(data);
      const render = () => {
        if (disposed || gesture.current) return;
        const started = performance.now();
        renderPianoProjection(p,data,latest.current.target,page,selection.current,latest.current.preview);
        p.dataset.projectionMs = String(performance.now() - started);
        setSelected([...selection.current]);
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
          if (kind === "add" && (args[1] < 0 || args[1] >= PAGE_BEATS)) throw Error("请先翻页到目标位置，再添加音符。");
          if (["move", "cropEnd", "remove", "clone"].includes(kind) && Array.isArray(args[0]) && args[0].length && args[0].every((id: string) => selection.current.has(id))) args[0] = [...selection.current];
          const ops = noteIntent(frozen, kind, args, page * PAGE_BEATS * frozen.ppq);
          if (ops.length) void edit(frozen, ops).finally(render);
        } catch (e) { reportError(e); }
        queueMicrotask(render);
      };
      action.current = command;
      p.$setCallbacks(command);
      const cancel = () => {
        gesture.current = null;
        p.$cancel();
        for (const el of [p, ...p.querySelectorAll("*")]) {
          // Release any capture held by the block manager's root.
          if (pointerID !== null && el.hasPointerCapture(pointerID)) el.releasePointerCapture(pointerID);
        }
        pointerID = null;
        queueMicrotask(render);
      };
      let pointerID: number | null = null;
      const down = (e: PointerEvent) => {
        // The host ruler is display-only. Suppress the vendor's separate
        // cursor/loop state, which is not the native playback transport.
        if ((e.target as Element).closest(".gsuiTimewindow-time")) { e.preventDefault(); e.stopImmediatePropagation(); return; }
        if (latest.current.busy) { e.preventDefault(); e.stopImmediatePropagation(); return; }
        // The vendor keyboard includes four extra rows; do not let those start
        // a gesture even though the host would also reject the resulting pitch.
        const row = (e.target as Element).closest<HTMLElement>("[data-midi]");
        if (row && Number(row.dataset.midi) > 127) { e.stopImmediatePropagation(); return; }
        if (pointerID !== null) { e.stopImmediatePropagation(); return; }
        pointerID = e.pointerId;
        gesture.current = structuredClone(latest.current.target);
      };
      const up = () => {
        // Upstream's body listener commits first; release the frozen snapshot
        // only after it has generated the complete batch.
        queueMicrotask(() => { gesture.current = null; pointerID = null; render(); });
      };
      const keydown = (e: KeyboardEvent) => {
        if (e.isComposing || (e.target as HTMLElement).closest("input,textarea,select,[contenteditable=true]")) { e.stopImmediatePropagation(); return; }
        if (e.key === "Escape") { cancel(); e.stopImmediatePropagation(); return; }
        if (latest.current.busy || gesture.current) { e.stopImmediatePropagation(); return; }
        const mod = e.ctrlKey || e.metaKey;
        if (mod && e.key.toLowerCase() === "z") {
          e.preventDefault(); e.stopImmediatePropagation(); latest.current.shift(e.shiftKey ? "redo" : "undo"); return;
        }
        const ids = [...selection.current];
        if (mod && ["a", "d"].includes(e.key.toLowerCase())) {
          e.preventDefault(); e.stopImmediatePropagation();
          selection.current = e.key.toLowerCase() === "a" ? new Set((latest.current.target.clip.notes ?? []).filter(n => n.start >= page * PAGE_BEATS * latest.current.target.ppq && n.start < (page + 1) * PAGE_BEATS * latest.current.target.ppq).map(n => n.id)) : new Set(); render(); return;
        }
        if (ids.length && ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) {
          e.preventDefault(); e.stopImmediatePropagation();
          const step = Number(p.querySelector("gsui-timewindow")?.getAttribute("step") ?? 1) / 4;
          command("move", ids, e.key === "ArrowLeft" ? -step : e.key === "ArrowRight" ? step : 0, e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0);
        } else if (ids.length && mod && e.key.toLowerCase() === "b") {
          e.preventDefault(); e.stopImmediatePropagation();
          const ns = latest.current.target.clip.notes?.filter(n => selection.current.has(n.id)) ?? [];
          const span = Math.max(...ns.map(n => n.start + n.duration)) - Math.min(...ns.map(n => n.start));
          command("clone", ids, span / latest.current.target.ppq);
        }
      };
      p.addEventListener("pointerdown", down, true);
      const wheel = (e: WheelEvent) => {
        if (e.ctrlKey && (gesture.current || latest.current.busy)) { e.preventDefault(); e.stopImmediatePropagation(); }
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
      const scroll = () => { if (!scrollFrame) scrollFrame = requestAnimationFrame(() => { scrollFrame = 0; render(); }); };
      scrollArea?.addEventListener("scroll", scroll, { passive: true });
      render(); setReady(true);
      p.querySelector("gsui-timewindow")?.setAttribute("pxperbeat", String(zoom));
      p.querySelector("gsui-timewindow")?.setAttribute("step", String(snap));
      p.toggleAttribute("disabled", latest.current.busy);
      // The upstream helper scrolls the custom-element wrapper; the actual
      // scroll container is nested. Center only on mount/page change, after
      // layout, so imported notes are visible without scrolling from MIDI 131.
      requestAnimationFrame(() => {
        if (disposed) return;
        const area = p.querySelector<HTMLElement>(".gsuiTimewindow-scrollArea");
        const pitch = latest.current.target.clip.notes?.find(n => n.start >= page * PAGE_BEATS * latest.current.target.ppq && n.start < (page + 1) * PAGE_BEATS * latest.current.target.ppq)?.pitch ?? 60;
        const row = p.querySelector<HTMLElement>(`.gsui-row[data-midi="${pitch}"]`);
        if (area && row) {
          area.scrollTo({ top: area.scrollTop + row.getBoundingClientRect().top - area.getBoundingClientRect().top - area.clientHeight / 2, left: 0, behavior: "instant" });
          render();
        }
      });
      cleanup = () => {
        disposed = true; gesture.current = null; p.$cancel();
        zoomObserver.disconnect();
        cancelAnimationFrame(scrollFrame); scrollArea?.removeEventListener("scroll", scroll);
        window.removeEventListener("pointerup", up);
        window.removeEventListener("pointercancel", cancel);
        window.removeEventListener("blur", cancel);
        p.remove(); setRulerSlot(null); grid.current = null; applyView.current = () => {}; action.current = () => {};
      };
    }).catch(e => { if (!disposed) setLoadError(String(e)); });
    return () => { disposed = true; cleanup(); };
  }, [target.id, target.track.id, target.clip.id, page]);

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
    tw?.setAttribute("step", String(snap));
  }, [zoom, snap]);

  const absoluteStart = target.clip.start + offset;
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
        void auditionInstrument(target.track.program, target.track.channel, [midi]);
    };
    host.addEventListener("pointerdown", down);
    return () => host.removeEventListener("pointerdown", down);
  }, [target.track.program, target.track.channel]);
  return {
    mount,rulerSlot,ready,loadError,page,pageCount,setPage,zoom,setZoom,absoluteStart,mapIndex,selected,visible,velocityField,velocityIDs,velocity,
    selectPage: () => action.current("selection",visible.map(n=>n.id)),
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
