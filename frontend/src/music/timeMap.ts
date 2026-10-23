import type { MeterPoint, Op, TempoPoint } from "../../bindings/icetudio/internal/project/models";

export const MAX_TICK = 2147483647;
export const MAP_PAGE_SIZE = 24;
export const DENOMINATORS = [1, 2, 4, 8, 16, 32, 64, 128];
export type TempoRow = { key: number; tick: string; bpm: string; originalBPM: string; originalUS: number };
export type MeterRow = { key: number; tick: string; numerator: string; denominator: string };
export type MapDraft = { tempo: TempoRow[]; meter: MeterRow[] };

export function draftMaps(tempo: TempoPoint[], meter: MeterPoint[]): MapDraft {
  return {
    tempo: tempo.map((p, key) => ({ key, tick: String(p.tick), bpm: String(60000000 / p.usPerQuarter), originalBPM: String(60000000 / p.usPerQuarter), originalUS: p.usPerQuarter })),
    meter: meter.map((p, key) => ({ key, tick: String(p.tick), numerator: String(p.numerator), denominator: String(p.denominator) })),
  };
}
function integer(value: string, min: number, max: number, label: string): number {
  const n = Number(value);
  if (!value.trim() || !Number.isSafeInteger(n) || n < min || n > max) throw Error(`${label}须为 ${min}–${max} 的整数`);
  return n;
}
export function tempoUS(row: TempoRow): number {
  if (row.bpm === row.originalBPM) return row.originalUS;
  const bpm = Number(row.bpm), us = Math.round(60000000 / bpm);
  if (!row.bpm.trim() || !Number.isFinite(bpm) || bpm <= 0 || us < 1 || us > 0xffffff) throw Error("速度须对应 1–16777215 微秒/四分音符的有效 BPM");
  return us;
}
function ordered<T extends { tick: number }>(points: T[], label: string): T[] {
  points.sort((a, b) => a.tick - b.tick);
  if (!points.length || points[0].tick !== 0) throw Error(`${label}必须保留 tick 0 起点`);
  if (points.some((p, i) => i > 0 && p.tick === points[i - 1].tick)) throw Error(`${label}含重复 tick，请修改位置后再应用`);
  return points;
}
export function mapOps(draft: MapDraft, original: { tempo: TempoPoint[]; meter: MeterPoint[] }): Op[] {
  const tempo = ordered(draft.tempo.map(p => ({ tick: integer(p.tick, 0, MAX_TICK, "速度位置"), usPerQuarter: tempoUS(p) })), "速度图");
  const meter = ordered(draft.meter.map(p => {
    const denominator = integer(p.denominator, 1, 128, "拍号分母");
    if (!DENOMINATORS.includes(denominator)) throw Error("拍号分母须为 1–128 中的 2 的幂");
    return { tick: integer(p.tick, 0, MAX_TICK, "拍号位置"), numerator: integer(p.numerator, 1, 255, "拍号分子"), denominator };
  }), "拍号图");
  const ops: Op[] = [];
  if (JSON.stringify(tempo) !== JSON.stringify(original.tempo.map(p => ({ tick: p.tick, usPerQuarter: p.usPerQuarter })))) ops.push({ kind: "tempo.set", tempo });
  if (JSON.stringify(meter) !== JSON.stringify(original.meter.map(p => ({ tick: p.tick, numerator: p.numerator, denominator: p.denominator })))) ops.push({ kind: "meter.set", meter });
  return ops;
}

type Segment = MeterPoint & { start: number; end: number; beatSize: number; barSize: number; bar: number };
export type MusicPosition = { bar: number; beat: number; remainder: number; numerator: number; denominator: number };
export type RulerMark = { tick: number; kind: "bar" | "beat" | "meter" | "tempo"; label: string; title: string; count: number };

