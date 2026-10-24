import test from "node:test";
import assert from "node:assert/strict";
import { GM_FAMILIES, GM_INSTRUMENT_NAMES, GM_PERCUSSION, gmInstrument, searchGmInstruments, trackInstrumentLabel, isGmProgram, isPercussionChannel } from "../src/music/gm.ts";

test("GM 元数据完整：128 program 连续编号", () => {
  assert.equal(GM_INSTRUMENT_NAMES.length, 128);
  for (let program = 0; program < 128; program++) {
    const instrument = gmInstrument(program);
    assert.equal(instrument.program, program);
    assert.ok(instrument.name.length > 0, `program ${program} 名称为空`);
    assert.equal(instrument.family.en, GM_FAMILIES[Math.floor(program / 8)].en);
  }
});

test("GM 元数据完整：16 家族、每家族恰好 8 个 program", () => {
  assert.equal(GM_FAMILIES.length, 16);
  for (let f = 0; f < 16; f++) {
    for (let k = 0; k < 8; k++) {
      const instrument = gmInstrument(f * 8 + k);
      assert.equal(instrument.family.en, GM_FAMILIES[f].en);
      assert.ok(instrument.family.zh.length > 0, `家族 ${f} 中文名为空`);
    }
  }
});

test("GM 标记音：已知 program 的名称与打击组", () => {
  assert.equal(gmInstrument(0).name, "Acoustic Grand Piano");
  assert.equal(gmInstrument(40).name, "Violin");
  assert.equal(GM_PERCUSSION.bank, 128);
  assert.equal(trackInstrumentLabel(0, 9), GM_PERCUSSION.name);
  assert.equal(trackInstrumentLabel(40, 0), "Violin");
  assert.ok(isPercussionChannel(9));
  assert.ok(!isPercussionChannel(0));
});

test("GM 搜索：英文名、家族中英文", () => {
  const byName = searchGmInstruments("violin");
  assert.equal(byName.filter(m => m.matched).length, 1);
  assert.equal(byName.find(m => m.matched)?.program, 40);
  const byFamilyZh = searchGmInstruments("钢琴");
  assert.deepEqual(byFamilyZh.filter(m => m.matched).map(m => m.program), [0, 1, 2, 3, 4, 5, 6, 7]);
  const byFamilyEn = searchGmInstruments("brass");
  assert.ok(byFamilyEn.slice(56, 64).every(m => m.matched));
  assert.ok(searchGmInstruments("").every(m => m.matched));
  assert.equal(searchGmInstruments("不存在的乐器 xyzzy").filter(m => m.matched).length, 0);
});

test("GM 范围拒绝：越界 program 报错", () => {
  assert.ok(isGmProgram(0) && isGmProgram(127));
  assert.ok(!isGmProgram(-1) && !isGmProgram(128) && !isGmProgram(0.5));
  assert.throws(() => gmInstrument(128));
  assert.throws(() => gmInstrument(-1));
});
