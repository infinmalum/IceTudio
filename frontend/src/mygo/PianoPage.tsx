import { createRoot } from "react-dom/client";
import { createPortal } from "react-dom";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { CloseContext, CloseFrozenContext, useCloseFrozen, useCloseBlocker, type Blocker, type Register } from "../shared/musicClose";
import { MusicServicesContext, type MusicServices } from "../music/services";
import { MusicInputs, useMusicInputs } from "../hooks/useMusicInputs";
import { usePianoRoll } from "../hooks/usePianoRoll";
import { MusicRuler } from "../components/MusicRuler";
import type { ProjectEditPreview } from "../music/previewTypes";
import type { Clip, Track, MeterPoint, TempoPoint, Op } from "../music/projectTypes";
import type { EditTarget } from "../music/edits";
import { ControlLane } from "./ControlLane";
import "./piano.css";

type Context = {pattern?:{TrackID:string;TrackName:string;ClipID:string;ClipName:string;Start:number}[];candidate?:ProjectEditPreview;snap?:number;protocolVersion:number;readOnly:boolean; sessionId:string;generation:number;projectId:string;trackId:string;clipId:string;revision:number;ppq:number;title:string;writable:boolean;track:Track;clip:Clip;meter:MeterPoint[];tempo:TempoPoint[]};
type Receipt = {opId:string;revision:number};
type Command = {theme?:string;quantizeKey?:string;duplicateKey?:string;tick?:number;playing?:boolean;follow?:string;step?:number;generation:number;selectionVersion:number;noteIds:string[];name:string;unit?:string;value?:number};
interface Runtime {call<T>(method:string,...args:unknown[]):Promise<T>;on<T>(event:string,callback:(value:T)=>void):()=>void}
declare global {interface Window {mygo:Runtime;__icetudioPiano?:{inspect:(token:string)=>unknown;resume:(token:string)=>void}}}
const call = <T,>(method:string,...args:unknown[]) => window.mygo.call<T>(`PianoPreview.${method}`,...args);
const services:MusicServices={
 GetMusicInputs:()=>call("GetMusicInputs"), SaveMusicInputs:(...args)=>call("SaveMusicInputs",...args),
 PrepareNoteTransform:(...args)=>call("PrepareNoteTransform",...args), Audition:(...args)=>call("Audition",...args),
};

function CloseScope({children}:{children:ReactNode}) {
 const readers=useRef(new Map<number,()=>Blocker|null>()),serial=useRef(0);
 const register=useRef<Register>(reader=>{const id=++serial.current;readers.current.set(id,reader);return()=>{readers.current.delete(id)}}).current;
 const [frozen,setFrozen]=useState(false);
 useEffect(()=>{
  const root=document.getElementById("root")!;
  let activeToken="";
  const canceled=new Set<string>();
  const api={inspect:(token:string)=>{
   if(canceled.has(token))return {token,blockers:[],version:"canceled"};
   activeToken=token;root.inert=true;setFrozen(true);
   if(document.activeElement instanceof HTMLElement)document.activeElement.blur();
   const blockers=[...readers.current.values()].flatMap(read=>{const v=read();return v?[v]:[]});
   return {token,blockers,version:JSON.stringify(blockers)};
  },resume:(token:string)=>{canceled.add(token);if(activeToken===token){activeToken="";root.inert=false;setFrozen(false)}}};
  window.__icetudioPiano=api;
  return()=>{if(window.__icetudioPiano===api)delete window.__icetudioPiano};
 },[]);
 return <CloseContext.Provider value={register}><CloseFrozenContext.Provider value={frozen}>{children}</CloseFrozenContext.Provider></CloseContext.Provider>;
}

