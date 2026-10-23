import { useMemo } from "react";
import type { MeterPoint, TempoPoint } from "../../bindings/icetudio/internal/project/models";
import { meterIndex, positionLabel, preciseWindow } from "../music/timeMap";
import { PAGE_BEATS } from "../music/edits";

export function MusicRuler({ ppq, start, zoom, meter, tempo }: { ppq: number; start: number; zoom: number; meter: MeterPoint[]; tempo: TempoPoint[] }) {
  const index = useMemo(() => meterIndex(ppq, meter), [ppq, meter]);
  const end = start + PAGE_BEATS * ppq, precise = preciseWindow(start, ppq);
  const { marks, thinned } = useMemo(() => precise ? index.ruler(start, end, zoom, tempo) : { marks: [], thinned: false }, [index, start, end, zoom, tempo, precise]);
  const description = precise ? `从 ${positionLabel(index.position(start))}` : "位置超出精确小节显示范围";
  return <svg className="music-ruler" role="img" aria-label={`工程小节尺，${description}${thinned ? "；密集标签抽稀，变化点分组合并" : ""}`} width={PAGE_BEATS * zoom} height={52} data-start={start} data-thinned={thinned}>
    <title>{`${description}；分母定义拍单位，刻度使用工程绝对 tick${thinned ? "；密集标签已抽稀，变化点分组后可在表单查看全部" : ""}`}</title>
    {marks.map((m, i) => {
      const x = (m.tick - start) / ppq * zoom;
      return <g key={`${m.kind}/${i}`} data-kind={m.kind} data-tick={m.tick} data-count={m.count} className={`music-ruler-${m.kind}`}>
        <title>{m.title}</title>
        <line x1={x} x2={x} y1={m.kind === "beat" ? 46 : m.kind === "bar" ? 22 : 0} y2={52}/>
        {m.label && <text x={x + 3} y={m.kind === "tempo" ? 11 : m.kind === "meter" ? 45 : 30}>{m.kind === "tempo" ? "♩ " : ""}{m.label}{m.count > 1 ? ` ×${m.count}` : ""}</text>}
      </g>;
    })}
  </svg>;
}