// Every supported denominator divides 128. Scale ticks by 32 so fractional
// beats remain exact; max project tick * 32 is well within JS safe integers.
export function meterIndex(ppq: number, meter: MeterPoint[]) {
  if (!Number.isInteger(ppq) || ppq < 1 || ppq > 32768 || !meter.length || meter[0].tick !== 0) throw Error("无效 PPQ 或拍号图");
  let bar = 1;
  const segments: Segment[] = meter.map((p, i) => {
    if (!Number.isInteger(p.tick) || p.tick < 0 || p.tick > MAX_TICK || (i > 0 && p.tick <= meter[i - 1].tick) || !DENOMINATORS.includes(p.denominator) || !Number.isInteger(p.numerator) || p.numerator < 1 || p.numerator > 255) throw Error("无效拍号变化点");
    const beatSize = ppq * (128 / p.denominator), barSize = beatSize * p.numerator;
    const start = p.tick * 32, end = i + 1 < meter.length ? meter[i + 1].tick * 32 : Infinity;
    const s = { ...p, start, end, beatSize, barSize, bar };
    if (Number.isFinite(end)) bar += Math.ceil((end - start) / barSize);
    return s;
  });
  function at(tick: number): Segment {
    if (tick < 0 || !Number.isSafeInteger(tick * 32)) throw Error("音乐位置超出精确小节显示范围");
    let lo = 0, hi = segments.length;
    while (lo < hi) { const m = (lo + hi) >>> 1; if (segments[m].tick <= tick) lo = m + 1; else hi = m; }
    return segments[Math.max(0, lo - 1)];
  }
  function position(tick: number): MusicPosition {
    const s = at(tick), offset = tick * 32 - s.start, inBar = offset % s.barSize;
    return { bar: s.bar + Math.floor(offset / s.barSize), beat: Math.floor(inBar / s.beatSize) + 1, remainder: (inBar % s.beatSize) / 32, numerator: s.numerator, denominator: s.denominator };
  }
  function ruler(start: number, end: number, pixelsPerQuarter: number, tempo: TempoPoint[]) {
    at(start);
    if (!Number.isSafeInteger(end * 32) || end <= start || !Number.isFinite(pixelsPerQuarter) || pixelsPerQuarter < 16 || pixelsPerQuarter > 200 || end - start > 16 * ppq) throw Error("小节尺窗口超出支持范围");
    const out: RulerMark[] = [];
    const groups = new Map<string, RulerMark>();
    let thinned = false;
    const px = (tick: number) => (tick - start) / ppq * pixelsPerQuarter;
    const addChange = (mark: RulerMark) => {
      const key = `${mark.kind}:${Math.floor(px(mark.tick) / 16)}`;
      const group = groups.get(key);
      if (group) { group.count++; group.title = `${group.count} 个${mark.kind === "tempo" ? "速度" : "拍号"}变化点：从 tick ${group.tick} 到 ${mark.tick}；在时间图表单查看全部`; thinned = true; }
      else groups.set(key, mark);
    };
    // Iterate intersecting map segments, never all preceding measures/beats.
    const first = segments.indexOf(at(start));
    for (let i = first; i < segments.length && segments[i].tick < end; i++) {
      const s = segments[i], a = Math.max(start * 32, s.start), b = Math.min(end * 32, s.end);
      const barStride = Math.max(1, Math.ceil(52 / (s.barSize / 32 / ppq * pixelsPerQuarter)));
      const beatStride = Math.max(1, Math.ceil(12 / (s.beatSize / 32 / ppq * pixelsPerQuarter)));
      thinned ||= barStride > 1 || beatStride > 1;
      for (let n = Math.ceil(Math.max(0, a - s.start) / s.barSize / barStride) * barStride; s.start + n * s.barSize < b; n += barStride) {
        const tick = (s.start + n * s.barSize) / 32;
        out.push({ tick, kind: "bar", label: String(s.bar + n), title: `第 ${s.bar + n} 小节 · tick ${tick} · ${s.numerator}/${s.denominator}`, count: 1 });
      }
      for (let n = Math.ceil(Math.max(0, a - s.start) / s.beatSize / beatStride) * beatStride; s.start + n * s.beatSize < b; n += beatStride) {
        if (n % s.numerator === 0) continue;
        const tick = (s.start + n * s.beatSize) / 32;
        out.push({ tick, kind: "beat", label: "", title: `tick ${tick}`, count: 1 });
      }
      if (s.tick >= start) addChange({ tick: s.tick, kind: "meter", label: `${s.numerator}/${s.denominator}`, title: `tick ${s.tick} · 第 ${s.bar} 小节 · ${s.numerator}/${s.denominator}`, count: 1 });
    }
    for (const p of tempo) if (p.tick >= start && p.tick < end) addChange({ tick: p.tick, kind: "tempo", label: `${Number((60000000 / p.usPerQuarter).toFixed(2))}`, title: `tick ${p.tick} · ${60000000 / p.usPerQuarter} BPM · ${p.usPerQuarter} 微秒/四分音符`, count: 1 });
    // Segment changes can also create dense ordinary downbeats. Keep at most
    // one ordinary mark per pixel bucket; changes are retained as counted groups.
    const ordinary = new Map<number, RulerMark>();
    for (const m of out) {
      const bucket = Math.floor(px(m.tick) / 12), prev = ordinary.get(bucket);
      if (!prev || (prev.kind === "beat" && m.kind === "bar")) ordinary.set(bucket, m);
      if (prev) thinned = true;
    }
    const marks = [...ordinary.values(), ...groups.values()].sort((a, b) => a.tick - b.tick);
    const labels = new Map<string, number>();
    for (const mark of marks) if (mark.label) {
      const gap = mark.kind === "tempo" ? 66 : mark.kind === "meter" ? 48 : 52;
      if (px(mark.tick) - (labels.get(mark.kind) ?? -Infinity) < gap) { mark.label = ""; thinned = true; }
      else labels.set(mark.kind, px(mark.tick));
    }
    return { marks, thinned };
  }
  return { position, ruler };
}
export function preciseWindow(start: number, ppq: number): boolean {
  return start >= 0 && Number.isSafeInteger(start * 32) && Number.isSafeInteger((start + 16 * ppq) * 32);
}
export function positionLabel(p: MusicPosition): string {
  return `${p.bar} 小节 ${p.beat} 拍${p.remainder ? ` +${p.remainder} tick` : ""}（${p.numerator}/${p.denominator}）`;
}
