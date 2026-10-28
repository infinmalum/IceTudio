import { useEffect,useRef,useState } from "react";
import { Events } from "@wailsio/runtime";
import { ProjectService } from "../../bindings/icetudio";
import type { ProjectEditPreview } from "../../bindings/icetudio/internal/app/models";
import type { Note } from "../../bindings/icetudio/internal/project/models";
import type { ToolCall } from "../../bindings/icetudio/internal/storage/models";
import type { ConversationState } from "../hooks/useConversation";
import type { useProjects } from "../hooks/useProjects";

export type PreviewWorkspace = Pick<ReturnType<typeof useProjects>,"activeID"|"showPreview"|"clearPreview">;
export function supportsNotePreview(call: ToolCall) {
  try {
    const a=JSON.parse(call.arguments);
    return call.toolName==="project__transform" ? !a.dryRun : call.toolName==="project__edit" && Array.isArray(a.ops) && a.ops.length>0 && a.ops.every((op:{kind:string})=>["note.add","note.update","note.remove"].includes(op.kind));
  } catch { return false }
}
function noteLabel(n?: Note | null) {
  if(!n)return "无";
  const pitch=["C","C♯","D","D♯","E","F","F♯","G","G♯","A","A♯","B"][n.pitch%12]+(Math.floor(n.pitch/12)-1);
  return `${pitch} · 起点 ${n.start} tick · 时长 ${n.duration} tick · 力度 ${n.velocity} · 通道 ${n.channel+1}`;
}

export function ProjectEditApproval({call,conversation:c,projects:p}:{
  call:ToolCall;projects?:PreviewWorkspace;
  conversation:Pick<ConversationState,"approveCall"|"denyCall"|"approvalBusy">;
}) {
  const [preview,setPreview]=useState<ProjectEditPreview|null>(null);
  const [error,setError]=useState("");
  const [audioError,setAudioError]=useState("");
  const [audio,setAudio]=useState("");
  const [busy,setBusy]=useState(false);
  const [page,setPage]=useState(0);
  const seq=useRef(0);
  const clearPreview=p?.clearPreview;
  useEffect(()=>()=>{seq.current++;clearPreview?.(call.id);void ProjectService.StopProjectEditPreview(call.id).catch(()=>{})},[call.id,clearPreview]);
  useEffect(()=>{
    setAudio("");setBusy(false);
    return ()=>{seq.current++;void ProjectService.StopProjectEditPreview(call.id).catch(()=>{})};
  },[p?.activeID,call.id]);
  useEffect(()=>Events.On("icetudio:service-event",event=>{
    const e=event.data as {type?:string;projectId?:string};
    let projectID="";
    try{projectID=JSON.parse(call.arguments).projectId}catch{}
    if(projectID && e.projectId===projectID && ["project.changed","project.deleted"].includes(e.type??"")) {
      seq.current++;setBusy(false);
      setError("工程已修改，候选基准已过时；请丢弃并让助手重新提出。");
      setAudio("");clearPreview?.(call.id);
      void ProjectService.StopProjectEditPreview(call.id).catch(()=>{});
    }
  }),[call.arguments,call.id,clearPreview]);
  async function load() {
    const request=++seq.current;setBusy(true);setError("");
    try{const value=await ProjectService.PreviewProjectEdit(call.id);if(request===seq.current){setPreview(value);setPage(0)}}
    catch(e){if(request===seq.current)setError(String(e))}
    finally{if(request===seq.current)setBusy(false)}
  }
  async function play(variant:string) {
    const request=seq.current;setBusy(true);setAudioError("");
    try{
      await ProjectService.PlayProjectEditPreview(call.id,variant);
      if(request===seq.current)setAudio(variant==="original"?"正在试听原版":"正在试听候选");
      else await ProjectService.StopProjectEditPreview(call.id);
    }
    catch(e){if(request===seq.current)setAudioError(String(e))}
    finally{if(request===seq.current)setBusy(false)}
  }
  function stop() {
    seq.current++;setBusy(false);setAudio("");
    void ProjectService.StopProjectEditPreview(call.id).catch(e=>setAudioError(String(e)));
  }
  const disabled=busy||c.approvalBusy;
  const valid=!!preview&&!error;
  const changes=preview?.diff.changes??[];
  const rows=changes.slice(page*50,(page+1)*50);
  return <section className="approval-card music-edit-approval" aria-label="音乐修改候选">
    <strong>检查音乐修改候选</strong>
    <p>先查看音符差异，再试听或采用。尚未写入工程；采用后仍可用共同撤销恢复。</p>
    <button className="secondary" disabled={disabled} onClick={()=>void load()}>查看音符差异</button>
    {error&&<p role="alert">{error}</p>}
    {preview&&<>
      <p className="operation-target">工程「{preview.title}」 · 基础修订 {preview.baseRevision}</p>
      <p>新增 {preview.diff.added} · 修改 {preview.diff.updated} · 删除 {preview.diff.removed} 个音符</p>
      {preview.diff.total>changes.length&&<p>共 {preview.diff.total} 项变化，仅展示前 {changes.length} 项；试听与采用处理整批。</p>}
      <div className="note-diff-table"><table><thead><tr><th>音轨 / 片段</th><th>原值</th><th>候选值</th></tr></thead><tbody>
        {rows.map((change,i)=><tr key={page*50+i}><td>{change.trackName||"未命名音轨"} / {change.clipName||"片段"}</td><td>{noteLabel(change.before)}</td><td>{noteLabel(change.after)}</td></tr>)}
      </tbody></table></div>
      {changes.length>50&&<div className="actions"><button className="secondary" disabled={page===0} onClick={()=>setPage(page-1)}>上一组差异</button><span>第 {page+1} 组</span><button className="secondary" disabled={(page+1)*50>=changes.length} onClick={()=>setPage(page+1)}>下一组差异</button></div>}
      <div className="actions">
        {p&&<button className="secondary" disabled={disabled||!valid} onClick={()=>p.showPreview(preview)}>在卷帘查看</button>}
        <button className="secondary" disabled={disabled||!valid} onClick={()=>void play("original")}>试听原版</button>
        <button className="secondary" disabled={disabled||!valid} onClick={()=>void play("candidate")}>试听候选</button>
        <button className="secondary" onClick={stop}>停止候选试听</button>
      </div>
      {audio&&<p role="status">{audio} · 临时快照，未采用；音频状态以播放栏为准</p>}
      {audioError&&<p role="alert">{audioError}</p>}
    </>}
    <div className="actions">
      <button disabled={disabled||!valid} title={!valid?"先查看有效差异；过时或失败时请丢弃并重新提出":undefined} onClick={()=>{if(preview)void c.approveCall(call,preview.argumentsHash)}}>采用这次修改</button>
      <button className="secondary" disabled={disabled} onClick={()=>void c.denyCall(call)}>丢弃候选</button>
    </div>
    {!preview&&<p className="hint">查看差异后才能采用；不改变任务冻结的目标与权限。</p>}
    <details><summary>技术详情</summary><pre>{call.arguments}</pre></details>
  </section>;
}
