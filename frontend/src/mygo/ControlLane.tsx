import { useMemo, useRef, useState, type PointerEvent } from "react";
import { useCloseBlocker } from "../shared/musicClose";
import type { ControlEvent, EventSpec, Note, Op } from "../music/projectTypes";

type Props = {
  clipId: string;
  trackId: string;
  revision: number;
  channel: number;
  length: number;
  events: ControlEvent[] | null;
  notes: Note[] | null;
  start: number;
  span: number;
  inset: number;
  grid: number;
  disabled: boolean;
  submit: (ops: Op[]) => void;
};

type Point = { tick: number; value: number };
type Stroke = {
  pointer: number;
  revision: number;
  clipId: string;
  start: number;
  span: number;
  event?: ControlEvent;
  initial: Point;
  tool: string;
  moved?: boolean;
};

export function ControlLane(props: Props) {
  const [kind, setKind] = useState("cc");
  const [controller, setController] = useState(64);
  const [channel, setChannel] = useState(props.channel);
  const [selection, setSelection] = useState<string[]>([]);
  const [tool, setTool] = useState("point");
  const [offset, setOffset] = useState(0);
  const [pending, setPending] = useState<Point | null>(null);
  const stroke = useRef<Stroke | null>(null);
  useCloseBlocker(() => stroke.current ? {
    kind: "busy",
    message: "控制事件的绘制手势尚未松开",
  } : null);
  const limit = kind === "pitchBend" ? 16383 : 127;
  const matching = useMemo(() => kind === "velocity"
    ? (props.notes ?? []).filter(n => n.channel === channel).map(n => ({
        id: n.id, tick: n.start, value: n.velocity, channel: n.channel, kind,
      }))
    : (props.events ?? []).filter(event => event.kind === kind && event.channel === channel &&
        (kind === "pitchBend" || (event.controller ?? 0) === controller)),
    [props.events, props.notes, kind, channel, controller]);
  const visible = useMemo(() => matching.filter(e => e.tick >= props.start && e.tick < props.start + props.span),
    [matching, props.start, props.span]);
  const selected = matching.filter(e => selection.includes(e.id));
  const valid = !props.disabled && props.start < props.length && Number.isInteger(channel) && channel >= 0 && channel < 16 &&
    (kind !== "cc" || Number.isInteger(controller) && controller >= 0 && controller <= 127);
  const point = (e: PointerEvent<SVGSVGElement>, origin: number, span: number): Point => {
    const r = e.currentTarget.getBoundingClientRect();
    const tick = origin + Math.min(1, Math.max(0, (e.clientX - r.left) / Math.max(1, r.width))) * span;
    return {
      tick: Math.max(0, Math.min(props.length - 1, Math.round(Math.round(tick / props.grid) * props.grid))),
      value: Math.round((1 - Math.min(1, Math.max(0, (e.clientY - r.top) / Math.max(1, r.height)))) * limit),
    };
  };
  const cancel = () => { stroke.current = null; setPending(null); };
  const save = (points: (Point & {id?: string})[], update = false) => {
    if (kind === "velocity") {
      const noteDeltas = points.flatMap(p => p.id ? [{id:p.id,velocity:Math.max(1,p.value)}] : []);
      if (noteDeltas.length) props.submit([{kind:"note.update", clipId:props.clipId, trackId:props.trackId, noteDeltas}]);
      return;
    }
    const events: EventSpec[] = points.map(at => ({ tick: at.tick, value: at.value, kind, channel,
      ...(kind === "cc" ? {controller} : {}), ...(at.id ? {id: at.id} : {}) }));
    props.submit([{kind: update ? "control.update" : "control.add", clipId: props.clipId, trackId: props.trackId, events}]);
  };
  const line = (from: Point, to: Point) => {
    const steps = Math.min(127, Math.max(1, Math.round(Math.abs(to.tick - from.tick) / props.grid)));
    const points = new Map<number, Point>();
    for (let i = 0; i <= steps; ++i) {
      const tick = Math.round(from.tick + (to.tick - from.tick) * i / steps);
      points.set(tick, {tick, value: Math.round(from.value + (to.value - from.value) * i / steps)});
    }
    return [...points.values()];
  };
  const x = (tick: number) => (tick - props.start) / props.span * 600;
  const y = (value: number) => 55 - value / limit * 55;
  return <section className="piano-control" aria-label="MIDI 控制通道">
    <div className="piano-control-tools">
      <label>控制 <select value={kind} onChange={e => { cancel(); setSelection([]); setTool("point"); setKind(e.target.value); }}>
        <option value="velocity">力度</option><option value="cc">CC</option><option value="pitchBend">弯音</option>
      </select></label>
      {kind === "cc" && <label>编号 <input type="number" min={0} max={127} value={controller} onChange={e => { cancel(); setController(e.target.valueAsNumber || 0); }}/></label>}
      <label>通道 <input type="number" min={1} max={16} value={channel + 1} onChange={e => { cancel(); setChannel((e.target.valueAsNumber || 1) - 1); }}/></label>
      {kind!=="velocity"&&<button disabled={!valid} onClick={() => save([{ tick: Math.min(props.length - 1, Math.max(0, Math.round(props.start))), value: kind === "cc" ? 64 : 8192 }])}>在视口起点添加</button>}
      <label>工具 <select value={tool} onChange={e => { cancel(); setTool(e.target.value); }}>
        <option value="point">点</option>{kind!=="velocity"&&<option value="line">直线</option>}<option value="select">区间选择</option>
      </select></label>
      <label>值偏移 <input type="number" value={offset} onChange={e=>setOffset(e.target.valueAsNumber || 0)}/></label>
      <button disabled={!valid || !selected.length || !offset} onClick={()=>save(selected.map(e=>({id:e.id,tick:e.tick,
        value:Math.min(limit,Math.max(0,(e.value??0)+Math.round(offset)))})),true)}>调整所选</button>
      {kind!=="velocity"&&<button disabled={!valid || !selected.length} onClick={() => props.submit([{ kind: "control.remove", trackId: props.trackId, clipId: props.clipId, eventIds: selected.map(e=>e.id) }])}>删除所选</button>}
      <span>{visible.length} 点</span>
    </div>
    <svg style={{marginLeft:props.inset,width:`calc(100% - ${props.inset}px)`}} viewBox="0 0 600 55" preserveAspectRatio="none" aria-label="点击添加，拖动控制点，Escape 取消"
      tabIndex={0} onKeyDown={e => { if (e.key === "Escape") cancel(); }}
      onPointerDown={e => {
        if (!valid || e.button !== 0 || stroke.current) return;
        const id = (e.target as SVGElement).dataset.event;
        const current = tool==="point"&&id ? visible.find(p => p.id === id) : undefined;
        if(kind==="velocity"&&tool==="point"&&!current)return;
        const initial=point(e,props.start,props.span);
        if(tool==="point")setSelection(current?[current.id]:[]);
        stroke.current = { pointer: e.pointerId, revision: props.revision, clipId: props.clipId,
          start: props.start, span: props.span, event: current, initial, tool };
        e.currentTarget.setPointerCapture(e.pointerId);
        setPending(current ? { tick: current.tick, value: current.value ?? 0 } : point(e, props.start, props.span));
      }}
      onPointerMove={e => {
        const current = stroke.current;
        if (current?.pointer === e.pointerId) {
          current.moved = true;
          setPending(point(e, current.start, current.span));
        }
      }}
      onPointerUp={e => {
        const current = stroke.current;
        if (current?.pointer !== e.pointerId) return;
        cancel();
        if (!valid || current.revision !== props.revision || current.clipId !== props.clipId ||
            current.start !== props.start || current.span !== props.span) return;
        const at=point(e,current.start,current.span);
        if(current.tool==="select"){
          const lo=Math.min(current.initial.tick,at.tick), hi=Math.max(current.initial.tick,at.tick);
          setSelection(visible.filter(p=>p.tick>=lo&&p.tick<=hi).slice(0,128).map(p=>p.id));
        }else if(current.tool==="line"){
          save(line(current.initial,at));
        }else if(!current.event||current.moved){
          save([{...at,id:current.event?.id}],!!current.event);
        }
      }}
      onPointerCancel={cancel} onLostPointerCapture={cancel} onBlur={cancel}>
      <line x1="0" y1="27.5" x2="600" y2="27.5" className="piano-control-guide"/>
      {visible.slice(0, 800).map(p => <g key={p.id}>
        <line x1={x(p.tick)} x2={x(p.tick)} y1={y(p.value ?? 0)} y2="55"/>
        <circle cx={x(p.tick)} cy={y(p.value ?? 0)} r="4" data-event={p.id} className={selection.includes(p.id) ? "selected" : ""}>
          <title>{p.tick} tick · {p.value ?? 0}</title>
        </circle>
      </g>)}
      {pending && stroke.current?.tool==="line" && <line x1={x(stroke.current.initial.tick)} y1={y(stroke.current.initial.value)}
        x2={x(pending.tick)} y2={y(pending.value)} pointerEvents="none"/>}
      {pending && <circle cx={x(pending.tick)} cy={y(pending.value)} r="5" className="selected" pointerEvents="none"/>}
    </svg>
  </section>;
}
