// 一次性 UX 走查脚本（不入库）：真实前端 + 增强产品 mock，按普通创作者旅程截图。
import { spawn } from "node:child_process";
import { mkdirSync, openSync, writeFileSync, readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const dir = resolve(root, ".cache/browser/ux");
const shots = "/tmp/ux-shots";
mkdirSync(dir, { recursive: true });
mkdirSync(shots, { recursive: true });
const log = openSync(resolve(dir, "services.log"), "a");
const vite = spawn("npm", ["run", "dev", "--", "--host", "127.0.0.1", "--port", "9395", "--strictPort"], { cwd: resolve(root, "frontend"), stdio: ["ignore", log, log], detached: true });
const chrome = spawn(process.env.ICETUDIO_E2E_BROWSER || "chromium", [
  "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking", "--no-first-run",
  `--user-data-dir=${resolve(dir, "chrome")}`, "--remote-debugging-port=9396", "about:blank",
], { stdio: ["ignore", log, log], detached: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function ready(url) {
  for (let i = 0; i < 100; i++) {
    try { await (await fetch(url)).json?.(); return true; } catch {}
    await sleep(100);
  }
  try { await fetch(url); return true; } catch { throw Error("unavailable " + url); }
}
await ready("http://127.0.0.1:9395");
let targets;
for (let i = 0; i < 100; i++) {
  try { targets = await (await fetch("http://127.0.0.1:9396/json/list")).json(); break; } catch {}
  await sleep(100);
}
if (!targets) throw Error("chromium CDP unavailable");
const ws = new WebSocket(targets.find((t) => t.type === "page").webSocketDebuggerUrl);
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
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

// ---- backend mock：取 product.mjs 的模板并打补丁（音轨/片段/音符/音频可用）----
const product = readFileSync(resolve(root, "frontend/e2e/product.mjs"), "utf8");
let backend = product.match(/const backend = `([\s\S]*?)`;\n/)[1];
backend = backend.replace(/export const PlaybackService=\{.*?\};/, `export const PlaybackService={Status:async()=>({available:true,ready:true,playing:false,projectId:'',revision:0,tick:0,endTick:0,loop:false,loopStart:0,loopEnd:0,sampleRate:48000,callbacks:12,missedBlocks:0,loops:0,skippedEvents:0,engine:'FluidSynth 2.6.1',settings:{version:2,driver:'pulseaudio',fonts:[],defaultFont:''},resolvedFont:'MuseScore_General.sf3',resolvedKind:'bundled',notice:'',error:''}),Stop:async()=>{},StopProject:async()=>{},Play:async()=>{},Seek:async()=>{},Configure:async()=>{},PickSoundFont:async()=>'',AddSoundFont:async()=>({id:'font_test',name:'local.sf2',path:'/test/local.sf2',priority:0,addedAt:''}),RemoveSoundFont:async()=>{},SetDefaultSoundFont:async()=>{},MoveSoundFont:async()=>{},ResetAudioSettings:async()=>{},Audition:async()=>{},StopAudition:async()=>{},SoundFonts:async()=>({bundled:{id:'bundled',name:'MuseScore_General.sf3',path:'/opt/icetudio/soundfonts/MuseScore_General.sf3',source:'bundled',exists:true,size:39900972,error:'',active:true,isDefault:true,priority:-1},basic:{id:'basic',name:'Basic Waves（内置基础波形）',path:'',source:'basic',exists:true,size:0,error:'',active:false,isDefault:false,priority:1073741824},fonts:[],defaultFont:'',notice:''})};`);
backend = backend.replace(/CreateProject:async\(title\)=>structuredClone\(newProject\(title\)\)/, `CreateProject:async(title)=>{const p=newProject(title);p.state.tracks=[{id:'t_1',name:'主旋律 · 钢琴',color:'ice',channel:0,program:0,muted:false,solo:false,clips:[{id:'c_1',name:'A 段',color:'',start:0,length:7680,notes:[{id:'n_1',start:0,duration:480,pitch:60,velocity:100,channel:0},{id:'n_2',start:480,duration:480,pitch:64,velocity:100,channel:0},{id:'n_3',start:960,duration:960,pitch:67,velocity:100,channel:0},{id:'n_4',start:1920,duration:480,pitch:72,velocity:100,channel:0},{id:'n_5',start:2400,duration:960,pitch:69,velocity:100,channel:0}],controls:[],raw:[]}]},{id:'t_2',name:'贝斯',color:'mint',channel:0,program:33,muted:false,solo:false,clips:[{id:'c_2',name:'B 段',color:'',start:0,length:7680,notes:[{id:'n_6',start:0,duration:960,pitch:36,velocity:100,channel:0},{id:'n_7',start:1440,duration:960,pitch:43,velocity:100,channel:0}],controls:[],raw:[]}]}];return structuredClone(p)}`);
backend = backend.replace(/SubmitProjectOps:async\([^)]*\)=>\{[\s\S]*?\},UndoProject/, `SubmitProjectOps:async(projectId,opId,source,baseRevision,ops)=>{const p=findProject(projectId);if(!p)throw Error('工程不存在');if(state.projectOps[opId])return structuredClone(state.projectOps[opId]);const stale=staleRevision(p,baseRevision);if(stale)return stale;const created=[];const summary={};for(const op of ops){const st=p.state;summary[op.kind]=1;if(op.kind==='project.rename')st.title=op.title;else if(op.kind==='track.add'){const t={id:op.track.id||('t'+(++state.n)),name:op.track.name||'音轨',color:op.track.color||'',channel:op.track.channel,program:op.track.program,muted:!!op.track.muted,solo:!!op.track.solo,clips:[]};st.tracks.push(t);created.push(t.id)}else if(op.kind==='track.update'){const t=st.tracks.find(t=>t.id===op.trackId);if(!t)throw Error('音轨不存在');Object.assign(t,op.trackPatch)}else if(op.kind==='clip.add'){const t=st.tracks.find(t=>t.id===op.clip.trackId);const c={id:op.clip.id||('c'+(++state.n)),name:op.clip.name||'片段',color:op.clip.color||'',start:op.clip.start,length:op.clip.length,notes:[],controls:[],raw:[]};t.clips.push(c);created.push(c.id)}else if(op.kind==='clip.update'){for(const tr of st.tracks){const c=tr.clips.find(c=>c.id===op.clipId);if(c){Object.assign(c,op.clipPatch);break}}}else if(op.kind==='note.add'){for(const tr of st.tracks){const c=tr.clips.find(c=>c.id===op.clipId);if(c){for(const n of op.notes)c.notes.push({id:'n'+(++state.n),start:n.start,duration:n.duration,pitch:n.pitch,velocity:n.velocity,channel:n.channel});break}}}else throw Error('模拟环境不支持操作 '+op.kind)}p.revision++;p.updatedAt='2026-10-24';state.projectHistory[projectId].push({opId:opId,kind:'edit',ops:structuredClone(ops),inverse:[],undone:false,revision:p.revision});const result={opId:opId,revision:p.revision,baseRevision:baseRevision,source:source,applied:ops.length,summary:summary,createdIds:created};state.projectOps[opId]=result;state.projectResults[opId]={found:true,result:result};return structuredClone(result)},UndoProject`);

ws.onmessage = (e) => {
  const m = JSON.parse(e.data);
  if (m.id) {
    const p = pending.get(m.id);
    if (p) { clearTimeout(p.timer); pending.delete(m.id); m.error ? p.reject(Error(JSON.stringify(m.error))) : p.resolve(m.result); }
  } else if (m.method === "Fetch.requestPaused") {
    send("Fetch.fulfillRequest", { requestId: m.params.requestId, responseCode: 200, responseHeaders: [{ name: "Content-Type", value: "application/javascript" }], body: Buffer.from(backend).toString("base64") }).catch((e2) => errors.push(e2.message));
  } else if (m.method === "Runtime.exceptionThrown") errors.push(m.params.exceptionDetails.exception?.description ?? m.params.exceptionDetails.text);
};
await send("Runtime.enable");
await send("Page.enable");
await send("Network.enable");
await send("Fetch.enable", { patterns: [{ urlPattern: "*/bindings/icetudio/index.*", requestStage: "Request" }] });

async function evaluate(expr) {
  const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text);
  return r.result?.value;
}
async function until(expr) {
  for (let i = 0; i < 100; i++) {
    if (await evaluate(`(()=>{try{return !!(${expr})}catch{return false}})()`)) return;
    await sleep(100);
  }
  throw Error("not reached: " + expr.slice(0, 120));
}
async function clickIn(scope, text) {
  const s = JSON.stringify(scope), t = JSON.stringify(text);
  await until(`(()=>{const c=document.querySelector(${s});return c&&Array.from(c.querySelectorAll('button')).some(e=>e.textContent.trim()===${t}&&!e.disabled)})()`);
  await evaluate(`(()=>{const c=document.querySelector(${s});const b=Array.from(c.querySelectorAll('button')).find(e=>e.textContent.trim()===${t});if(!b)throw Error('missing');b.click()})()`);
}
async function fill(selector, value) {
  const s = JSON.stringify(selector), v = JSON.stringify(value);
  await until(`document.querySelector(${s})`);
  await evaluate(`(()=>{const el=document.querySelector(${s});el.value=${v};el.dispatchEvent(new Event('input',{bubbles:true}))})()`);
}
async function shot(name, full = false) {
  const params = { format: "png" };
  if (full) params.captureBeyondViewport = true;
  const r = await send("Page.captureScreenshot", params);
  writeFileSync(resolve(shots, name + ".png"), Buffer.from(r.data, "base64"));
  console.log("SHOT", name);
}

try {
  await send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send("Page.navigate", { url: "http://127.0.0.1:9395" });
  await until(`document.querySelector('.workspace-page')`);
  await sleep(800);
  await shot("01-首页-空工程");
  await clickIn(".browser", "＋ 新建工程");
  await fill("dialog[open] input[type=text]", "夜曲小样");
  await clickIn("dialog[open]", "创建");
  await until(`document.body.innerText.includes('主旋律 · 钢琴')`);
  await sleep(900);
  await shot("02-新建后-编排视图");
  await shot("02b-整页", true);
  await evaluate(`(()=>{const s=document.querySelectorAll('.browser-section summary');s[1].click();return s.length})()`);
  await sleep(400);
  await shot("03-浏览器-音色分区");
  await clickIn(".transport-bar", "拆分");
  await sleep(500);
  await shot("04-拆分视图");
  await clickIn(".transport-bar", "琴键");
  await sleep(500);
  await shot("05-钢琴卷帘视图");
  await evaluate(`(()=>{const c=document.querySelector('.workspace-center');if(c)c.scrollTop=c.scrollHeight;return true})()`);
  await sleep(300);
  await shot("06-底部-检查器");
  await send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  await clickIn(".transport-bar", "编排");
  await sleep(500);
  await shot("07-1280-编排视图");
  await clickIn(".sidebar", "⚙ 设置");
  await until(`document.querySelector('.settings-page')`);
  await sleep(400);
  await shot("08-设置页");
  console.log("DONE. page errors:", errors.length ? errors.slice(0, 3) : "none");
} finally {
  for (const child of [vite, chrome]) { try { process.kill(-child.pid, "SIGTERM"); } catch {} }
}