function PianoPage(){
 const inputs=useMusicInputs();
 const closeFrozen=useCloseFrozen();
 const [context,setContext]=useState<Context|null>(null),[error,setError]=useState("");
 const [submitting,setSubmitting]=useState(false);
 const pending=useRef(false),sequence=useRef(0),current=useRef(context);
 current.current=context;
 useCloseBlocker(()=>!inputs.ready?{kind:"busy",message:"音乐草稿恢复检查尚未完成"}:pending.current?{kind:"busy",message:"卷帘操作尚未确认"}:null);
 useEffect(()=>{
  let alive=true;
  const attached=call<Context>("Attach");
  async function refresh(attach=false){const n=++sequence.current;try{
   const initial=await attached;
   const value=attach?initial:await call<Context>("Context");
   if(value.protocolVersion!==1)throw Error("卷帘协议版本不匹配");
   if(alive&&n===sequence.current)setContext(value);
  }catch(e){if(alive)setError(String(e))}}
  const off=window.mygo.on<number>("icetudio:piano-context",()=>{void refresh()});
  void refresh(true);
  return()=>{alive=false;off()};
 },[]);
 async function edit(target:EditTarget,ops:Op[]|"undo"|"redo"):Promise<boolean>{
  const ctx=current.current;
  if(!ctx||pending.current||!ctx.writable||document.getElementById("root")?.inert)return false;
  pending.current=true;setSubmitting(true);setError("");
  let committed:Receipt|null=null;
  const opId=`op_${crypto.randomUUID().replace(/-/g,"")}`;
  try{
   const receipt=await call<Receipt>("Submit",{sessionId:ctx.sessionId,generation:ctx.generation,projectId:target.id,revision:target.revision,trackId:ctx.trackId,clipId:ctx.clipId,opId,kind:typeof ops==="string"?ops:"edit",ops:typeof ops==="string"?[]:ops});
   committed=receipt;
   await call("Acknowledge",ctx.sessionId,target.id,receipt.opId);
   let value=await call<Context>("Context");
   const deadline=Date.now()+2000;
   while(value.projectId===ctx.projectId&&value.revision<receipt.revision&&Date.now()<deadline){await new Promise(resolve=>setTimeout(resolve,20));value=await call<Context>("Context")}
   if(value.projectId===ctx.projectId&&value.revision<receipt.revision)throw Error("已保存，但权威视图刷新尚未完成");
   if(current.current?.sessionId===ctx.sessionId && value.generation>=current.current.generation)setContext(value);
   return true;
  }catch(e){setError(committed?`已保存（修订 ${committed.revision}），界面确认未完成：${String(e)}`:`修改未获确认，请核对原保存结果：${String(e)}`);return committed!==null}
  finally{pending.current=false;setSubmitting(false)}
 }
 return <MusicInputs.Provider value={inputs}>
  <div className="piano-status" role={error?"alert":"status"}>{error||(context?.readOnly?"只读视觉预览":context?.writable?"自动保存 · 与 Native 共享撤销":"正在检查恢复状态 / 等待保存确认")}</div>
  {context?.clipId&&context.clip ? <Surface key={`${context.projectId}/${context.trackId}/${context.clipId}`} context={context} busy={submitting||!context.writable||closeFrozen} edit={edit} reportError={e=>setError(String(e))}/> : <p>在编排中选择一个片段。</p>}
 </MusicInputs.Provider>;
}

