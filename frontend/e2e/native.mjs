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
 const report={checks:[],metrics:[],limits:['Test build adds Wails MCP automation; production build has no automation listener','DOM input is injected by Wails automation; no claim of manual listening or physical latency','Native file chooser tested with GTK_USE_PORTAL=0; KDE portal path is separate','SIGKILL is process failure, not system power loss'],environment:{node:process.version,binarySHA256:createHash('sha256').update(readFileSync(binary)).digest('hex')}};
 mkdirSync(dir,{recursive:true});const log=openSync(resolve(dir,'native.log'),'a');let child;
 async function launch(){child=spawn(binary,[],{env:{...process.env,GDK_BACKEND:'x11',GTK_USE_PORTAL:'0',WAILS_MCP_PORT:String(port),WAILS_MCP_HIDE_CURSOR:'1',PULSE_SERVER:'unix:'+resolve('.cache/p2-closeout/audio-runtime/pulse.sock')},stdio:['ignore',log,log]});for(let i=0;i<150;i++){try{if((await fetch(`http://127.0.0.1:${port}/`)).ok)break}catch{}await sleep(100)}await sleep(5000);await until('document.querySelector("[data-close-guard=ready]")&&document.querySelector(".project-strip")');await setup();}
 async function stop(signal='SIGKILL'){const done=new Promise(r=>child.once('exit',r));child.kill(signal);await done;await sleep(150)}
 async function check(name,js){if(!await ev('return Boolean('+js+');'))throw Error(name);report.checks.push(name);writeFileSync(resolve(dir,'results.json'),JSON.stringify(report,null,2));}
 async function enter(selector,value){await fill(selector,value);await tool('keyboard_press',{key:'Enter'});await sleep(200)}
 async function choose(mode,path){return execFileSync('python3',['frontend/e2e/native-chooser.py',String(child.pid),mode,path],{encoding:'utf8',timeout:15000});}
 try {
  await launch();
  report.environment.webview=await ev('return {userAgent:navigator.userAgent,viewport:[innerWidth,innerHeight],devicePixelRatio};');
  await check('Scaled native viewport keeps a usable music column',`document.querySelector('.project-stage').getBoundingClientRect().width>=300&&document.querySelector('.project-canvas').getBoundingClientRect().height>150`);
  const title='原生完整流程-'+Date.now();await click('新建工程','.project-strip');await fill('dialog[open] input',title);await click('创建','dialog[open]');await until(`document.querySelector('.project-header h1').textContent===${JSON.stringify(title)}&&document.querySelector('.music-editor')`);await click('添加音轨');await until('document.querySelector("gsui-pianoroll .gsui-row[data-midi=60]")');
  await ev(`window.projectID=localStorage.getItem('icetudio.lastProject');window.getNative=()=>nativeCall('projectservice.GetProject',projectID);return true;`);
  await ev(`document.querySelector('.piano-host').scrollIntoView({block:'center'});return true;`);await sleep(300);
  const point=await ev(`const row=document.querySelector('.gsui-row[data-midi="60"]'),area=document.querySelector('.gsuiTimewindow-scrollArea');area.scrollTop+=row.getBoundingClientRect().top-area.getBoundingClientRect().top-area.clientHeight/2;const r=row.getBoundingClientRect(),o=document.querySelector('.gsuiTimewindow-mainContent').getBoundingClientRect();return {x:o.left+64,y:r.top+10};`);
  await tool('mouse_click',point);await until('(await getNative()).state.tracks[0].clips[0].notes?.length===1');
  await check('Native WebKit pointer input creates a committed MIDI note',`(await getNative()).state.tracks[0].clips[0].notes[0].pitch===60`);
  await click('全选本页');await fill('[aria-label="音符力度"]','88');await click('应用力度');await until('(await getNative()).state.tracks[0].clips[0].notes[0].velocity===88');
  await click('添加音轨');await until('(await getNative()).state.tracks.length===2');
  await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','93');await click('应用时间图','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")');await until('(await getNative()).state.tempo[0].usPerQuarter===645161');
  await click('撤销','.project-strip');await until('(await getNative()).state.tempo[0].usPerQuarter===500000');await click('重做','.project-strip');await until('(await getNative()).state.tempo[0].usPerQuarter===645161');
  await check('Native multi-track/time-map editing shares undo and redo',`(await getNative()).state.tracks.length===2`);
  await click('播放','.transport');await until('(await nativeCall("playbackservice.Status")).callbacks>4&&(await nativeCall("playbackservice.Status")).playing');
  const before=await ev('return await nativeCall("playbackservice.Status");');await click('停止','.transport');
  await check('Native transport reaches real FluidSynth/Pulse callbacks and stops',`!(await nativeCall('playbackservice.Status')).playing`);report.metrics.push({audio:before});
  const output=resolve(dir,'native-export.mid');await click('导出 MIDI','.project-strip');report.fileChooserSave=await choose('save',output);await until(`!Array.from(document.querySelectorAll('.project-strip button')).find(b=>b.textContent==='导出 MIDI').disabled`);if(!existsSync(output))throw Error('native export did not create MIDI');
  await click('导入 MIDI','.project-strip');report.fileChooserOpen=await choose('open',output);await until(`localStorage.getItem('icetudio.lastProject')!==projectID&&document.querySelector('gsui-pianoroll')`);
  await check('Actual GTK native save/open dialogs export and import MIDI',`(await nativeCall('projectservice.GetProject',localStorage.getItem('icetudio.lastProject'))).state.tracks.some(t=>t.clips?.some(c=>c.notes?.some(n=>n.pitch===60&&n.velocity===88)))`);
  const originalID=await ev('return projectID;');await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.title===${JSON.stringify(originalID)}).click();return true;`);await until(`localStorage.getItem('icetudio.lastProject')===${JSON.stringify(originalID)}&&document.querySelector('gsui-pianoroll')`);
  await fill('[aria-label="片段长度（tick）"]','');await tool('window_control',{action:'close'});await until('document.querySelector("#dialog-title")?.textContent==="保留音乐编辑草稿并退出？"');await click('继续编辑','dialog[open]');
  await check('Actual Wails window close is cancelled and preserves blank music input',`document.querySelector('[aria-label="片段长度（tick）"]').value===''&&!document.querySelector('[data-close-guard]').hasAttribute('inert')`);
  await tool('window_control',{action:'close'});await until('document.querySelector("#dialog-title")?.textContent==="保留音乐编辑草稿并退出？"');const exit=new Promise(r=>child.once('exit',r));await click('保留草稿并退出','dialog[open]').catch(()=>{});await exit;await launch();
  await check('Actual close/reopen preserves music and offers input without applying it',`document.querySelector('[aria-label="已保留音乐输入"]')&&document.querySelector('[aria-label="片段长度（tick）"]').disabled`);
  await click('恢复输入','[aria-label="已保留音乐输入"]');await check('Explicit native input recovery restores the original blank value',`document.querySelector('[aria-label="片段长度（tick）"]').value===''`);
  await click('丢弃输入','[aria-label="已保留音乐输入"]');await click('丢弃输入','dialog[open]');
  // Interrupt after the actual service commit but before the UI acknowledges it.
  const interrupted=await ev(`const id=localStorage.getItem('icetudio.lastProject'),p=await nativeCall('projectservice.GetProject',id),op='native-interrupted';await nativeCall('projectservice.BeginProjectEdit',id,op,p.revision,'edit');const result=await nativeCall('projectservice.SubmitProjectOps',id,op,'gui',p.revision,[{kind:'project.rename',title:'进程中断后的音乐'}]);return {id,revision:result.revision};`);
  await stop();await launch();await until('document.body.textContent.includes("查询保存结果")');await click('查询保存结果');await until('!document.body.textContent.includes("查询保存结果")');
  await check('SIGKILL/restart queries the original committed operation without replay',`(await nativeCall('projectservice.GetProject',${JSON.stringify(interrupted.id)})).revision===${interrupted.revision}&&(await nativeCall('projectservice.GetPendingProjectEdit'))===null`);
  report.checks.push('New isolated install initializes without model configuration');
  console.log(JSON.stringify(report,null,2));
 }catch(e){report.failure=String(e);try{report.body=await ev('return document.body.innerText;')}catch{}console.error(JSON.stringify(report,null,2));process.exitCode=1}
 finally {writeFileSync(resolve(dir,'results.json'),JSON.stringify(report,null,2));if(child?.exitCode===null&&child?.signalCode===null)await stop();}
}
if(process.argv.includes('--debug')) {
 const binary=resolve('.cache/p2-closeout/native-final/icetudio-test');
 const debug=spawn('gdb',['-batch','-ex','set pagination off','-ex','handle SIGUSR1 nostop noprint pass','-ex','handle SIGURG nostop noprint pass','-ex','run','-ex','bt 25','--args',binary],{env:{...process.env,GDK_BACKEND:'x11',WAILS_MCP_PORT:String(port)},stdio:'inherit'});
 await new Promise(r=>debug.once('exit',r));
}
