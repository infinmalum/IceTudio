import { useMemo, useState } from "react";
import { GM_FAMILIES, GM_PERCUSSION, gmInstrument, isPercussionChannel, searchGmInstruments } from "../music/gm";

// GM 乐器选择器：按 16 家族分组 + 搜索（英文名、家族中英文）。
// 打击通道（索引 9）显示 GM 打击乐组，不提供 melodic 列表；program 数字在 Inspector 高级参数中调整。
export function InstrumentPicker({ program, channel, disabled, onPick }: {
  program: number; channel: number; disabled: boolean;
  onPick: (program: number) => void;
}) {
  const [query, setQuery] = useState("");
  const percussion = isPercussionChannel(channel);
  const matches = useMemo(() => searchGmInstruments(query), [query]);
  const groups = useMemo(() => GM_FAMILIES
    .map((family, f) => ({ family, items: matches.filter(m => m.matched && Math.floor(m.program / 8) === f) }))
    .filter(g => g.items.length > 0), [matches]);
  const selectedVisible = percussion || matches[program]?.matched === true;
  return <div className="instrument-picker">
    {percussion
      ? <span className="editor-help" title="打击通道使用 GM 打击乐组（bank 128）；program 数字在高级参数调整">{GM_PERCUSSION.zh} · {GM_PERCUSSION.name}</span>
      : <>
        <label>乐器搜索<input aria-label="搜索乐器" placeholder="名称或家族，如：钢琴、Violin、Brass" disabled={disabled} value={query}
          onChange={e => setQuery(e.target.value)}/></label>
        <select aria-label="选择乐器" disabled={disabled} value={program} onChange={e => onPick(Number(e.target.value))}>
          {!selectedVisible && <option value={program}>{gmInstrument(program).name}（当前）</option>}
          {groups.map(g => <optgroup key={g.family.en} label={`${g.family.zh} · ${g.family.en}`}>
            {g.items.map(m => <option key={m.program} value={m.program}>{m.name}</option>)}
          </optgroup>)}
        </select>
      </>}
  </div>;
}