function Surface({context:ctx,busy,edit,reportError}:{context:Context;busy:boolean;edit:(target:EditTarget,ops:Op[]|"undo"|"redo")=>Promise<boolean>;reportError:(error:unknown)=>void}){
 const target=useMemo(()=>({id:ctx.projectId,revision:ctx.revision,ppq:ctx.ppq,track:ctx.track,clip:ctx.clip}),[ctx]);
 const [snap,setSnap]=useState(ctx.snap??1);
 const [scale,setScale]=useState("off"), [root,setRoot]=useState(0), [controls,setControls]=useState(false);
 useEffect(()=>setSnap(ctx.snap??1),[ctx.snap]);
 const roll=usePianoRoll({title:ctx.title,target,meter:ctx.meter,tempo:ctx.tempo,busy,edit,shift:kind=>{void edit({id:ctx.projectId,revision:ctx.revision},kind)},reportError,snap,navigate:(name,tick=0,end=0)=>{void call("Navigate",ctx.sessionId,ctx.generation,name,tick,end).catch(error=>{void scopedError(error,ctx)})},preview:ctx.candidate});
 useEffect(()=>{
  const notes=scale==="minor"?[0,2,3,5,7,8,10]:[0,2,4,5,7,9,11];
  roll.mount.current?.querySelectorAll<HTMLElement>(".gsui-row[data-midi],.gsuiKeys-key[data-midi]").forEach(row=>{
   const degree=(Number(row.dataset.midi)-root+12)%12;
   row.classList.toggle("piano-scale-muted",scale!=="off"&&!notes.includes(degree));
   row.classList.toggle("piano-scale-root",scale!=="off"&&degree===0);
  });
 },[roll.mount,roll.ready,root,scale]);
 const selectionVersion=useRef(0);
 const selectionKey=JSON.stringify(roll.selected);
 const latest=useRef({ctx,roll,busy});latest.current={ctx,roll,busy};
 const alive=useRef(true);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[]);
 async function scopedError(error:unknown,requested:Context){
  const same=()=>alive.current&&latest.current.ctx.sessionId===requested.sessionId&&latest.current.ctx.generation===requested.generation;
  if(!same())return;
  try{const actual=await call<Context>("Context");if(actual.sessionId!==requested.sessionId||actual.generation!==requested.generation)return}catch{}
  if(same())reportError(error);
 }

 useEffect(()=>window.mygo.on<Command>("icetudio:piano-command",command=>{
  const state=latest.current;
  if(command.generation!==state.ctx.generation||document.getElementById("root")?.inert)return;
  const r=state.roll;
  if(command.name==="appearance"){document.documentElement.dataset.theme=command.theme==="light"?"light":"dark";return}
  if(command.name==="keymap"){r.keymap(command.quantizeKey??"Q",command.duplicateKey??"B");return}
  if(command.name==="playhead"){r.playhead(command.tick??0,command.playing??false,command.follow??"关闭");return}
  if(r.gestureActive()){reportError("请先完成或取消音符手势，再使用 Native 工具");return}
  if(["snap","zoom"].includes(command.name)){
   if(state.busy&&!state.ctx.readOnly)return;
   if(command.name==="zoom")r.setZoom(Math.max(16,Math.min(200,r.zoom+(command.value??0))));
   else {const next=command.step??1;if(Number.isFinite(next)&&next>0&&next<=16)setSnap(next)}
   return;
  }
  if(!["locate","paste","selectAll","clear"].includes(command.name)){
   const equal=(a:string[],b:string[])=>JSON.stringify([...a].sort())===JSON.stringify([...b].sort());
   if(state.busy||r.gestureActive()||command.selectionVersion!==selectionVersion.current||!equal(command.noteIds,r.currentSelection())||!equal(command.noteIds,r.selected)){reportError("选区或手势已变化，请重新选择后重试");return}
  }
  switch(command.name){case "selectAll":r.selectAll();break;case "clear":r.clearSelection();break;case "delete":r.removeNotes();break;case "copy":r.copy();break;case "paste":r.paste();break;case "duplicate":r.cloneNotes();break;case "transpose":r.transpose(command.value??0);break;case "quantize":if(["1/4","1/8","1/16","1/32","1/8T","1/16T"].includes(command.unit??"1/16"))r.quantizeTo(command.unit??"1/16");break;case "locate":r.locate(command.value??0);break;}
 }),[]);
 useEffect(()=>{
  const version=++selectionVersion.current;
  void call("Selection",ctx.sessionId,ctx.generation,version,roll.selected,!busy&&!roll.transformBusy).catch(()=>{});
 },[ctx.sessionId,ctx.generation,selectionKey,busy,roll.transformBusy]);
 useEffect(()=>{
  document.body.dataset.ready=roll.ready?"true":"false";
  if(roll.loadError)document.documentElement.dataset.loadError=roll.loadError;
  document.body.dataset.preview=String(ctx.readOnly);
  document.body.dataset.clip=ctx.clipId;document.body.dataset.generation=String(ctx.generation);
  document.body.dataset.notes=String(roll.visible.length);
  if(roll.ready)void call("ContextReady",ctx.sessionId,ctx.generation).catch(error=>{void scopedError(error,ctx)});
 },[roll.ready,roll.loadError,ctx.sessionId,ctx.generation,ctx.clipId,roll.visible.length]);
 return <>
  {roll.rulerSlot&&createPortal(<MusicRuler ppq={ctx.ppq} start={roll.absoluteStart} zoom={roll.zoom} meter={ctx.meter} tempo={ctx.tempo} beats={roll.viewport.beats}/>,roll.rulerSlot)}
  {roll.loadError&&<p role="alert">{roll.loadError}</p>}
  <div className="piano-control-tools piano-aux">
   <label>音阶 <select value={scale} onChange={event=>setScale(event.target.value)}>
    <option value="off">关闭</option><option value="major">大调</option><option value="minor">自然小调</option>
   </select></label>
   {scale!=="off"&&<label>根音 <select value={root} onChange={event=>setRoot(Number(event.target.value))}>
    {["C","C♯","D","E♭","E","F","F♯","G","A♭","A","B♭","B"].map((name,index)=><option key={name} value={index}>{name}</option>)}
   </select></label>}
   <button aria-expanded={controls} onClick={()=>setControls(open=>!open)}>控制通道</button>
   {!!ctx.pattern?.length&&<details className="piano-pattern">
    <summary>共享 Pattern · {ctx.pattern.length} 个实例</summary>
    <ul>{ctx.pattern.map(part=><li key={part.ClipID}>{part.TrackName} / {part.ClipName} · {part.Start} tick</li>)}</ul>
   </details>}
  </div>
  <div className="piano-host" ref={roll.mount}/>
  {controls&&<ControlLane key={ctx.clipId} clipId={ctx.clipId} trackId={ctx.trackId} revision={ctx.revision}
   channel={ctx.track.channel} length={ctx.clip.length} events={ctx.clip.controls} notes={ctx.clip.notes}
   start={roll.viewport.start} span={roll.viewport.controlBeats*ctx.ppq} inset={roll.viewport.inset} grid={Math.max(1,snap*ctx.ppq)}
   disabled={busy||!roll.ready} submit={ops=>{void edit(target,ops)}}/>}
  <div className="piano-status">连续时间轴 · 已选 {roll.selected.length} · {roll.visible.length}/{ctx.clip.notes?.length??0} 音符 · 保留 {ctx.clip.controls?.length??0} 控制事件</div>
 </>;
}

createRoot(document.getElementById("root")!).render(<MusicServicesContext.Provider value={services}><CloseScope><PianoPage/></CloseScope></MusicServicesContext.Provider>);
