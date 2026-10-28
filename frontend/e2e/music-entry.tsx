// Isolated test entry; uses the production workspace and hooks with an
// authenticated bridge to the real Go service supplied by the Go test.
import React,{useEffect,useState} from "react";
import { createRoot } from "react-dom/client";
import { DialogProvider } from "../src/shared/dialogs";
import { useProjects } from "../src/hooks/useProjects";
import { ProjectWorkspace } from "../src/components/ProjectWorkspace";
import { CloseGuardProvider } from "../src/shared/closeGuard";
import { ToolApprovals } from "../src/components/ToolApprovals";
import type { ToolCall } from "../bindings/icetudio/internal/storage/models";
function MusicTest() {
  const projects = useProjects(null, "");
  const [pendingCalls,setPending]=useState<ToolCall[]>([]);
  useEffect(()=>{(window as any).musicShowPreview=(call:ToolCall|null)=>setPending(call?[call]:[]);return()=>{delete(window as any).musicShowPreview}},[]);
  const c={pendingCalls,unknownCalls:[],approvalBusy:false,
    approveCall:async(call:ToolCall,hash?:string)=>{await(window as any).musicCall("ApproveProjectEditPreview",call.id,hash);setPending([])},
    denyCall:async(call:ToolCall)=>{await(window as any).musicCall("DenyToolCall",call.id,"浏览器显式丢弃");setPending([])},
    resolveUnknown:async()=>{},
  };
  return <div style={{ padding: 20, height: "100vh", overflow: "auto" }}><ProjectWorkspace projects={projects} openModel={() => {}} openAssistants={() => {}}/><ToolApprovals conversation={c} projects={projects}/></div>;
}
createRoot(document.getElementById("root")!).render(<React.StrictMode><DialogProvider><CloseGuardProvider><MusicTest/></CloseGuardProvider></DialogProvider></React.StrictMode>);
