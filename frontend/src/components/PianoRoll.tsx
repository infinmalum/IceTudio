import { createPortal } from "react-dom";
import { MusicRuler } from "./MusicRuler";
import { PAGE_BEATS } from "../music/edits";
import { positionLabel,preciseWindow } from "../music/timeMap";
import { usePianoRoll,type PianoRollProps } from "../hooks/usePianoRoll";

export function PianoRoll(props: PianoRollProps) {
  const { target,meter,tempo,busy }=props;
  const { mount,rulerSlot,ready,loadError,page,pageCount,setPage,zoom,setZoom,absoluteStart,mapIndex,selected,visible,velocityField,velocityIDs,velocity,selectPage,clearSelection,cloneNotes,removeNotes,focusVelocity,changeVelocity,applyVelocity }=usePianoRoll(props);
  return <section className="piano-editor" aria-label="片段音符编辑器">
    {rulerSlot && createPortal(<MusicRuler ppq={target.ppq} start={absoluteStart} zoom={zoom} meter={meter} tempo={tempo}/>, rulerSlot)}
    <div className="editor-toolbar">
      <button className="secondary" disabled={busy || !ready || page === 0} onClick={() => setPage(page - 1)}>上一页</button>
      <span>四分拍 {page * PAGE_BEATS + 1}–{(page + 1) * PAGE_BEATS} · 第 {page + 1}/{pageCount} 页</span>
      <button className="secondary" disabled={busy || !ready || page + 1 >= pageCount} onClick={() => setPage(page + 1)}>下一页</button>
      <label>缩放<input aria-label="钢琴卷帘缩放" type="range" min={16} max={200} value={zoom} disabled={busy || !ready} onChange={e => setZoom(Number(e.target.value))}/></label>
    </div>
    <p className="editor-help ruler-position">本页起点：{preciseWindow(absoluteStart, target.ppq) ? positionLabel(mapIndex.position(absoluteStart)) : "位置超出精确小节显示范围，未自动调整片段"} · 工程 tick {absoluteStart}–{absoluteStart + PAGE_BEATS * target.ppq}。密集刻度按像素抽稀，变化点可分组合并；在时间图表单查看全部。</p>
    <div className="editor-toolbar">
      <button className="secondary" disabled={busy || !ready} onClick={selectPage}>全选本页</button>
      <button className="secondary" disabled={busy || !ready || !selected.length} onClick={clearSelection}>取消选择</button>
      <button className="secondary" disabled={busy || !ready || !selected.length} onClick={cloneNotes}>复制音符</button>
      <button className="secondary" title="先 Shift 点击/框选音符，或全选本页；可用撤销恢复删除" disabled={busy || !ready || !selected.length} onClick={removeNotes}>删除音符</button>
      <label>力度<input aria-label="音符力度" type="number" min={1} max={127} value={velocityField.raw} disabled={busy || !ready || !velocityIDs.length || velocityField.locked || !!velocityField.error}
        onFocus={focusVelocity} onChange={e => changeVelocity(e.target.value)}/></label>
      <button className="secondary" disabled={busy || !ready || !velocityIDs.length || velocityField.locked || !!velocityField.error || !velocityField.raw || !Number.isInteger(velocity) || velocity < 1 || velocity > 127} onClick={applyVelocity}>应用力度</button>
      <span>{selected.length} 个已选 · 本页 {visible.length} / 共 {target.clip.notes?.length ?? 0} 个音符</span>
    </div>
    {props.preview && <p className="editor-help">候选差异：绿色虚线为新值，红色虚线为旧值；只读标记，不参与选择或编辑。</p>}
    {loadError ? <p role="alert">{loadError}</p> : null}
    <div className="piano-host" ref={mount}/>
    <p className="editor-help">空白处点击添加；拖动移动，右边缘拉伸；Shift 点击或拖动多选；Delete 删除、Ctrl/⌘ B 复制、方向键移动、Ctrl/⌘ Z 撤销。前页开始的持续音在起始页编辑。更改自动保存；可用上方播放栏试听工程。</p>
    <p className="editor-help">另保留 {target.clip.controls?.length ?? 0} 个控制事件、{target.clip.raw?.length ?? 0} 个原始事件；音符编辑不会移除它们。</p>
  </section>;
}
