import { spawn } from "node:child_process";
import { closeSync, mkdirSync, mkdtempSync, openSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
// One-use browser artifacts are not a dependency/build cache. Keep them out
// of project .cache; retain only when a caller explicitly requests a directory.
const artifactParent = process.env.ICETUDIO_PRODUCT_ARTIFACT_DIR;
if (artifactParent) mkdirSync(artifactParent, { recursive: true });
const dir = mkdtempSync(resolve(artifactParent || tmpdir(), "icetudio-product-"));
const log = openSync(resolve(dir, "services.log"), "a");
const vite = spawn(
  "npm",
  ["run", "dev", "--", "--host", "127.0.0.1", "--port", "9391", "--strictPort"],
  {
    cwd: resolve(root, "frontend"),
    stdio: ["ignore", log, log],
    detached: true,
  },
);
const chrome = spawn(
  process.env.ICETUDIO_E2E_BROWSER || "chromium",
  [
    "--headless",
    "--no-sandbox",
    "--disable-gpu",
    "--disable-background-networking",
    "--no-first-run",
    `--user-data-dir=${resolve(dir, "chrome")}`,
    "--remote-debugging-port=9392",
    "about:blank",
  ],
  { stdio: ["ignore", log, log], detached: true },
);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let ws;
try {
  async function ready(url) {
    for (let i = 0; i < 100; i++) {
      try {
        const r = await fetch(url);
        if (r.ok) return r;
      } catch {}
      await sleep(100);
    }
    throw Error("unavailable " + url);
  }
  await ready("http://127.0.0.1:9391");
  const targets = await (await ready("http://127.0.0.1:9392/json/list")).json();
  ws = new WebSocket(
    targets.find((t) => t.type === "page").webSocketDebuggerUrl,
  );
  await new Promise((r, j) => {
    ws.onopen = r;
    ws.onerror = j;
  });
  let seq = 0;
  const pending = new Map();
  const errors = [];
  function send(method, params = {}) {
    const id = ++seq;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(Error("timeout " + method)), 20000);
      pending.set(id, { resolve, reject, timer });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }
  const backend = `
const blank=()=>({schemaVersion:1,description:'',systemPrompt:'',persona:'',model:{provider:'',baseUrl:'',name:''},allMcpServers:true,enabledMcpServers:[],allowedTools:[],allSkills:false,enabledSkills:[],contextBudget:8192,planEnabled:false,delegation:{enabled:false,maxDepth:0,maxConcurrency:0,maxSeconds:0},execution:{workflow:'direct',stages:[],main:{},planner:{},reviewer:{},improver:{},worker:{},workerSources:[],budget:{},review:{mode:'off'},improverEnabled:true}});
const config=(id,name)=>({id,name,isDefault:id==='default',revision:1,data:blank(),createdAt:'2026-10-17',updatedAt:'2026-10-17'});
const state=window.__product={n:0,configs:[config('default','创作助手')],conversations:[],contexts:{},runs:{},messages:{},pending:[],grants:{},model:{provider:'ollama',baseUrl:'http://127.0.0.1:11434/v1',name:''},savedKey:false,approvals:[],skillDrafts:[],previewRemote:false,skills:[{name:'report-check',description:'检查报告结构',managed:false}],mcpConfig:[],projects:[],projectHistory:{},projectOps:{},projectResults:{},lastExport:null};
const status=()=>({storageReady:true,runtimeReady:!!state.model.name,modelConnected:!!state.model.name,model:state.model,availableModels:['mock'],message:state.model.name?'已连接':'请连接模型'});
export const DesktopService={Ready:async()=>{},CancelClose:async()=>{},ConfirmClose:async()=>{}};
export const PlaybackService={Status:async()=>({available:false,ready:false,playing:false,settings:{version:2,driver:'pulseaudio',fonts:[],defaultFont:''},resolvedFont:'Basic Waves（内置基础波形）',resolvedKind:'basic',notice:'',error:'',engine:'UI mock',sampleRate:48000}),Stop:async()=>{},StopProject:async()=>{},Play:async()=>{},Seek:async()=>{},Configure:async()=>{},PickSoundFont:async()=>'',AddSoundFont:async()=>({id:'font_test',name:'local.sf2',path:'/test/local.sf2',priority:0,addedAt:''}),RemoveSoundFont:async()=>{},SetDefaultSoundFont:async()=>{},MoveSoundFont:async()=>{},ResetAudioSettings:async()=>{},Audition:async()=>{},StopAudition:async()=>{},SoundFonts:async()=>({bundled:{id:'bundled',name:'MuseScore_General.sf3',path:'',source:'bundled',exists:false,size:0,error:'',active:false,isDefault:true,priority:-1},basic:{id:'basic',name:'Basic Waves（内置基础波形）',path:'',source:'basic',exists:true,size:0,error:'',active:true,isDefault:false,priority:1073741824},fonts:[],defaultFont:'',notice:''})};
export const StatusService={
GetStatus:async()=>structuredClone(status()),ListCredentialStatus:async()=>[{provider:'openai',configured:state.savedKey}],SaveCredential:async(p,key)=>{if(key!=='mock-key')throw Error('unexpected key');state.savedKey=true},DeleteCredential:async()=>{state.savedKey=false},SaveModelConfig:async(m)=>{state.model=structuredClone(m);return structuredClone(status())},ListMCPEndpoints:async()=>[],
ListRuntimeConfigs:async()=>{if(state.failProfiles)throw Error('助手列表加载失败');return structuredClone(state.configs)},GetRuntimeConfig:async(id)=>structuredClone(state.configs.find(p=>p.id===id)),UpdateRuntimeConfig:async(p)=>{const cur=state.configs.find(v=>v.id===p.id);if(!cur)throw Error('助手不存在');if(cur.revision!==p.revision)throw Error('执行配置已被其他操作修改，请刷新后重试');const saved={...structuredClone(p),revision:p.revision+1,updatedAt:'2026-10-17'};state.configs=state.configs.map(v=>v.id===p.id?saved:v);return structuredClone(saved)},RestoreRuntimeConfigDefault:async(revision)=>{const cur=state.configs.find(p=>p.id==='default');if(!cur||cur.revision!==revision)throw Error('执行配置已被其他操作修改，请刷新后重试');const d=config('default','创作助手');d.revision=revision+2;state.configs=state.configs.map(v=>v.id==='default'?d:v);return structuredClone(d)},
ListConversations:async()=>{if(state.failList)throw Error('列表刷新失败');return structuredClone(state.conversations)},GetConversation:async(id)=>structuredClone(state.conversations.find(c=>c.id===id)),CreateConversation:async(configId,reuse,title)=>{if(state.failCreate)throw Error('创建失败，请重试');if(state.delayCreate)await new Promise(r=>setTimeout(r,500));configId=configId||'default';const id='c'+(++state.n),contextId=reuse?state.conversations.find(c=>c.id===reuse).contextId:'ctx'+state.n,c={id,configId,contextId,title:title||'新对话',kind:'normal',configName:state.configs.find(p=>p.id===configId).name,configArchived:false,createdAt:'2026-10-17',projectId:'',options:{webSearch:false}};state.contexts[contextId]??={id:contextId,name:c.title,policy:{approvalMode:'semi',permissions:{unrestricted:false,workspacePaths:[],commands:[],networkHosts:[]}}};state.conversations.unshift(c);state.messages[id]=[];return structuredClone(c)},RenameConversation:async(id,title)=>{state.conversations.find(c=>c.id===id).title=title},DeleteConversation:async(id)=>{state.conversations=state.conversations.filter(c=>c.id!==id);delete state.messages[id]},
GetLocalContext:async(id)=>structuredClone(state.contexts[id]),UpdateContextPolicy:async(id,policy)=>{state.contexts[id].policy=structuredClone(policy);return structuredClone(state.contexts[id])},ListExfilGrants:async(id)=>structuredClone(state.grants[id]??[]),GrantExfil:async(id,destination,scopes)=>{state.grants[id]=[{destination,scopes}]},RevokeExfil:async(id)=>{state.grants[id]=[]},PreviewSend:async(id)=>{const c=state.conversations.find(c=>c.id===id);return state.previewRemote&&!state.grants[c.contextId]?.length?[{provider:'openai',destination:'https://custom.example',scopes:['personal','unknown'],missing:['personal','unknown']}]:[]},
ConversationSearchAvailability:async()=>({available:false,reason:'模拟能力不可用'}),ListConversationSearch:async()=>[],SetConversationOptions:async(id,options)=>{const c=state.conversations.find(c=>c.id===id);c.options=structuredClone(options);return structuredClone(c)},
ListMessages:async(id)=>structuredClone(state.messages[id]??[]),ListChatMessages:async(id)=>({messages:structuredClone(state.messages[id]??[]).map((m,i)=>({...m,seq:i+1,sessionId:id})),beforeSeq:1,hasMore:false}),ListRuns:async(id)=>structuredClone(Object.values(state.runs).filter(r=>r.sessionId===id).reverse()),ListToolCalls:async()=>[],PendingApprovals:async()=>structuredClone(state.pending),GetSessionSummary:async()=>{throw Error('none')},GetSessionPlan:async()=>{throw Error('none')},ListChildRuns:async()=>[],GetTaskGraph:async()=>({tasks:[],attempts:[],artifacts:[],coordination:{state:'complete',rounds:0},continuations:[]}),ListTaskReviews:async()=>[],ListTaskRetrospectives:async()=>[],PendingStreamText:async()=>'',
StartConversationTask:async(id,input,scope,uploads)=>{state.lastSend={id,input,scope,uploads};uploads=uploads??[];const c=state.conversations.find(c=>c.id===id),r={id:'r'+(++state.n),sessionId:id,contextId:c.contextId,profileId:c.configId,profileRevision:0,component:'main',status:'completed',createdAt:new Date().toISOString()};state.runs[r.id]=r;c.title=input.slice(0,32)||'附件分析';state.messages[id].push({id:r.id+'u',runId:r.id,role:'user',content:input,attachments:uploads.map(u=>({name:u.name,size:10,mediaType:u.name.endsWith('.png')?'image/png':'text/plain'}))},{id:r.id+'a',runId:r.id,role:'assistant',content:'# 回答\\n\\n- 已处理材料\\n\\n| 项目 | 状态 |\\n| --- | --- |\\n| 文件 | 完成 |\\n\\n\u0060\u0060\u0060js\\nconst answer = 42;\\n\u0060\u0060\u0060\\n\\n<script>window.__unsafe=true</script>'});return {...r,status:'running'}},GetRun:async(id)=>structuredClone(state.runs[id]),CancelTask:async(id)=>{state.runs[id].status='cancelled'},
ApproveToolCall:async(id)=>{state.approvals.push(id);state.pending=state.pending.filter(c=>c.id!==id)},DenyToolCall:async(id)=>{state.denied=id;state.pending=state.pending.filter(c=>c.id!==id)},ResolveToolCall:async(id,status,result,note)=>{state.resolution={id,status,result,note};state.pending=state.pending.filter(c=>c.id!==id)},
PickFolder:async()=>'/home/mock/Documents',MessageAttachment:async()=>({name:'note.txt',mediaType:'text/plain',text:'原始附件内容'}),
ListSkillDrafts:async()=>state.skillDrafts,
ListSkills:async()=>structuredClone(state.skills),ListConfiguredMCPServers:async()=>structuredClone(state.mcpConfig),
SaveMCPServer:async(s)=>{if(!s.name||!s.command)throw Error('MCP 服务名称或命令无效');if(s.name==='local'||s.name.includes('__'))throw Error('MCP 服务名称无效');state.mcpConfig=state.mcpConfig.filter(v=>v.name!==s.name);state.mcpConfig.push(structuredClone(s));return structuredClone(state.mcpConfig)},
DeleteMCPServer:async(name)=>{if(!state.mcpConfig.some(v=>v.name===name))throw Error('未配置 MCP 服务 '+name);state.mcpConfig=state.mcpConfig.filter(v=>v.name!==name);return structuredClone(state.mcpConfig)},
ConfirmMCPEndpoint:async()=>[],ReadHostLogs:async()=>['2026-10-16T10:00:00 mock: host started','2026-10-16T10:00:01 mock: model connected'],ListWorkerTraces:async()=>[],
};
const findProject=(id)=>state.projects.find(p=>p.id===id);
const projectSummary=(p)=>({id:p.id,title:p.title,ppq:p.ppq,revision:p.revision,createdAt:p.createdAt,updatedAt:p.updatedAt});
const newProject=(title)=>{const p={id:'pr'+(++state.n),title:title||'未命名工程',ppq:480,revision:0,state:{title:title||'未命名工程',ppq:480,tempo:[{tick:0,usPerQuarter:500000}],meter:[{tick:0,numerator:4,denominator:4}],tracks:[]},createdAt:'2026-10-17',updatedAt:'2026-10-17'};state.projects.push(p);state.projectHistory[p.id]=[];return p};
const staleRevision=(p,base)=>base!==p.revision?Promise.reject(Error('工程已被其他操作修改（当前修订 '+p.revision+'，请求基于 '+base+'）')):null;
// 工程条模拟：与真实语义一致的重放幂等、过时修订拒绝、线性撤销/重做。
export const ProjectService={GetTimeMapDraft:async()=>null,SaveTimeMapDraft:async()=>{},ClearTimeMapDraft:async()=>{},GetPendingProjectEdit:async()=>null,BeginProjectEdit:async()=>{},ClearPendingProjectEdit:async()=>{},GetMusicInputs:async()=>null,SaveMusicInputs:async()=>{},ClearMusicInputs:async()=>{},
ListProjects:async()=>state.projects.map(projectSummary),
GetProject:async(id)=>{const p=findProject(id);if(!p)throw Error('工程不存在');return structuredClone(p)},
CreateProject:async(title)=>structuredClone(newProject(title)),
DeleteProject:async(id)=>{state.projects=state.projects.filter(p=>p.id!==id);delete state.projectHistory[id];state.conversations.forEach(c=>{if(c.projectId===id)c.projectId=''})},
SubmitProjectOps:async(projectId,opId,source,baseRevision,ops)=>{const p=findProject(projectId);if(!p)throw Error('工程不存在');if(state.projectOps[opId])return structuredClone(state.projectOps[opId]);const stale=staleRevision(p,baseRevision);if(stale)return stale;const inverse=[];for(const op of ops){if(op.kind!=='project.rename')throw Error('模拟环境不支持操作 '+op.kind);inverse.push({kind:'project.rename',title:p.title});p.title=op.title}p.revision++;p.updatedAt='2026-10-17';state.projectHistory[projectId].push({opId:opId,kind:'edit',ops:structuredClone(ops),inverse:inverse,undone:false,revision:p.revision});const result={opId:opId,revision:p.revision,baseRevision:baseRevision,source:source,applied:ops.length,summary:{rename:ops.length},createdIds:[]};state.projectOps[opId]=result;state.projectResults[opId]={found:true,result:result};return structuredClone(result)},
UndoProject:async(projectId,opId,baseRevision)=>{const p=findProject(projectId);if(!p)throw Error('工程不存在');const stale=staleRevision(p,baseRevision);if(stale)return stale;const entry=[...state.projectHistory[projectId]].reverse().find(h=>h.kind==='edit'&&!h.undone);if(!entry)throw Error('没有可撤销的编辑');for(const op of entry.inverse)p.title=op.title;entry.undone=true;p.revision++;const result={opId:opId,revision:p.revision,baseRevision:baseRevision,source:'gui',applied:entry.inverse.length,summary:{undo:1},createdIds:[]};state.projectOps[opId]=result;state.projectResults[opId]={found:true,result:result};return structuredClone(result)},
RedoProject:async(projectId,opId,baseRevision)=>{const p=findProject(projectId);if(!p)throw Error('工程不存在');const stale=staleRevision(p,baseRevision);if(stale)return stale;const entry=[...state.projectHistory[projectId]].reverse().find(h=>h.undone);if(!entry)throw Error('没有可重做的编辑');for(const op of entry.ops)p.title=op.title;entry.undone=false;p.revision++;const result={opId:opId,revision:p.revision,baseRevision:baseRevision,source:'gui',applied:entry.ops.length,summary:{redo:1},createdIds:[]};state.projectOps[opId]=result;state.projectResults[opId]={found:true,result:result};return structuredClone(result)},
GetProjectTransaction:async(projectId,opId)=>state.projectResults[opId]?structuredClone(state.projectResults[opId]):{found:false},
ProjectHistory:async(projectId)=>structuredClone(state.projectHistory[projectId]??[]),
SetConversationProject:async(conversationId,projectId)=>{const c=state.conversations.find(c=>c.id===conversationId);if(!c)throw Error('对话不存在');c.projectId=projectId},
ImportProjectMIDI:async(path)=>structuredClone(newProject('导入的工程')),
ExportProjectMIDI:async(projectId,path)=>{if(!findProject(projectId))throw Error('工程不存在');state.lastExport={projectId:projectId,path:path}},
PickOpenMIDIFile:async()=>'/home/mock/imports/example.mid',
PickSaveMIDIFile:async(name)=>'/home/mock/exports/'+name,
};`;
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id) {
      const p = pending.get(m.id);
      if (p) {
        clearTimeout(p.timer);
        pending.delete(m.id);
        m.error
          ? p.reject(Error(JSON.stringify(m.error)))
          : p.resolve(m.result);
      }
    } else if (m.method === "Fetch.requestPaused") {
      send("Fetch.fulfillRequest", {
        requestId: m.params.requestId,
        responseCode: 200,
        responseHeaders: [
          { name: "Content-Type", value: "application/javascript" },
        ],
        body: Buffer.from(backend).toString("base64"),
      }).catch((e) => errors.push(e.message));
    } else if (m.method === "Runtime.exceptionThrown")
      errors.push(
        m.params.exceptionDetails.exception?.description ??
          m.params.exceptionDetails.text,
      );
    else if (m.method === "Runtime.consoleAPICalled" &&
      m.params.args.some(arg => String(arg.value ?? arg.description ?? "").includes("Maximum update depth exceeded")))
      errors.push("React exceeded the maximum update depth");
  };
  await send("Runtime.enable");
  await send("Page.enable");
  await send("Network.enable");
  await send("Network.setCacheDisabled", { cacheDisabled: true });
  await send("Page.addScriptToEvaluateOnNewDocument", {
    source: "try{localStorage.clear();sessionStorage.clear()}catch{}",
  });
  await send("Fetch.enable", {
    patterns: [
      { urlPattern: "*/bindings/icetudio/index.*", requestStage: "Request" },
    ],
  });
  await send("Emulation.setDeviceMetricsOverride", {
    width: 1366,
    height: 850,
    deviceScaleFactor: 1,
    mobile: false,
  });
  await send("Page.navigate", { url: "http://127.0.0.1:9391" });
  async function evaluate(expression) {
    const r = await send("Runtime.evaluate", {
      expression,
      returnByValue: true,
      awaitPromise: true,
    });
    if (r.exceptionDetails)
      throw Error(
        r.exceptionDetails.exception?.description ?? r.exceptionDetails.text,
      );
    return r.result.value;
  }
  async function until(expr) {
    for (let i = 0; i < 140; i++) {
      if (await evaluate(`Boolean(${expr})`)) return;
      await sleep(100);
    }
    throw Error(
      "not reached: " +
        expr +
        (errors.length ? "\n  page errors: " + errors.join("\n  ") : "") +
        "\n  state: " +
        (await evaluate(
          `JSON.stringify({n:window.__product?.conversations?.length,v:document.querySelector('[aria-label="发送消息"]')?.value,alert:document.querySelector('[role=alert]')?.innerText??"",dialog:document.querySelector('dialog[open]')?.innerText.slice(0,150)??"none",ls:Object.keys(localStorage).filter(k=>k.includes("draft"))})`,
        ).catch((e) => "unavailable: " + e.message)),
    );
  }
  async function click(text) {
    await until(
      `Array.from(document.querySelectorAll('button')).some(e=>e.textContent.trim()===${JSON.stringify(text)}&&!e.disabled)`,
    );
    await evaluate(
      `(()=>{const b=Array.from(document.querySelectorAll('button')).find(e=>e.textContent.trim()===${JSON.stringify(text)});if(!b)throw Error('missing button '+${JSON.stringify(text)});b.click()})()`,
    );
  }
  // Scope clicks to a container: names like 重命名/删除 exist in several panels.
  async function clickIn(scope, text) {
    const s = JSON.stringify(scope);
    await until(
      `(()=>{const c=document.querySelector(${s});return c&&Array.from(c.querySelectorAll('button')).some(e=>e.textContent.trim()===${JSON.stringify(text)}&&!e.disabled)})()`,
    );
    await evaluate(
      `(()=>{const c=document.querySelector(${s});const b=Array.from(c.querySelectorAll('button')).find(e=>e.textContent.trim()===${JSON.stringify(text)});if(!b)throw Error('missing button '+${JSON.stringify(text)});b.click()})()`,
    );
  }
  async function fill(selector, value) {
    await evaluate(
      `(()=>{const t=document.querySelector(${JSON.stringify(selector)});if(!t)throw Error('missing input');Object.getOwnPropertyDescriptor(t instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(t,${JSON.stringify(value)});t.dispatchEvent(new Event('input',{bubbles:true}))})()`,
    );
  }
  // 启动即工程工作区：没有对话时输入框就是入口，发送第一条消息才创建对话。
  await until(
    `window.__product?.conversations.length===0 && document.querySelector('[aria-label="发送消息"]') && document.body.innerText.includes('输入消息，直接开始新对话')`,
  );
  await new Promise((r) => setTimeout(r, 400));
  writeFileSync(
    resolve(dir, "draft-home.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  if (
    await evaluate(
      `/AGENT HARNESS|Profiles|本地 Agent 宿主/.test(document.body.innerText)`,
    )
  )
    throw Error("runtime jargon on home");
  if (
    !(await evaluate(
      `document.body.innerText.includes('开始创作') && document.body.innerText.includes('新建工程') && document.body.innerText.includes('导入 MIDI')`,
    ))
  )
    throw Error("workspace must explain project creation and audition entry");
  // 工程在左栏（新建/导入/列表），中间只有编辑器命令条；绑定对话在右侧 agent 栏。
  await clickIn(".browser", "＋ 新建工程");
  await fill("dialog[open] input[type=text]", "示例工程");
  await clickIn("dialog[open]", "创建");
  await until(
    `Array.from(document.querySelectorAll('.project-select')).some(b=>b.textContent.includes('示例工程'))`,
  );
  await clickIn(".transport-bar", "示例工程");
  await fill("dialog[open] input[type=text]", "夜曲");
  await clickIn("dialog[open]", "应用");
  await until(
    `window.__product.projects[0].revision===1 && document.body.innerText.includes('夜曲')`,
  );
  await clickIn(".transport-bar", "撤销");
  await until(
    `window.__product.projects[0].revision===2 && document.body.innerText.includes('示例工程')`,
  );
  await clickIn(".transport-bar", "重做");
  await until(
    `window.__product.projects[0].revision===3 && document.body.innerText.includes('夜曲')`,
  );
  if ((await evaluate(`window.__product.projects[0].title`)) !== "夜曲")
    throw Error("undo/redo lost the edit");
  await clickIn(".browser", "导入 MIDI");
  await until(
    `window.__product.projects.length===2 && document.body.innerText.includes('导入的工程')`,
  );
  await clickIn(".transport-bar", "导出 MIDI");
  await until(
    `window.__product.lastExport?.path.endsWith('.mid') && window.__product.lastExport.projectId===window.__product.projects[1].id`,
  );
  // 连接模型后，输入框直接开始：第一条消息创建对话。
  await click("连接模型");
  await until(`document.querySelector('.model-form')`);
  await evaluate(
    `(()=>{const s=document.querySelector('.model-form select');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,'openai');s.dispatchEvent(new Event('change',{bubbles:true}))})()`,
  );
  await until(`document.querySelector('input[type=password]')`);
  await fill("input[type=password]", "mock-key");
  await fill('input[list="available-models"]', "mock");
  await click("保存并连接");
  await until(`document.body.innerText.includes('模型已连接')`);
  if (!(await evaluate(`window.__product.savedKey`)))
    throw Error("Unified model setup did not save credentials");
  await click("开始聊天 →");
  await fill('[aria-label="发送消息"]', "第一条消息");
  await click("发送 ↑");
  await until(
    `window.__product.conversations.length===1 && window.__product.lastSend?.input==='第一条消息'`,
  );
  // 工程内新对话：发送即绑定编辑器当前工程（导入的工程）；无片段目标时不携带范围。
  await until(
    `window.__product.conversations.some(c=>c.projectId===window.__product.projects[1].id)`,
  );
  if ((await evaluate(`window.__product.lastSend?.scope`)) !== null)
    throw Error("clip-free editor target must send a whole-project scope");
  await clickIn(".agent-dock", "解绑工程");
  await until(
    `window.__product.conversations.every(c=>!c.projectId)`,
  );
  await clickIn(".agent-dock", "绑定当前工程");
  await until(
    `window.__product.conversations.some(c=>c.projectId===window.__product.projects[1].id)`,
  );
  await evaluate(`document.querySelectorAll('.project-remove')[1].click()`);
  await until(
    `document.querySelector('dialog[open]')?.innerText.includes('删除工程')`,
  );
  await clickIn("dialog[open]", "删除");
  await until(
    `window.__product.projects.length===1 && document.body.innerText.includes('夜曲')`,
  );
  if ((await evaluate(`window.__product.conversations.some(c=>c.projectId)`)))
    throw Error("deleted project kept the conversation binding");
  // 未发送文字按对话保留；对话内用输入框工具条的「新对话」回到待输入状态，不创建对话。
  await fill('[aria-label="发送消息"]', "保留未发送的内容");
  await click("新对话");
  await until(
    `document.querySelector('[aria-label="发送消息"]').value===''`,
  );
  if ((await evaluate(`window.__product.conversations.length`)) !== 1)
    throw Error("new-draft entry created a conversation");
  await evaluate(`document.querySelector('.session-link').click()`);
  await until(
    `document.querySelector('[aria-label="发送消息"]').value==='保留未发送的内容'`,
  );
  await evaluate(
    `(()=>{const d=new DataTransfer();d.items.add(new File(['note body'],'note.txt',{type:'text/plain'}));const input=document.querySelector('[aria-label="添加对话附件"]');input.files=d.files;input.dispatchEvent(new Event('change',{bubbles:true}))})()`,
  );
  await until(
    `document.querySelector('.draft-attachments')?.innerText.includes('note.txt')`,
  );
  await evaluate(
    `(()=>{const dt=new DataTransfer();dt.items.add(new File([Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jY9sAAAAASUVORK5CYII='),c=>c.charCodeAt(0))],'pasted.png',{type:'image/png'}));document.querySelector('[aria-label="发送消息"]').dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}))})()`,
  );
  await until(`document.querySelector('.attachment-thumb')?.naturalWidth===1`);
  await evaluate(
    `(()=>{const dt=new DataTransfer();dt.items.add(new File(['drop body'],'dropped.txt',{type:'text/plain'}));document.querySelector('.composer').dispatchEvent(new DragEvent('drop',{dataTransfer:dt,bubbles:true,cancelable:true}))})()`,
  );
  await until(
    `document.querySelector('.draft-attachments').innerText.includes('dropped.txt')`,
  );
  // Enter submits; a composing Enter must not submit.
  const sentBefore = await evaluate(`window.__product.lastSend?.input`);
  await evaluate(
    `document.querySelector('[aria-label="发送消息"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}))`,
  );
  if ((await evaluate(`window.__product.lastSend?.input`)) !== sentBefore)
    throw Error("IME submitted");
  await evaluate(
    `document.querySelector('[aria-label="发送消息"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}))`,
  );
  await until(
    `document.querySelector('.markdown h1') && document.querySelector('.markdown table') && document.querySelector('.code-block')`,
  );
  if (await evaluate(`!!window.__unsafe`))
    throw Error("unsafe markdown executed");
  await until(`!document.querySelector('.draft-attachments')`);
  await click("note.txt");
  await until(
    `document.querySelector('.attachment-preview')?.innerText.includes('原始附件内容')`,
  );
  await click("关闭预览");
  // Unsupported search must offer a usable plain-chat choice, not disable send.
  await evaluate(`document.querySelector('.search-toggle input').click()`);
  await fill('[aria-label="发送消息"]', "不要联网也可以回答");
  await click("发送 ↑");
  await until(
    `document.querySelector('dialog[open]')?.innerText.includes('当前模型暂不支持联网')`,
  );
  await click("关闭联网并发送");
  await until(`window.__product.lastSend.input==='不要联网也可以回答'`);
  await sleep(1100);
  // Concrete destination consent; cancellation retains the draft and sends zero data.
  await evaluate(`window.__product.previewRemote=true`);
  await fill('[aria-label="发送消息"]', "需要授权的消息");
  await click("发送 ↑");
  await until(
    `document.querySelector('dialog[open]')?.innerText.includes('https://custom.example')`,
  );
  await click("暂不发送");
  await until(`!document.querySelector('dialog[open]')`);
  if (await evaluate(`window.__product.lastSend.input==='需要授权的消息'`))
    throw Error("send without consent");
  await click("发送 ↑");
  await until(`document.querySelector('dialog[open]')`);
  await click("允许并继续");
  await until(`window.__product.lastSend.input==='需要授权的消息'`);
  await sleep(1100);
  // Folder picker grants only after an explicit, scoped confirmation.
  await click("选择文件夹");
  await until(
    `document.querySelector('dialog[open]')?.innerText.includes('/home/mock/Documents')`,
  );
  await click("允许并添加到消息");
  await until(
    `document.querySelector('[aria-label="发送消息"]').value.includes('/home/mock/Documents')`,
  );
  // A hidden worker request is surfaced in the main chat, with action language.
  await evaluate(
    `window.__product.pending=[{id:'child-call',runId:'hidden-child',toolName:'local__write_file',arguments:JSON.stringify({path:'/home/mock/Documents/report.md',content:'报告'}),state:'pending'}]`,
  );
  await fill('[aria-label="发送消息"]', "触发审批同步");
  await click("发送 ↑");
  await until(`document.body.innerText.includes('IceTudio 想写入文件')`);
  await click("允许这次操作");
  await until(`window.__product.approvals.includes('child-call')`);
  await evaluate(
    `window.__product.pending=[{id:'command-call',runId:'child',toolName:'local__exec',arguments:JSON.stringify({executable:'/bin/echo',args:['hello'],cwd:'/home/mock'}),state:'pending'},{id:'unknown-call',runId:'child',toolName:'local__write_file',arguments:JSON.stringify({path:'/home/mock/report.md',content:'report'}),state:'execution_unknown'}]`,
  );
  await fill('[aria-label="发送消息"]', "检查审批与恢复");
  await click("发送 ↑");
  await until(
    `document.body.innerText.includes('/bin/echo') && document.body.innerText.includes('hello')`,
  );
  await click("拒绝");
  await until(
    `document.querySelector('dialog[open]')?.innerText.includes('拒绝这次操作')`,
  );
  await click("取消");
  if (await evaluate(`!!window.__product.denied`))
    throw Error("Cancel still denied tool");
  await click("拒绝");
  await until(`document.querySelector('dialog[open] textarea')`);
  await fill("dialog[open] textarea", "这次不用执行");
  await click("拒绝操作");
  await until(`window.__product.denied==='command-call'`);
  await click("暂不确定");
  if (await evaluate(`!!window.__product.resolution`))
    throw Error("Uncertain result was resolved");
  await click("已核实未完成");
  await until(`document.querySelector('dialog[open] textarea')`);
  await fill("dialog[open] textarea", "我检查了目标文件，文件尚未生成。");
  await click("记录核对结论");
  await until(`window.__product.resolution?.status==='failed'`);
  // 助手编辑：未保存修改跨页面保留；保存走 UpdateRuntimeConfig（修订 +1）。
  await click("管理助手");
  await until(`document.querySelector('#assistant-editor')`);
  await fill("#assistant-editor input[required]", "未保存的助手名称");
  await click("返回工程");
  await click("管理助手");
  await until(
    `document.querySelector('#assistant-editor input[required]').value==='未保存的助手名称'`,
  );
  await click("保存助手");
  await until(`window.__product.configs[0].name==='未保存的助手名称'`);
  // Normal navigation does not change which assistant an existing chat uses.
  await click("返回工程");
  await until(`document.querySelector('.chat-page')`);
  await evaluate(
    `document.querySelector('.conversation-menu summary').click()`,
  );
  await clickIn(".agent-dock", "重命名");
  await until(`document.querySelector('dialog[open] input[type=text]')`);
  await fill("dialog[open] input[type=text]", "资料整理");
  await click("保存");
  await until(
    `document.querySelector('.session-link').textContent==='资料整理'`,
  );
  await fill(".conversation-search input", "资料整理");
  await until(`document.querySelectorAll('.session-row').length===1`);
  await fill(".conversation-search input", "");
  await send("Page.captureScreenshot", { format: "png" }).then((r) =>
    writeFileSync(resolve(dir, "workspace.png"), Buffer.from(r.data, "base64")),
  );
  await send("Emulation.setDeviceMetricsOverride", {
    width: 800,
    height: 600,
    deviceScaleFactor: 1,
    mobile: false,
  });
  if (await evaluate(`document.documentElement.scrollWidth>innerWidth`))
    throw Error("horizontal overflow");
  if (
    await evaluate(
      `document.querySelector('.message-list').scrollHeight>document.querySelector('.message-list').clientHeight+1`,
    )
  )
    throw Error("nested message scroll");
  // Failure and delayed responses must not strand navigation or revert successful mutations.
  await click("⚙ 设置");
  await click("开发者设置");
  if (
    await evaluate(
      `document.body.innerText.includes('已保存的模型密钥') || !!document.querySelector('input[type=password]')`,
    )
  )
    throw Error("Duplicate credential editor in developer settings");
  await click("返回工程");
  // 对话只在发送时创建：创建失败在输入框旁报错，文字保留可重试。
  await evaluate(`window.__product.failCreate=true`);
  await click("新对话");
  await fill('[aria-label="发送消息"]', "创建失败也要保留文字");
  await click("发送 ↑");
  await until(
    `document.querySelector('.chat-page') && document.querySelector('[role=alert]')?.innerText.includes('创建失败')`,
  );
  await until(
    `document.querySelector('[aria-label="发送消息"]').value==='创建失败也要保留文字'`,
  );
  // 本段只验证创建失败与延迟；关闭远端预览，避免新对话触发无关的目的地授权弹窗。
  await evaluate(
    `window.__product.failCreate=false;window.__product.delayCreate=true;window.__product.previewRemote=false`,
  );
  await click("发送 ↑");
  await until(
    `Array.from(document.querySelectorAll('button')).some(b=>b.textContent==='发送中…'&&b.disabled)`,
  );
  await until(
    `window.__product.conversations.length===2 && document.querySelector('[aria-label="发送消息"]').value===''`,
  );
  await evaluate(`window.__product.delayCreate=false`);
  // 删除当前对话需要确认，删除后回到剩余对话。
  await evaluate(
    `document.querySelector('.conversation-menu summary').click()`,
  );
  await click("删除对话");
  await until(
    `document.querySelector('dialog[open]')?.innerText.includes('无法恢复')`,
  );
  await click("取消");
  if ((await evaluate(`window.__product.conversations.length`)) !== 2)
    throw Error("Cancelled deletion removed conversation");
  await evaluate(
    `document.querySelector('.conversation-menu summary').click()`,
  );
  await click("删除对话");
  await until(`document.querySelector('dialog[open]')`);
  await evaluate(
    `Array.from(document.querySelectorAll('dialog[open] button')).find(b=>b.textContent.trim()==='删除对话').click()`,
  );
  await until(`window.__product.conversations.length===1`);
  // 助手产品只有一个内置创作助手：没有配置库入口；恢复默认走 configs.restoreDefault。
  await click("管理助手");
  await until(`document.querySelector('#assistant-editor')`);
  if (await evaluate(`!!document.querySelector('#assistant-editor select')`))
    throw Error("assistant picker should be gone");
  if (
    await evaluate(
      `Array.from(document.querySelectorAll('button')).some(b=>['＋ 创建助手','复制当前','使用这个助手开始新对话'].includes(b.textContent.trim()))`,
    )
  )
    throw Error("assistant library entries should be gone");
  await evaluate(
    `document.querySelector('.settings-page').scrollTop=0;document.querySelector('#assistant-editor').scrollIntoView({block:'start'})`,
  );
  await new Promise((r) => setTimeout(r, 400));
  writeFileSync(
    resolve(dir, "assistant-editor.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  await click("恢复默认配置");
  await until(`document.querySelector('dialog[open]')`);
  await click("恢复默认");
  await until(
    `window.__product.configs[0].name==='创作助手' && window.__product.configs[0].revision===4`,
  );
  await click("返回工程");
  await until(
    `document.querySelector('.chat-page') && document.querySelector('.assistant-name')?.textContent==='创作助手'`,
  );
  if (await evaluate(`!!document.querySelector('[aria-label="当前助手"]')`))
    throw Error("per-chat assistant switcher should be gone");
  // 权限合并：这里只保存编辑过的字段，不覆盖聊天中新授权的文件夹。
  await click("⚙ 设置");
  await click("权限");
  await until(`document.querySelector('.model-form select')`);
  await evaluate(
    `(()=>{const s=document.querySelector('.model-form select');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,'manual');s.dispatchEvent(new Event('change',{bubbles:true}))})()`,
  );
  await click("返回工程");
  await evaluate(
    `window.__product.contexts[window.__product.conversations[0].contextId].policy.permissions.workspacePaths=['/newly-authorized']`,
  );
  await click("⚙ 设置");
  await click("权限");
  await click("保存偏好");
  await until(
    `window.__product.contexts[window.__product.conversations[0].contextId].policy.approvalMode==='manual'`,
  );
  if (
    !(await evaluate(
      `window.__product.contexts[window.__product.conversations[0].contextId].policy.permissions.workspacePaths.includes('/newly-authorized')`,
    ))
  )
    throw Error("Saving preferences overwrote folder consent");
  await click("返回工程");
  await click("⚙ 设置");
  await click("权限");
  await until(`document.body.innerText.includes('/newly-authorized')`);
  await click("模型连接");
  await evaluate(
    `Array.from(document.querySelectorAll('summary')).find(e=>e.textContent==='管理已保存的密钥').click()`,
  );
  await click("删除密钥");
  await until(`document.querySelector('dialog[open]')`);
  await click("取消");
  if (!(await evaluate(`window.__product.savedKey`)))
    throw Error("Cancelled key deletion removed credential");
  await click("删除密钥");
  await until(`document.querySelector('dialog[open]')`);
  await evaluate(
    `Array.from(document.querySelectorAll('dialog[open] button')).find(b=>b.textContent==='删除密钥').click()`,
  );
  await until(
    `!window.__product.savedKey && document.body.innerText.includes('尚未保存密钥')`,
  );
  await click("返回工程");
  console.log(
    "PASS: honest workspace home, send-creates-conversation with per-conversation drafts, unified model connection, single-assistant editing with restore-default, live permission merge without overwriting folder consent, credential lifecycle, and failed/delayed creation surfaced at the composer.",
  );
  // MCP 服务在普通设置安装；助手编辑页从目录勾选，而不是手填名称。
  await click("⚙ 设置");
  await click("技能与服务");
  await until(`document.querySelector('[aria-label="MCP 服务管理"]')`);
  await click("＋ 接入 MCP 服务");
  await evaluate(
    `(()=>{const f=document.querySelector('[aria-label="MCP 服务管理"] form');const set=(ph,v)=>{const t=f.querySelector('input[placeholder="'+ph+'"]');if(!t)throw Error('missing input '+ph);Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(t,v);t.dispatchEvent(new Event('input',{bubbles:true}))};set('例如 filesystem','mock-mcp');set('例如 npx 或 /usr/bin/mcp-server','mock-mcp');})()`,
  );
  await click("保存并连接");
  await until(
    `window.__product.mcpConfig.length===1 && document.body.innerText.includes('已保存并连接')`,
  );
  await click("返回工程");
  await click("管理助手");
  await evaluate(
    `Array.from(document.querySelectorAll('label.checkbox-label')).find(l=>l.textContent.includes('启用全部已接入的 MCP 服务')).querySelector('input').click()`,
  );
  await until(
    `document.querySelector('[aria-label="助手的技能与扩展"]').innerText.includes('mock-mcp')`,
  );
  await evaluate(
    `Array.from(document.querySelectorAll('label.checkbox-label')).find(l=>l.textContent.includes('启用全部已接入的 MCP 服务')).querySelector('input').click()`,
  );
  await click("⚙ 设置");
  await click("技能与服务");
  await click("移除");
  await until(`document.querySelector('dialog[open]')`);
  await click("移除服务");
  await until(`window.__product.mcpConfig.length===0`);
  await click("开发者设置");
  await evaluate(
    `(()=>{const d=Array.from(document.querySelectorAll('details')).find(x=>x.querySelector('summary')?.textContent.includes('宿主日志'));if(!d)throw Error('missing log section');d.open=true})()`,
  );
  await click("读取最近 200 行");
  await until(`document.body.innerText.includes('host started')`);
  console.log(
    "PASS: assistant editor picks installed MCP/Skills from the catalog, MCP managed in ordinary settings, developer settings and host logs.",
  );
  if (!(await evaluate(`document.documentElement.dataset.theme==='dark'`)))
    throw Error("dark must be the default DAW theme");
  await click("☀ 浅色模式");
  await until(`document.documentElement.dataset.theme==='light'`);
  await click("🌙 深色模式");
  await until(`document.documentElement.dataset.theme==='dark'`);
  if (
    !(await evaluate(
      `getComputedStyle(document.documentElement).backgroundColor==='rgb(16, 22, 28)'`,
    ))
  )
    throw Error("Dark palette not applied");
  await click("返回工程");
  await until(`document.querySelector('.chat-page')`);
  await new Promise((r) => setTimeout(r, 400));
  writeFileSync(
    resolve(dir, "theme-dark-workspace.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  await click("⚙ 设置");
  await until(`document.querySelector('.settings-page')`);
  await click("技能与服务");
  await until(`document.querySelector('[aria-label="MCP 服务管理"]')`);
  await new Promise((r) => setTimeout(r, 400));
  writeFileSync(
    resolve(dir, "theme-dark.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  await click("返回工程");
  await until(`document.querySelector('.chat-page')`);
  await click("管理助手");
  await click("☀ 浅色模式");
  await until(`document.documentElement.dataset.theme==='light'`);
  if (
    !(await evaluate(
      `getComputedStyle(document.documentElement).backgroundColor==='rgb(245, 248, 250)'`,
    ))
  )
    throw Error("Light palette not applied");
  await new Promise((r) => setTimeout(r, 400));
  writeFileSync(
    resolve(dir, "theme-light.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  await click("返回工程");
  await until(`document.querySelector('.chat-page')`);
  await new Promise((r) => setTimeout(r, 400));
  writeFileSync(
    resolve(dir, "theme-light-workspace.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  console.log("PASS: theme toggle switches palettes in place.");
  if (errors.length) throw Error(errors.join("\n"));
  console.log(
    "PASS: workspace-first home with audition entry status, project sidebar (create/import/select/delete) and editor-only center (rename transaction/undo/redo/export), right agent dock with conversation list/send-creates-conversation/composer new-chat entry/search/manage-assistants, model entry and project binding, file upload/preview, Markdown/table/code and HTML safety, IME/Enter, search recovery, explicit endpoint consent, folder permission, child approval aggregation, conversation rename/search/delete, drag/paste thumbnails, guided unknown outcome and cancellable denial, narrow layout and single chat scroll.",
  );
} finally {
  try {
    ws?.close();
  } catch {}
  for (const child of [vite, chrome]) {
    try {
      process.kill(-child.pid, "SIGTERM");
    } catch {}
  }
  await Promise.all([vite, chrome].map(child => child.exitCode !== null || child.signalCode !== null ? null : new Promise(resolve => {
    const timer = setTimeout(resolve, 2000);
    child.once("exit", () => { clearTimeout(timer); resolve(); });
  })));
  closeSync(log);
  if (artifactParent) console.log(`Browser artifacts: ${dir}`);
  else rmSync(dir, { recursive: true, force: true, maxRetries: 3, retryDelay: 50 });
}
