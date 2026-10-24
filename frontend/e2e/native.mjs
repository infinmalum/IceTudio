// Opt-in native Wails/WebKit acceptance; uses a production+mcp TEST build only.
// Run with --run after building the documented isolated binary. No model calls.
// The Wails automation library injects DOM input; native dialogs/window closing,
// service bindings, SQLite and FluidSynth are real. Physical listening is separate.
import {readFileSync,writeFileSync,mkdirSync,openSync,existsSync} from 'node:fs';
import {spawn,execFileSync} from 'node:child_process';
import {resolve} from 'node:path';
import {createHash} from 'node:crypto';
const port=Number(process.env.NATIVE_PORT||9230), endpoint=`http://127.0.0.1:${port}/mcp`;
let seq=0;
export async function tool(name,args={}){const r=await fetch(endpoint,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:++seq,method:'tools/call',params:{name,arguments:args}})});const b=await r.json();if(b.error||b.result?.isError)throw Error(JSON.stringify(b));const s=b.result.content.find(x=>x.type==='text')?.text;if(!s)return null;try{return JSON.parse(s)}catch{return s}}
export const ev=js=>tool('js_eval',{js,timeout_ms:60000});
export const sleep=ms=>new Promise(r=>setTimeout(r,ms));
export async function until(js){for(let i=0;i<150;i++){if(await ev('return Boolean('+js+');'))return;await sleep(100)}throw Error(js)}
export async function click(text,scope='body'){await until(`Array.from(document.querySelector(${JSON.stringify(scope)})?.querySelectorAll('button')??[]).some(b=>b.textContent.trim()===${JSON.stringify(text)}&&!b.disabled)`);await ev(`const b=Array.from(document.querySelector(${JSON.stringify(scope)}).querySelectorAll('button')).find(b=>b.textContent.trim()===${JSON.stringify(text)}&&!b.disabled);if(!b)throw Error('button unavailable '+${JSON.stringify(text)});b.click();return true;`);await sleep(80)}
export async function fill(selector,value){await ev(`const e=document.querySelector(${JSON.stringify(selector)});e.focus();Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}));return true;`);await sleep(60)}
export async function setup(){const ids={};for(const service of ['projectservice','playbackservice']){const source=readFileSync(`frontend/bindings/icetudio/${service}.ts`,'utf8');for(const m of source.matchAll(/export function (\w+)\([^]*?return \$Call.ByID\((\d+)/g))ids[service+'.'+m[1]]=Number(m[2])}await ev(`window.nativeCall=async(name,...args)=>{const dispatch=window._wails.dispatchWailsEvent;const {Call}=await import('/wails/runtime.js');window._wails.dispatchWailsEvent=dispatch;return Call.ByID((${JSON.stringify(ids)})[name],...args)};return true;`)}

if(process.argv.includes('--run')) {
 const dir=resolve('.cache/p2-closeout/native-final'),binary=resolve(dir,'icetudio-test');
 const report={checks:[],metrics:[],limits:['Test build adds Wails MCP automation; production build has no automation listener','DOM input is injected by Wails automation; no claim of manual listening or physical latency','GTK4 GtkFileDialog always routes through xdg-desktop-portal; on KDE the chooser is a Qt window evidenced via KWin scripting and cancelled with closeWindow. Typing a filename into the Wayland-native dialog is not automatable here; the MIDI round trip uses explicit service paths','SIGKILL is process failure, not system power loss',
   'Cosmetic UI state in localStorage (lastProject, view, theme) may go stale across the native quit because the WebKit storage flush is not guaranteed; music data and input drafts are durable in Go and unaffected','Audio callbacks run on the user PipeWire server routed to a temporary null sink (icetudio_p2_sink, unloaded after the run); the 2026-10-18 fully isolated secondary server could not be reproduced on the current system and no user-facing device is stopped'],environment:{node:process.version,binarySHA256:createHash('sha256').update(readFileSync(binary)).digest('hex')}};
 mkdirSync(dir,{recursive:true});const log=openSync(resolve(dir,'native.log'),'a');let child;
 async function launch(){child=spawn(binary,[],{env:{...process.env,GDK_BACKEND:'x11',GTK_USE_PORTAL:'0',WAILS_MCP_PORT:String(port),WAILS_MCP_HIDE_CURSOR:'1',PULSE_SINK:process.env.P2_TEST_SINK||'icetudio_p2_sink'},stdio:['ignore',log,log]});for(let i=0;i<150;i++){try{if((await fetch(`http://127.0.0.1:${port}/`)).ok)break}catch{}await sleep(100)}await sleep(5000);await until('document.querySelector("[data-close-guard=ready]")&&document.querySelector(".browser")');await setup();}
 async function stop(signal='SIGKILL'){const done=new Promise(r=>child.once('exit',r));child.kill(signal);await done;await sleep(150)}
 async function check(name,js){if(!await ev('return Boolean('+js+');'))throw Error(name);report.checks.push(name);writeFileSync(resolve(dir,'results.json'),JSON.stringify(report,null,2));}
 async function enter(selector,value){await fill(selector,value);await tool('keyboard_press',{key:'Enter'});await sleep(200)}
 async function choose(mode,path){return execFileSync('python3',['frontend/e2e/native-chooser.py',String(child.pid),mode,path],{encoding:'utf8',timeout:15000});}
 // GTK4 GtkFileDialog always routes through xdg-desktop-portal; on KDE the
 // chooser is a Qt window invisible to AT-SPI. KWin scripting gives evidence
 // (window list) and drives the cancel path (closeWindow = Alt+F4). Typing a
 // filename into a Wayland-native dialog cannot be automated here; the MIDI
 // round trip therefore uses the real services with explicit paths.
 function sh(cmd,args){return execFileSync(cmd,args,{encoding:'utf8',timeout:15000})}
 async function kwinRun(jsBody){
  writeFileSync('/tmp/kwin-scripts/p2run.js',`workspace.windowList().forEach(function(w){ ${jsBody} });\n`);
  try{sh('qdbus6',['org.kde.KWin','/Scripting','org.kde.kwin.Scripting.unloadScript','p2run'])}catch{}
  sh('qdbus6',['org.kde.KWin','/Scripting','org.kde.kwin.Scripting.loadScript','/tmp/kwin-scripts/p2run.js','p2run']);
  sh('qdbus6',['org.kde.KWin','/Scripting','org.kde.kwin.Scripting.start']);
  await sleep(300);
  try{sh('qdbus6',['org.kde.KWin','/Scripting','org.kde.kwin.Scripting.unloadScript','p2run'])}catch{}
 }
 let kwinNonce=0;
 async function kwinWindows(){
  const mark='P2W'+(++kwinNonce);
  await kwinRun(`if(w.resourceClass)print('${mark}|'+w.resourceClass+'|'+w.caption)`);
  const log=sh('journalctl',['--user','-b','--no-pager','--output=cat','-n','300']);
  return log.split('\n').filter(l=>l.includes(mark+'|')).map(l=>{const p=l.split('|');return {cls:p[1]||'',caption:p.slice(2).join('|')}}).
   filter((r,i,a)=>a.findIndex(x=>x.cls===r.cls&&x.caption===r.caption)===i);
 }
 async function waitPortalDialog(){for(let i=0;i<20;i++){const rows=await kwinWindows();const hit=rows.find(r=>r.cls==='org.freedesktop.impl.portal.desktop.kde');if(hit)return hit;await sleep(300)}throw Error('portal file dialog window not found')}
 async function closePortalDialog(){await kwinRun("if(w.resourceClass==='org.freedesktop.impl.portal.desktop.kde')w.closeWindow()")}
 try {
  await launch();
  report.environment.webview=await ev('return {userAgent:navigator.userAgent,viewport:[innerWidth,innerHeight],devicePixelRatio};');
  report.metrics.push({defaultViewport:report.environment.webview.viewport});
  await check('Native viewport keeps a usable browser column and a start hero or restored project',`document.querySelector('.browser').getBoundingClientRect().width>=180&&((document.querySelector('.hero-actions')?.getBoundingClientRect().height??0)>=40||document.querySelector('.music-editor')!==null)`);
  const title='原生完整流程-'+Date.now();await click('＋ 新建工程','.browser');await fill('dialog[open] input[type=text]',title);await click('创建','dialog[open]');await until(`document.querySelector('.transport-title')?.textContent===${JSON.stringify(title)}&&document.querySelector('.music-editor')`);await check('Open project keeps a usable editor area at the native viewport',`document.querySelector('.editor-center').getBoundingClientRect().height>150&&document.querySelector('.inspector-dock')!==null`);await click('拆分','.transport-bar');await until('document.querySelector(".editor-center[data-view=split]")');await click('添加音轨');await until('document.querySelector("gsui-pianoroll .gsui-row")');
  await ev(`window.projectID=localStorage.getItem('icetudio.lastProject');window.getNative=()=>nativeCall('projectservice.GetProject',projectID);return true;`);
  await ev(`document.querySelector('.piano-host').scrollIntoView({block:'center'});return true;`);await sleep(300);
  const point=await ev(`const row=document.querySelector('.gsui-row[data-midi="60"]'),area=document.querySelector('.gsuiTimewindow-scrollArea');area.scrollTop+=row.getBoundingClientRect().top-area.getBoundingClientRect().top-area.clientHeight/2;const r=row.getBoundingClientRect(),o=document.querySelector('.gsuiTimewindow-mainContent').getBoundingClientRect();return {x:o.left+64,y:r.top+10};`);
  await tool('mouse_click',point);await until('(await getNative()).state.tracks[0].clips[0].notes?.length===1');
  await check('Native WebKit pointer input creates a committed MIDI note',`(await getNative()).state.tracks[0].clips[0].notes[0].pitch===60`);
  await click('全选本页');await fill('[aria-label="音符力度"]','88');await click('应用力度');await until('(await getNative()).state.tracks[0].clips[0].notes[0].velocity===88');
  await click('添加音轨');await until('(await getNative()).state.tracks.length===2');
  await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','93');await click('应用时间图','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")');await until('(await getNative()).state.tempo[0].usPerQuarter===645161');
  await click('撤销','.transport-bar');await until('(await getNative()).state.tempo[0].usPerQuarter===500000');await click('重做','.transport-bar');await until('(await getNative()).state.tempo[0].usPerQuarter===645161');
  await check('Native multi-track/time-map editing shares undo and redo',`(await getNative()).state.tracks.length===2`);
  await click('播放','.transport');await until('(await nativeCall("playbackservice.Status")).callbacks>4&&(await nativeCall("playbackservice.Status")).playing');
  const before=await ev('return await nativeCall("playbackservice.Status");');await click('停止','.transport');
  await check('Native transport reaches real FluidSynth/Pulse callbacks and stops',`!(await nativeCall('playbackservice.Status')).playing`);report.metrics.push({audio:before});
  const cb0=await ev('return (await nativeCall("playbackservice.Status")).callbacks;'),rev0=await ev('return (await getNative()).revision;');
  await click('试听','.track-inspector');await sleep(800);
  await check('Native audition renders off-timeline without changing the project',`(await nativeCall('playbackservice.Status')).callbacks>${cb0}&&(await getNative()).revision===${rev0}`);
  const output=resolve(dir,'native-export.mid');
  await click('导出 MIDI','.transport-bar');
  const saveDialog=await waitPortalDialog();
  report.metrics.push({nativeSaveDialog:{cls:saveDialog.cls,caption:saveDialog.caption}});
  await closePortalDialog();
  await until(`!Array.from(document.querySelectorAll('.transport-bar button')).find(b=>b.textContent==='导出 MIDI').disabled`);
  await check('Native export opens the real system save dialog and cancel unblocks it',`document.querySelector('.transport-bar')!==null`);
  await ev(`await nativeCall('projectservice.ExportProjectMIDI',projectID,${JSON.stringify(output)});return true;`);
  if(!existsSync(output))throw Error('native export did not create MIDI');
  const imported=await ev(`return await nativeCall('projectservice.ImportProjectMIDI',${JSON.stringify(output)});`);
  await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.dataset.id===${JSON.stringify(imported.id)})`);
  await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(imported.id)}).click();return true;`);
  await until(`localStorage.getItem('icetudio.lastProject')===${JSON.stringify(imported.id)}&&document.querySelector('gsui-pianoroll')`);
  await check('Native binary exports and re-imports the MIDI round trip',`(await nativeCall('projectservice.GetProject',${JSON.stringify(imported.id)})).state.tracks.some(t=>t.clips?.some(c=>c.notes?.some(n=>n.pitch===60&&n.velocity===88)))`);
  await click('导入 MIDI','.browser');
  const openDialog=await waitPortalDialog();
  report.metrics.push({nativeOpenDialog:{cls:openDialog.cls,caption:openDialog.caption}});
  await closePortalDialog();
  await until(`!Array.from(document.querySelectorAll('.browser button')).find(b=>b.textContent==='导入 MIDI').disabled`);
  await check('Native import opens the real system open dialog and cancel skips import',`localStorage.getItem('icetudio.lastProject')===${JSON.stringify(imported.id)}`);
  const originalID=await ev('return projectID;');await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(originalID)}).click();return true;`);await until(`localStorage.getItem('icetudio.lastProject')===${JSON.stringify(originalID)}&&document.querySelector('gsui-pianoroll')`);
  await fill('[aria-label="片段长度（tick）"]','');await tool('window_control',{action:'close'});await until('document.querySelector("#dialog-title")?.textContent==="保留音乐编辑草稿并退出？"');await click('继续编辑','dialog[open]');
  await check('Actual Wails window close is cancelled and preserves blank music input',`document.querySelector('[aria-label="片段长度（tick）"]').value===''&&!document.querySelector('[data-close-guard]').hasAttribute('inert')`);
  await tool('window_control',{action:'close'});await until('document.querySelector("#dialog-title")?.textContent==="保留音乐编辑草稿并退出？"');const exit=new Promise(r=>child.once('exit',r));await click('保留草稿并退出','dialog[open]').catch(()=>{});await exit;await launch();
  // icetudio.lastProject is UI state in localStorage and can go stale across
  // the native quit (WebKit flush); the preserved draft itself is durable in
  // the Go data dir. Select the draft's project like a user would, then the
  // draft must be offered with its field locked.
  await ev(`const row=Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(originalID)});if(row&&localStorage.getItem('icetudio.lastProject')!==row.dataset.id)row.click();return true`);
  await until(`document.querySelector('[aria-label="已保留音乐输入"]')&&document.querySelector('[aria-label="片段长度（tick）"]')?.disabled===true`);
  await check('Actual close/reopen preserves music and offers input without applying it',`document.querySelector('[aria-label="已保留音乐输入"]')!==null&&document.querySelector('[aria-label="片段长度（tick）"]').disabled===true`);
  await click('恢复输入','[aria-label="已保留音乐输入"]');await check('Explicit native input recovery restores the original blank value',`document.querySelector('[aria-label="片段长度（tick）"]').value===''`);
  await click('丢弃输入','[aria-label="已保留音乐输入"]');await click('丢弃输入','dialog[open]');
  // Interrupt after the actual service commit but before the UI acknowledges it.
  const interrupted=await ev(`const id=localStorage.getItem('icetudio.lastProject'),p=await nativeCall('projectservice.GetProject',id),op='native-interrupted';await nativeCall('projectservice.BeginProjectEdit',id,op,p.revision,'edit');const result=await nativeCall('projectservice.SubmitProjectOps',id,op,'gui',p.revision,[{kind:'project.rename',title:'进程中断后的音乐'}]);return {id,revision:result.revision};`);
  await stop();await launch();await until('document.body.textContent.includes("查询保存结果")');await click('查询保存结果');await until('!document.body.textContent.includes("查询保存结果")');
  await check('SIGKILL/restart queries the original committed operation without replay',`(await nativeCall('projectservice.GetProject',${JSON.stringify(interrupted.id)})).revision===${interrupted.revision}&&(await nativeCall('projectservice.GetPendingProjectEdit'))===null`);
  console.log(JSON.stringify(report,null,2));
 }catch(e){report.failure=String(e);try{report.body=await ev('return document.body.innerText;')}catch{}console.error(JSON.stringify(report,null,2));process.exitCode=1}
 finally {writeFileSync(resolve(dir,'results.json'),JSON.stringify(report,null,2));if(child?.exitCode===null&&child?.signalCode===null)await stop();}
}
if(process.argv.includes('--debug')) {
 const binary=resolve('.cache/p2-closeout/native-final/icetudio-test');
 const debug=spawn('gdb',['-batch','-ex','set pagination off','-ex','handle SIGUSR1 nostop noprint pass','-ex','handle SIGURG nostop noprint pass','-ex','run','-ex','bt 25','--args',binary],{env:{...process.env,GDK_BACKEND:'x11',WAILS_MCP_PORT:String(port)},stdio:'inherit'});
 await new Promise(r=>debug.once('exit',r));
}
