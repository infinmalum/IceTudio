import test from "node:test";
import assert from "node:assert/strict";
import { draftMaps, mapOps, MAX_TICK, meterIndex, preciseWindow } from "../src/music/timeMap.ts";

test("bar/beat positions use the denominator across 4/4, 3/4 and 6/8", () => {
  const m = meterIndex(480, [{ tick: 0, numerator: 4, denominator: 4 }, { tick: 1920, numerator: 3, denominator: 4 }, { tick: 3360, numerator: 6, denominator: 8 }]);
  assert.deepEqual(m.position(1920), { bar: 2, beat: 1, remainder: 0, numerator: 3, denominator: 4 });
  assert.deepEqual(m.position(4080), { bar: 3, beat: 4, remainder: 0, numerator: 6, denominator: 8 });
  assert.equal(m.position(4800).bar, 4);
});
test("a mid-bar or same-value meter event starts a new numbered bar without moving ticks", () => {
  const m = meterIndex(480, [{ tick: 0, numerator: 4, denominator: 4 }, { tick: 600, numerator: 4, denominator: 4 }]);
  assert.deepEqual(m.position(599), { bar: 1, beat: 2, remainder: 119, numerator: 4, denominator: 4 });
  assert.equal(m.position(600).bar, 2);
  assert.equal(m.position(2520).bar, 3);
});
test("PPQ=1 and denominator 128 retain fractional tick beat boundaries", () => {
  const m = meterIndex(1, [{ tick: 0, numerator: 3, denominator: 128 }]);
  assert.deepEqual(m.position(1), { bar: 11, beat: 3, remainder: 0, numerator: 3, denominator: 128 });
  const r = m.ruler(0, 16, 160, []);
  assert(r.thinned);
  assert(r.marks.some(p => !Number.isInteger(p.tick)));
  assert(r.marks.length < 1024);
});
test("far locations use direct arithmetic and page offsets, with exact supported maximum", () => {
  assert.equal(meterIndex(1, [{ tick: 0, numerator: 1, denominator: 128 }]).position(MAX_TICK).bar, MAX_TICK * 32 + 1);
  assert.equal(meterIndex(1, [{ tick: 0, numerator: 1, denominator: 128 }]).position(MAX_TICK * 2).bar, MAX_TICK * 64 + 1);
  assert(preciseWindow(MAX_TICK, 480));
  assert(!preciseWindow(Number.MAX_SAFE_INTEGER, 480));
  const m = meterIndex(480, [{ tick: 0, numerator: 4, denominator: 4 }, { tick: 600, numerator: 3, denominator: 4 }]);
  assert.deepEqual(m.position(600 + 16 * 480), { bar: 7, beat: 2, remainder: 0, numerator: 3, denominator: 4 });
  assert.equal(m.ruler(8280, 15960, 64, []).marks.find(p => p.kind === "bar")?.tick, 9240);
  assert.throws(() => m.ruler(0, 7681, 64, []));
});
test("dense changes are counted in bounded groups, not discarded or mounted per event", () => {
  const meter = Array.from({ length: 7000 }, (_, tick) => ({ tick, numerator: 1 + tick % 7, denominator: 128 }));
  const tempo = Array.from({ length: 7000 }, (_, tick) => ({ tick, usPerQuarter: 400000 + tick }));
  const r = meterIndex(480, meter).ruler(0, 7680, 200, tempo);
  assert(r.thinned);
  assert(r.marks.length < 1024, String(r.marks.length));
  assert.equal(r.marks.filter(p => p.kind === "meter").reduce((n, p) => n + p.count, 0), 7000);
  assert.equal(r.marks.filter(p => p.kind === "tempo").reduce((n, p) => n + p.count, 0), 7000);
});
test("untouched tempo precision is preserved and changing both maps makes one operation batch", () => {
  const original = { tempo: [{ tick: 0, usPerQuarter: 500001 }, { tick: 480, usPerQuarter: 765431 }], meter: [{ tick: 0, numerator: 4, denominator: 4 }] };
  const d = draftMaps(original.tempo, original.meter);
  assert.deepEqual(mapOps(d, original), []);
  d.meter.push({ key: 1, tick: "600", numerator: "6", denominator: "8" });
  assert.equal(mapOps(d, original).length, 1);
  d.tempo[1].bpm = "90";
  const ops = mapOps(d, original);
  assert.equal(ops.length, 2);
  assert.equal(ops[0].tempo?.[0].usPerQuarter, 500001);
  assert.equal(ops[0].tempo?.[1].usPerQuarter, 666667);
});
test("empty, duplicate, fractional, non-finite and out-of-range form values are rejected", () => {
  const original = { tempo: [{ tick: 0, usPerQuarter: 500000 }], meter: [{ tick: 0, numerator: 4, denominator: 4 }] };
  for (const value of ["", "0", "-2", "Infinity", "NaN", "0.00001", "999999999999"]) {
    const d = draftMaps(original.tempo, original.meter); d.tempo[0].bpm = value;
    assert.throws(() => mapOps(d, original), value);
  }
  for (const value of ["", "0", "-1", "1.5", "2147483648"]) {
    const d = draftMaps(original.tempo, original.meter); d.meter.push({ key: 1, tick: value, numerator: "3", denominator: "4" });
    assert.throws(() => mapOps(d, original), value);
  }
  const d = draftMaps(original.tempo, original.meter); d.meter[0].denominator = "3";
  assert.throws(() => mapOps(d, original));
  assert.throws(() => meterIndex(480, [{ tick: 0, numerator: 4, denominator: 3 }]));
});
