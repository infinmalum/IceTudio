// Run through: ICETUDIO_MUSIC_BROWSER=1 go test ./internal/app -run TestMusicEditorBrowser -v
import { spawn } from 'node:child_process';
import { mkdirSync, openSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cpus } from 'node:os';
const root=process.cwd(), dir=process.env.ICETUDIO_TEST_RUN;
if (!dir || !process.env.ICETUDIO_TEST_URL || !process.env.ICETUDIO_TEST_TOKEN) throw Error('Run via the opt-in Go browser test');
mkdirSync(dir,{recursive:true});const log=openSync(resolve(dir,'services.log'),'a');
const vite=spawn('npm',['run','dev','--','--port','9393','--strictPort'],{cwd:resolve(root,'frontend'),stdio:['ignore',log,log],detached:true});
const chrome=spawn(process.env.ICETUDIO_E2E_BROWSER||'chromium',['--headless','--no-sandbox','--disable-gpu','--disable-background-networking','--no-first-run',`--user-data-dir=${resolve(dir,'chrome')}`,'--remote-debugging-port=9394','about:blank'],{stdio:['ignore',log,log],detached:true});
const sleep=ms=>new Promise(r=>setTimeout(r,ms));let ws;let debug;
const report={checks:[],errors:[],timings:[],environment:{cpu:cpus()[0]?.model,viewport:[1440,1000]}};
const bridge=`
const endpoint=${JSON.stringify(process.env.ICETUDIO_TEST_URL)}, token=${JSON.stringify(process.env.ICETUDIO_TEST_TOKEN)};
const call=async(method,...args)=>{const r=await fetch(endpoint+'/call',{method:'POST',headers:{'Content-Type':'application/json','X-Test-Token':token},body:JSON.stringify({method,args})});if(!r.ok)throw new window.__musicRuntimeError(await r.text());return r.json()};
window.musicCall=call;
const closeState=window.musicClose={readyCalls:0,cancelled:[],confirmed:[]};
export const DesktopService={Ready:async()=>{closeState.readyCalls++;if(window.musicReadyFails){window.musicReadyFails--;throw Error('test close initialization failed')}},CancelClose:async id=>{closeState.cancelled.push(id)},ConfirmClose:async id=>{if(window.musicCloseFailure)throw Error('test close reply failed');closeState.confirmed.push(id)}};
// Audio controls are mocked here; PCM/driver behavior is tested in Go.
const audio=window.musicTransport={available:true,ready:false,playing:false,projectId:'',revision:0,tick:0,endTick:960,loop:false,loopStart:0,loopEnd:960,sampleRate:48000,callbacks:0,missedBlocks:0,loops:0,skippedEvents:0,engine:'UI transport mock',settings:{version:2,driver:'pulseaudio',fonts:[],defaultFont:''},resolvedFont:'Basic Waves（内置基础波形）',resolvedKind:'basic',notice:'',error:''};
export const PlaybackService={Status:async()=>structuredClone(audio),Play:async req=>{if(window.musicAudioFailure)throw Error('test device unavailable');const p=await call('GetProject',req.projectId);audio.ready=true;audio.playing=true;audio.projectId=req.projectId;audio.revision=p.revision;audio.tick=req.tick;audio.loop=req.loop;audio.loopStart=req.loopStart;audio.loopEnd=req.loopEnd;audio.endTick=Math.max(1920,...p.state.tracks.flatMap(t=>t.clips.map(c=>c.start+c.length)))},Stop:async()=>{audio.playing=false},StopProject:async id=>{if(audio.projectId===id)audio.playing=false},Seek:async req=>{(window.musicSeekRequests??=[]).push(req);audio.tick=req.tick;audio.loop=req.loop;audio.loopStart=req.loopStart;audio.loopEnd=req.loopEnd},Configure:async settings=>{audio.settings=settings;audio.ready=true;audio.playing=false},PickSoundFont:async()=>window.musicPickedFont??'',AddSoundFont:async path=>({id:'font_test',name:path.split('/').pop(),path,priority:0,addedAt:''}),RemoveSoundFont:async()=>{},SetDefaultSoundFont:async()=>{},MoveSoundFont:async()=>{},ResetAudioSettings:async()=>{},Audition:async()=>{},StopAudition:async()=>{},SoundFonts:async()=>({bundled:{id:'bundled',name:'MuseScore_General.sf3',path:'',source:'bundled',exists:false,size:0,error:'',active:false,isDefault:true,priority:-1},basic:{id:'basic',name:'Basic Waves（内置基础波形）',path:'',source:'basic',exists:true,size:0,error:'',active:true,isDefault:false,priority:1073741824},fonts:[],defaultFont:'',notice:''})};
export const ProjectService=new Proxy({}, {get:(_,method)=>async(...args)=>{
 if(method==='PlayProjectEditPreview'){const p=await call('PreviewProjectEdit',args[0]);if(window.musicPreviewPlayHold)await new Promise(resolve=>{window.musicPreviewPlayRelease=resolve});audio.previewId=args[0];audio.previewVariant=args[1];audio.playing=true;audio.projectId=p.projectId;audio.revision=p.baseRevision;window.musicPreviewPlayCompleted=(window.musicPreviewPlayCompleted??0)+1;return null}
 if(method==='StopProjectEditPreview'){if(audio.previewId===args[0]){audio.previewId='';audio.previewVariant='';audio.playing=false}return null}
 if(method==='GetTimeMapDraft'&&window.musicDraftReadFails){window.musicDraftReadFails--;throw new window.__musicRuntimeError('test draft read failed')}
 const inputsRecord=()=>{const raw=localStorage.getItem('test.musicInputsRecord');return raw?JSON.parse(raw):null};
 if(method==='GetMusicInputs'){if(window.musicInputsReadFails){window.musicInputsReadFails--;throw new window.__musicRuntimeError('test music inputs read failed')}return inputsRecord()}
 if(method==='SaveMusicInputs'){if(window.musicInputsSaveFails)throw new window.__musicRuntimeError('test music inputs save failed');const r=inputsRecord();if(r&&r.token!==args[0])throw new window.__musicRuntimeError('music inputs token mismatch');const shape=JSON.parse(args[2]);const req=['key','label','title','target','raw'],opt=['trackID','clipID','noteIDs'];const bad=shape.entries.find(e=>{const ks=Object.keys(e);const target=Object.keys(e.target??{});return shape.version!==1||!Array.isArray(shape.entries)||ks.some(k=>![...req,...opt].includes(k))||req.some(k=>!(k in e))||target.length!==2||!target.includes('id')||!target.includes('revision')});if(bad)throw new window.__musicRuntimeError('music inputs entry shape rejected: '+JSON.stringify(Object.keys(bad)));localStorage.setItem('test.musicInputsRecord',JSON.stringify({version:1,token:args[1],payload:args[2]}));return null}
 if(method==='ClearMusicInputs'){const r=inputsRecord();if(r&&r.token!==args[0])throw new window.__musicRuntimeError('music inputs clear token mismatch');localStorage.removeItem('test.musicInputsRecord');return null}
 if(method==='SaveTimeMapDraft'&&window.musicDraftSaveFails)throw new window.__musicRuntimeError('test draft save failed');
 if(method==='ClearTimeMapDraft'&&window.musicDraftClearFails)throw new window.__musicRuntimeError('test draft clear failed');
 if(method==='SaveTimeMapDraft'&&window.musicDraftHold)await new Promise(resolve=>{window.musicDraftRelease=resolve});
 if(method==='PickSaveMIDIFile'){window.musicExportName=args[0];return ''} // Inspect the suggested filename, cancel the mocked picker.
 if(method==='GetPendingProjectEdit'&&window.musicRecoveryReadFails){window.musicRecoveryReadFails--;throw new window.__musicRuntimeError('test recovery read failed')}
 if(method==='BeginProjectEdit'&&window.musicRecoveryBeginFails)throw new window.__musicRuntimeError('test recovery write failed');
 if(method==='ClearPendingProjectEdit'&&window.musicRecoveryClearFails)throw new TypeError('test recovery clear failed');
 if(method==='GetProjectTransaction'&&window.musicLookupFails){window.musicLookupFails--;throw Error('test lookup interrupted')}
 const result=await call(method,...(['UndoProject','RedoProject'].includes(method)?[args[0],args[1],'gui',args[2]]:args));
 if(method==='PreviewProjectEdit'&&window.musicPreviewHold)await new Promise(resolve=>{window.musicPreviewRelease=resolve});
 if(method==='SaveTimeMapDraft'&&window.musicDraftLoseReply){window.musicDraftLoseReply=false;throw new TypeError('test draft reply lost')}
 if(method==='BeginProjectEdit'&&window.musicRecoveryBeginLoseReply){window.musicRecoveryBeginLoseReply=false;throw new TypeError('test Begin reply lost after persist')}
 if(method==='SubmitProjectOps'&&window.musicHoldSave)await new Promise(resolve=>{window.musicReleaseSave=resolve});
 if(method==='GetProject'&&window.musicDelayID===args[0])await new Promise(r=>setTimeout(r,350));
 if(method==='SubmitProjectOps'&&window.musicLoseReply){window.musicLoseReply=false;throw new TypeError('test reply interrupted after commit')}
 return result;
}});
`;
const runtime=`
const source=new EventSource(${JSON.stringify(process.env.ICETUDIO_TEST_URL+'/events?token='+process.env.ICETUDIO_TEST_TOKEN)});
const callbacks=new Map();const emit=(name,data)=>{for(const [cb,eventName] of callbacks)if(name===eventName)cb({data})};source.onmessage=e=>emit('icetudio:service-event',JSON.parse(e.data));
window.musicCloseRequest=id=>emit('icetudio:close-requested',id);
class RuntimeError extends Error{};window.__musicRuntimeError=RuntimeError;export const Call={RuntimeError};
export const Events={On:(name,cb)=>{callbacks.set(cb,name);return()=>callbacks.delete(cb)}};
`;
try{
 const ready=async url=>{for(let i=0;i<200;i++){try{const r=await fetch(url);if(r.ok)return r}catch{}await sleep(100)}throw Error('unavailable '+url)};
 await ready('http://127.0.0.1:9393');const targets=await(await ready('http://127.0.0.1:9394/json/list')).json();
 ws=new WebSocket(targets.find(t=>t.type==='page').webSocketDebuggerUrl);await new Promise((r,j)=>{ws.onopen=r;ws.onerror=j});
 let seq=0;const pending=new Map();
 const send=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>reject(Error('timeout '+method)),20000);pending.set(id,{resolve,reject,timer});ws.send(JSON.stringify({id,method,params}))});
 ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id){const q=pending.get(m.id);if(q){pending.delete(m.id);clearTimeout(q.timer);m.error?q.reject(Error(JSON.stringify(m.error))):q.resolve(m.result)}}else if(m.method==='Fetch.requestPaused'){const body=m.params.request.url.includes('/bindings/')?bridge:runtime;send('Fetch.fulfillRequest',{requestId:m.params.requestId,responseCode:200,responseHeaders:[{name:'Content-Type',value:'text/javascript'}],body:Buffer.from(body).toString('base64')}).catch(e=>report.errors.push(String(e)))}else if(m.method==='Runtime.exceptionThrown')report.errors.push(m.params.exceptionDetails.exception?.description||m.params.exceptionDetails.text);else if(m.method==='Runtime.consoleAPICalled'&&m.params.args.some(a=>String(a.value??a.description??'').includes('Maximum update depth exceeded')))report.errors.push('React exceeded the maximum update depth')};
 const ev=async expression=>{const v=await send('Runtime.evaluate',{expression:`(async()=>(${expression}))()`,returnByValue:true,awaitPromise:true});if(v.exceptionDetails)throw Error(v.exceptionDetails.exception?.description||v.exceptionDetails.text);return v.result.value};
 debug=async()=>{const shot=await send('Page.captureScreenshot');writeFileSync(resolve(dir,'debug.png'),Buffer.from(shot.data,'base64'));return ev('document.body.innerText')};
 const until=async expression=>{for(let i=0;i<120;i++){try{if(await ev(`Boolean(${expression})`))return}catch(e){if(!/Inspected target navigated|Cannot find context|Execution context was destroyed/.test(String(e)))throw e}await sleep(100)}throw Error('not reached '+expression)};
 const check=async(name,expression)=>{if(!await ev(expression))throw Error(name);report.checks.push(name)};
 const click=async(text,scope='body')=>{const expr=`Array.from(document.querySelector(${JSON.stringify(scope)}).querySelectorAll('button')).find(b=>b.textContent.trim()===${JSON.stringify(text)}&&!b.disabled)`;await until(expr);await ev(`(${expr}).click()`);await sleep(80)};
 const fill=async(selector,value)=>ev(`(async()=>{const e=document.querySelector(${JSON.stringify(selector)});e.focus();Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}));await new Promise(requestAnimationFrame)})()`);
 const rect=async selector=>ev(`(async()=>{const e=document.querySelector(${JSON.stringify(selector)});e.scrollIntoView({block:'center',inline:'center',behavior:'instant'});await new Promise(r=>setTimeout(r,120));const r=e.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2,w:r.width,h:r.height}})()`);
 const mouse=async(type,x,y,extra={})=>send('Input.dispatchMouseEvent',{type,x,y,button:'left',...extra});
 const drag=async(selector,dx,dy=0,during)=>{const r=await rect(selector);await mouse('mouseMoved',r.x,r.y);await mouse('mousePressed',r.x,r.y,{clickCount:1});for(let i=1;i<=5;i++)await mouse('mouseMoved',r.x+dx*i/5,r.y+dy*i/5,{buttons:1});if(during)await during();await mouse('mouseReleased',r.x+dx,r.y+dy,{clickCount:1});await sleep(200)};
 const keyboard=async(key,code,modifiers=0)=>{await send('Input.dispatchKeyEvent',{type:'keyDown',key,code,modifiers});await send('Input.dispatchKeyEvent',{type:'keyUp',key,code,modifiers})};
 await send('Runtime.enable');await send('Page.enable');await send('Fetch.enable',{patterns:[{urlPattern:'*/bindings/icetudio/index.*'},{urlPattern:'*/node_modules/.vite/deps/@wailsio_runtime.js*'}]});
 await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 report.environment.browser=(await send('Browser.getVersion')).product;
 const closeInit=await send('Page.addScriptToEvaluateOnNewDocument',{source:'window.musicReadyFails=2;window.musicRecoveryReadFails=2'});
 await send('Page.navigate',{url:'http://127.0.0.1:9393/e2e/music.html'});
 await until('window.musicCall && document.querySelector(".browser")');
 await until('document.querySelector(".close-guard-status[role=alert]")');
 await check('Close initialization failure keeps the editor inert until explicit retry',`document.querySelector('[data-close-guard]').hasAttribute('inert') && document.querySelector('[data-close-guard]').dataset.closeGuard==='starting'`);
 await click('重试退出保护');await until('document.querySelector("[data-close-guard=ready]")');
 await until('document.body.innerText.includes("test recovery read failed")');
 await check('Recovery read failure blocks edits until retry',`Array.from(document.querySelectorAll('.browser button')).find(b=>b.textContent.trim()==='＋ 新建工程')?.disabled`);
 await ev(`musicCloseRequest('recovery-not-ready')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Recovery initialization failure blocks close approval (mock desktop)',`document.querySelector('.dialog-body').textContent.includes('恢复检查')&&!musicClose.confirmed.length`);
 await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("recovery-not-ready")');
 await click('重试恢复检查');await until('!document.body.innerText.includes("test recovery read failed")');
 await click('＋ 新建工程','.browser');await fill('dialog[open] input','浏览器真实事务');
 await ev(`musicCloseRequest('existing-dialog')`);await until('musicClose.cancelled.includes("existing-dialog")');
 await check('Existing application dialog remains editable instead of queuing a close prompt',`document.querySelector('dialog[open] input').value==='浏览器真实事务'&&document.querySelectorAll('#dialog-title').length===1&&!document.querySelector('[data-close-guard]').hasAttribute('inert')&&!musicClose.confirmed.length`);
 await click('创建','dialog[open]');
 await until('document.querySelector(".music-editor")');
 // 新布局默认只显示编排；切换到拆分视图让钢琴卷帘与编排同屏（等价旧常驻布局）。
 await click('拆分','.transport-bar');await until('document.querySelector(".editor-center[data-view=split]")');
 await click('添加音轨');await until('document.querySelector("gsui-pianoroll .gsui-row")');
 await ev(`(async()=>{window.getMusic=async()=>{const ps=await musicCall('ListProjects');return musicCall('GetProject',ps.find(p=>p.title==='浏览器真实事务').id)}; window.base=await getMusic()})()`);
 // Seed through the actual service, keeping control and raw events in the same clip.
 await ev(`musicCall('SubmitProjectOps',base.id,'seed','cli',base.revision,[{kind:'note.add',trackId:base.state.tracks[0].id,clipId:base.state.tracks[0].clips[0].id,notes:[{id:'n_seed',start:480,duration:480,pitch:60,velocity:100,channel:0}]},{kind:'control.add',trackId:base.state.tracks[0].id,clipId:base.state.tracks[0].clips[0].id,events:[{kind:'cc',tick:0,channel:0,controller:64,value:127}]}])`);
 await until('document.querySelector("[data-id=n_seed]")');
 await check('External committed edits refresh the UI',`(await getMusic()).revision===2`);
 // The intention must persist before sending any music transaction.
 await ev('window.musicRecoveryBeginFails=true');await fill('[aria-label="片段起点（tick）"]','240');await keyboard('Enter','Enter');
 await until('document.body.innerText.includes("编辑未发送")');
 await check('Failed recovery write prevents the music command from being sent',`(await getMusic()).revision===2&&(await musicCall('GetPendingProjectEdit'))===null`);
 await ev('(()=>{window.musicRecoveryBeginFails=false;window.musicRecoveryBeginLoseReply=true})()');
 await fill('[aria-label="片段起点（tick）"]','240');await keyboard('Enter','Enter');await until('document.body.innerText.includes("Begin reply lost")');
 await check('Lost intention acknowledgment retains a durable query target without sending the edit',`(await getMusic()).revision===2&&(await musicCall('GetPendingProjectEdit')).projectID===base.id&&document.body.innerText.includes('查询保存结果')`);
 await click('查询保存结果');await until('document.querySelector("#dialog-title")?.textContent==="尚未查到已提交结果"');
 await click('继续等待','dialog[open]');
 await check('No committed result remains uncertain when the user continues waiting',`(await musicCall('GetPendingProjectEdit'))!==null&&(await getMusic()).revision===2`);
 await click('查询保存结果');await until('document.querySelector("#dialog-title")');await click('刷新工程并继续','dialog[open]');
 await until('(await musicCall("GetPendingProjectEdit"))===null');
 await ev(`document.querySelector('[aria-label="片段起点（tick）"]').focus()`);await keyboard('Escape','Escape');
 await ev('window.musicRecoveryClearFails=true');await fill('[aria-label="片段起点（tick）"]','120');await keyboard('Enter','Enter');
 await until('document.body.innerText.includes("恢复标记清理失败")');
 await check('Cleanup failure reports the committed result and retains recovery instead of replaying',`(await getMusic()).revision===3&&(await getMusic()).state.tracks[0].clips[0].start===120&&(await musicCall('GetPendingProjectEdit'))!==null&&document.body.innerText.includes('已保存')`);
 await ev('window.musicRecoveryClearFails=false');await click('查询保存结果');await until('(await musicCall("GetPendingProjectEdit"))===null');
 await check('Querying a committed operation retries cleanup without a second edit',`(await getMusic()).revision===3&&!document.body.innerText.includes('查询保存结果')`);
 await fill('[aria-label="片段起点（tick）"]','0');await keyboard('Enter','Enter');await until('(await getMusic()).state.tracks[0].clips[0].start===0');
 // Arrangement uses the same real project service, with separate view state.
 await ev(`(async()=>{const p=await getMusic();window.arrClip=p.state.tracks[0].clips[0].id;await musicCall('SubmitProjectOps',p.id,'arr-short','cli',p.revision,[{kind:'clip.update',clipId:arrClip,clipPatch:{length:1920}}]);window.arrBase=await getMusic()})()`);
 const arrBody='[data-clip] .arrangement-clip',arrResize='[data-clip] .arrangement-resize';
 await until(`document.querySelector('[data-clip] .arrangement-clip')?.title.includes('长度 1920')`);
 await drag(arrBody,64);await until('(await getMusic()).state.tracks[0].clips[0].start===480');
 await check('Arrangement mouse move changes only clip origin and preserves local events',`(()=>getMusic().then(p=>p.revision===arrBase.revision+1&&JSON.stringify({...p.state.tracks[0].clips[0],start:0})===JSON.stringify(arrBase.state.tracks[0].clips[0])))()`);
 await click('撤销','.transport-bar');await until('(await getMusic()).state.tracks[0].clips[0].start===0');
 await click('重做','.transport-bar');await until('(await getMusic()).state.tracks[0].clips[0].start===480');
 await check('Arrangement edits share project undo and redo',`(await getMusic()).revision===arrBase.revision+3`);
 await drag(arrResize,64);await until('(await getMusic()).state.tracks[0].clips[0].length===2400');
 await check('Arrangement right edge resize changes length without rewriting notes or controls',`(()=>getMusic().then(p=>JSON.stringify(p.state.tracks[0].clips[0].notes)===JSON.stringify(arrBase.state.tracks[0].clips[0].notes)&&JSON.stringify(p.state.tracks[0].clips[0].controls)===JSON.stringify(arrBase.state.tracks[0].clips[0].controls)))()`);
 await ev('window.arrReject=(await getMusic()).revision');
 await drag(arrResize,-256);await until('document.querySelector(".project-error")?.textContent.includes("超出片段范围")');
 await check('Arrangement shortening rejects a clip that cannot contain its existing note',`(await getMusic()).revision===arrReject&&(await getMusic()).state.tracks[0].clips[0].length===2400`);
 await drag(arrBody,-128);await until('document.querySelector(".project-error")?.textContent.includes("本次拖动不会提交")');
 await check('Arrangement negative origin preview is rejected without a transaction',`(await getMusic()).revision===arrReject&&(await getMusic()).state.tracks[0].clips[0].start===480`);
 await drag(arrBody,64,0,async()=>{await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'arr-concurrent','cli',p.revision,[{kind:'track.update',trackId:p.state.tracks[0].id,trackPatch:{name:'外部音轨修改'}}])})()`);await until('document.querySelector(".track-name").textContent==="外部音轨修改"')});
 await until('document.querySelector(".project-error")?.textContent.includes("刷新")');
 await check('Arrangement gesture retains its original revision and rejects a concurrent edit conflict',`(await getMusic()).revision===arrReject+1&&(await getMusic()).state.tracks[0].clips[0].start===480`);
 for(const reason of ['Escape','pointercancel','blur']){
   const r=await rect(arrBody);await mouse('mousePressed',r.x,r.y,{clickCount:1});await mouse('mouseMoved',r.x+64,r.y,{buttons:1});
   if(reason==='Escape')await keyboard('Escape','Escape');
   else if(reason==='blur')await ev('window.dispatchEvent(new Event("blur"))');
   else await ev(`(()=>{const b=document.querySelector('.arrangement-clip');for(let id=1;id<10;id++)if(b.hasPointerCapture(id))window.dispatchEvent(new PointerEvent('pointercancel',{pointerId:id}))})()`);
   await mouse('mouseReleased',r.x+64,r.y,{clickCount:1});await sleep(100);
   await check('Arrangement '+reason+' cancels the preview without saving',`!document.querySelector('.arrangement-block.dragging')&&(await getMusic()).revision===arrReject+1`);
 }
 const arrExit=await rect(arrBody);await mouse('mousePressed',arrExit.x,arrExit.y,{clickCount:1});await mouse('mouseMoved',arrExit.x+64,arrExit.y,{buttons:1});
 await ev(`musicCloseRequest('arrangement-gesture')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Arrangement active gesture blocks native-close request (mock desktop)',`document.querySelector('.dialog-body').textContent.includes('片段手势')&&!musicClose.confirmed.length`);
 await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("arrangement-gesture")');
 await keyboard('Escape','Escape');await mouse('mouseReleased',arrExit.x+64,arrExit.y,{clickCount:1});
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'arr-offset','cli',p.revision,[{kind:'clip.update',clipId:arrClip,clipPatch:{start:503}}])})()`);
 await until(`document.querySelector('.arrangement-clip').title.includes('起点 503')`);
 await ev(`(()=>{const e=document.querySelector('[aria-label="编排吸附"]');e.value='0.25';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 await drag(arrBody,8);await until('(await getMusic()).state.tracks[0].clips[0].start===623');
 await drag(arrBody,-8);await until('(await getMusic()).state.tracks[0].clips[0].start===503');
 await check('Fractional snap keeps the off-grid origin and rounds positive and negative half steps symmetrically',`(await getMusic()).state.tracks[0].clips[0].start===503`);
 await ev(`(()=>{const e=document.querySelector('[aria-label="编排吸附"]');e.value='1';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 // Long, overlapping and off-page clips remain accessible on two tracks.
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'arr-layout','cli',p.revision,[{kind:'clip.update',clipId:arrClip,clipPatch:{start:0,length:9600}},{kind:'clip.add',trackId:p.state.tracks[0].id,clip:{trackId:p.state.tracks[0].id,id:'c_overlap',name:'重叠片段',start:480,length:960}},{kind:'clip.add',trackId:p.state.tracks[0].id,clip:{trackId:p.state.tracks[0].id,id:'c_far',name:'远处片段',start:15360,length:480}},{kind:'track.add',track:{id:'t_arr_other',name:'编排第二轨',channel:1,program:1}},{kind:'clip.add',trackId:'t_arr_other',clip:{trackId:'t_arr_other',id:'c_arr_other',name:'第二轨片段',start:0,length:960}}]);window.arrViews=(await getMusic()).revision})()`);
 await until('document.querySelectorAll(".arrangement-track").length===2');
 await check('Overlapping clips use distinct accessible lanes and cropped ends have no fake resize handle',`document.querySelector('[data-clip="c_overlap"]').dataset.lane!==document.querySelector('[data-clip="'+arrClip+'"]').dataset.lane&&document.querySelector('[data-clip="'+arrClip+'"]').dataset.cropRight==='true'&&!document.querySelector('[data-clip="'+arrClip+'"] .arrangement-resize')`);
 const selectionGesture=await rect('[data-clip="'+await ev('arrClip')+'"] .arrangement-clip');
 await mouse('mousePressed',selectionGesture.x,selectionGesture.y,{clickCount:1});await mouse('mouseMoved',selectionGesture.x+64,selectionGesture.y,{buttons:1});
 await ev(`(()=>{const e=document.querySelector('[aria-label="选择片段"]');e.value='c_overlap';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 await until('!document.querySelector(".arrangement-block.dragging")');await mouse('mouseReleased',selectionGesture.x+64,selectionGesture.y,{clickCount:1});
 await check('Changing selected clip cancels an arrangement gesture without retargeting it',`(await getMusic()).revision===arrViews&&(await getMusic()).state.tracks[0].clips[0].start===0&&document.querySelector('[aria-label="选择片段"]').value==='c_overlap'`);
 const arrangementShot=await send('Page.captureScreenshot');writeFileSync(resolve(dir,'arrangement.png'),Buffer.from(arrangementShot.data,'base64'));
 await click('编排下一页');await until('document.querySelector(".arrangement").dataset.pageStart==="7680"');
 await check('A clip spanning the page renders its continuation with its real right edge',`document.querySelector('[data-clip="'+arrClip+'"]').dataset.cropLeft==='true'&&document.querySelector('[data-clip="'+arrClip+'"] .arrangement-resize')&&!document.querySelector('[data-clip="c_overlap"]')`);
 await ev(`(()=>{const e=document.querySelector('[aria-label="选择片段"]');e.value='c_far';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);await click('显示选中片段');
 await until('document.querySelector(".arrangement").dataset.pageStart==="15360"');
 await check('Off-page clips remain selectable and reveal their absolute timeline page',`document.querySelector('[data-clip="c_far"] .arrangement-clip').getAttribute('aria-pressed')==='true'`);
 await fill('[aria-label="编排缩放"]','96');await until('document.querySelector(".arrangement .music-ruler").getAttribute("width")==="1536"');
 await click('编排上一页');await click('编排上一页');
 await ev(`document.querySelector('[data-clip="c_arr_other"] .arrangement-clip').click()`);
 await until('document.querySelector(".track-inspector").textContent.includes("编排第二轨")');
 await check('Selecting a clip on another track opens that clip without creating history',`document.querySelector('[aria-label="选择片段"]').value==='c_arr_other'&&(await getMusic()).revision===arrViews`);
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'arr-restore','cli',p.revision,[{kind:'track.remove',trackId:'t_arr_other'},{kind:'clip.remove',clipId:'c_overlap'},{kind:'clip.remove',clipId:'c_far'},{kind:'track.update',trackId:p.state.tracks[0].id,trackPatch:{name:'音轨 1'}},{kind:'clip.update',clipId:arrClip,clipPatch:{start:0,length:7680}}])})()`);
 await until('document.querySelectorAll(".arrangement-track").length===1&&document.querySelector("[data-id=n_seed]")');
 await ev(`(()=>{document.querySelector('[aria-label="静音 音轨 1"]').click()})()`);await until(`(await getMusic()).state.tracks[0].muted&&!document.querySelector('[aria-label="独奏 音轨 1"]').disabled`);
 await ev(`document.querySelector('[aria-label="独奏 音轨 1"]').click()`);await until(`(await getMusic()).state.tracks[0].solo&&!document.querySelector('[aria-label="独奏 音轨 1"]').disabled`);
 await click('撤销','.transport-bar');await until('!(await getMusic()).state.tracks[0].solo');
 await click('撤销','.transport-bar');await until('!(await getMusic()).state.tracks[0].muted');
 await check('Arrangement retains mute and solo edits in shared undo history',`!document.querySelector('[aria-label="静音 音轨 1"]').getAttribute('aria-pressed').includes('true')&&!document.querySelector('[aria-label="独奏 音轨 1"]').getAttribute('aria-pressed').includes('true')`);
 // Verify UI wiring separately from native PCM and driver tests.
 await click('播放','.transport');await until('window.musicTransport.playing');
 await click('停止','.transport');await until('!window.musicTransport.playing && document.querySelector(".transport").dataset.busy==="false"');
 await fill('[aria-label="播放位置"]','120');await click('定位','.transport');
 await until('window.musicTransport.tick===120');
 await ev(`document.querySelector('[aria-label="启用循环"]').click()`);await fill('[aria-label="循环终点"]','480');await click('应用循环','.transport');
 await until('window.musicTransport.loop&&window.musicTransport.loopEnd===480');
 await ev('window.musicAudioFailure=true');await click('播放','.transport');
 await until('document.querySelector(".transport").textContent.includes("test device unavailable")');
 await ev('window.musicAudioFailure=false');await click('播放','.transport');await click('停止','.transport');
 report.checks.push('Transport UI play/stop/seek/loop and device error handling (mock audio)');

 // Real time-map forms submit one revision through the shared command service.
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'map-seed','cli',p.revision,[{kind:'tempo.set',tempo:[{tick:0,usPerQuarter:500001},{tick:1920,usPerQuarter:600000}]},{kind:'meter.set',meter:[{tick:0,numerator:4,denominator:4},{tick:1920,numerator:3,denominator:4}]}]);window.mapBase=await getMusic()})()`);
 await until('document.querySelectorAll(".piano-host .music-ruler [data-kind=meter]").length===2');
 await click('编辑速度与拍号');await until('document.querySelector(".time-map-dialog[open]")');
 await fill('[aria-label="速度 BPM 1"]','90');
 await fill('[aria-label="拍号分子 1"]','6');
 await ev(`(()=>{const e=document.querySelector('[aria-label="拍号分母 1"]');e.value='8';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 await click('添加速度点','.time-map-dialog');await fill('[aria-label="速度位置 2"]','3360');await fill('[aria-label="速度 BPM 2"]','70');
 await click('添加拍号点','.time-map-dialog');await fill('[aria-label="拍号位置 3"]','3360');await fill('[aria-label="拍号分子 3"]','5');
 await ev(`(()=>{const e=document.querySelector('[aria-label="拍号分母 3"]');e.value='8';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 await click('应用时间图','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")');
 await check('Tempo/meter forms commit atomically, preserve untouched precision and note/control data',`(()=>getMusic().then(p=>p.revision===mapBase.revision+1&&p.state.tempo.length===3&&p.state.tempo[0].usPerQuarter===500001&&p.state.tempo[1].usPerQuarter===666667&&p.state.meter[2].numerator===5&&JSON.stringify(p.state.tracks)===JSON.stringify(mapBase.state.tracks)))()`);
 await click('撤销','.transport-bar');await until('(await getMusic()).state.meter.length===2');
 await check('Undo restores both time maps together',`(await getMusic()).state.tempo[1].usPerQuarter===600000`);
 await click('重做','.transport-bar');await until('(await getMusic()).state.meter.length===3');
 await check('Ruler follows 4/4 to 6/8 to 5/8 changes',`document.querySelector('.piano-host .music-ruler [data-kind=bar][data-tick="3360"]').textContent.includes('第 3 小节')`);
 await ev('window.beforeRuler=(await getMusic()).revision');
 const rulerHit=await rect('.piano-host .music-ruler [data-kind=meter] text');await mouse('mouseMoved',rulerHit.x,rulerHit.y);await mouse('mousePressed',rulerHit.x,rulerHit.y,{clickCount:1});await mouse('mouseReleased',rulerHit.x,rulerHit.y,{clickCount:1});
 await check('Ruler labels accept hover inspection while pointer input stays read-only',`document.elementFromPoint(${rulerHit.x},${rulerHit.y})?.closest('.piano-host .music-ruler')!==null&&(await getMusic()).revision===beforeRuler`);

 await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','');
 await ev('window.beforeInvalid=(await getMusic()).revision');await click('应用时间图','.time-map-dialog');
 await until('document.querySelector(".time-map-dialog").textContent.includes("有效 BPM")');
 await check('Invalid time-map input leaves the project untouched',`(await getMusic()).revision===beforeInvalid&&document.querySelector('[aria-label="速度位置 0"]').disabled&&document.querySelector('[aria-label="删除拍号点 0"]').disabled`);
 await fill('[aria-label="速度 BPM 0"]','120');await fill('[aria-label="拍号位置 1"]','0');await click('应用时间图','.time-map-dialog');
 await until('document.querySelector(".time-map-dialog").textContent.includes("重复 tick")');
 await keyboard('Escape','Escape');await until('document.querySelector("dialog[open]:not(.time-map-dialog)")');
 await click('继续编辑','dialog[open]:not(.time-map-dialog)');
 await check('Closing a dirty map requires explicit discard and cancellation preserves inputs',`document.querySelector('.time-map-dialog[open]')&&document.querySelector('[aria-label="拍号位置 1"]').value==='0'&&(await getMusic()).revision===beforeInvalid`);
 await click('关闭时间图','.time-map-dialog');await click('丢弃草稿','dialog[open]:not(.time-map-dialog)');
 await until('!document.querySelector(".time-map-dialog")');

 await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','115');await fill('[aria-label="拍号分子 0"]','7');
 await ev(`(()=>{const e=document.querySelector('[aria-label="拍号分母 0"]');e.value='8';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 await click('应用时间图','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")');
 await check('Replacement form edits the initial tempo and meter without losing later points',`(await getMusic()).state.tempo[0].usPerQuarter===521739&&(await getMusic()).state.meter[0].numerator===7&&(await getMusic()).state.meter[0].denominator===8&&(await getMusic()).state.meter.length===3`);
 await click('撤销','.transport-bar');await until('(await getMusic()).state.tempo[0].usPerQuarter===500001');
 await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','111');
 await ev(`(async()=>{const p=await getMusic();window.mapConflictBase=p.revision;await musicCall('SubmitProjectOps',p.id,'map-conflict','cli',p.revision,[{kind:'note.update',clipId:p.state.tracks[0].clips[0].id,noteDeltas:[{id:'n_seed',velocity:99}]}])})()`);
 await until('document.querySelector(".time-map-dialog").textContent.includes("工程已变为修订")');
 await click('应用时间图','.time-map-dialog');await until('document.querySelector(".time-map-dialog").textContent.includes("草稿已保留")');
 await check('Time-map draft freezes its revision and does not overwrite a background edit',`(await getMusic()).revision===mapConflictBase+1&&document.querySelector('[aria-label="速度 BPM 0"]').value==='111'`);
 await click('重新载入当前图','.time-map-dialog');await click('丢弃草稿','dialog[open]:not(.time-map-dialog)');
 await until('Number(document.querySelector(\'[aria-label="速度 BPM 0"]\').value)===60000000/500001');
 await click('关闭时间图','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")');

 // Ruler and notes share the actual scroll container and quarter-note scale.
 const aligned=()=>`(()=>{const n=document.querySelector('[data-id=n_seed]').getBoundingClientRect(),r=document.querySelector('.piano-host .music-ruler').getBoundingClientRect(),z=Number(document.querySelector('gsui-timewindow').getAttribute('pxperbeat'));return Math.abs(n.left-r.left-z)<3})()`;
 await check('Ruler absolute tick origin aligns with note positions',aligned());
 await ev(`document.querySelector('.gsuiTimewindow-scrollArea').scrollLeft=100`);await sleep(150);
 await check('Ruler remains aligned after horizontal scroll',aligned());
 await fill('[aria-label="钢琴卷帘缩放"]','96');await until('document.querySelector(".piano-host .music-ruler").getAttribute("width")==="1536"');
 await check('Ruler remains aligned after zoom',aligned());
 await fill('[aria-label="钢琴卷帘缩放"]','64');
 const wheelTarget=await rect('[data-id=n_seed]');await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:wheelTarget.x,y:wheelTarget.y,deltaX:0,deltaY:-180,modifiers:2});
 await until(`Number(document.querySelector('gsui-timewindow').getAttribute('pxperbeat'))!==64&&Number(document.querySelector('.piano-host .music-ruler').getAttribute('width'))===16*Number(document.querySelector('gsui-timewindow').getAttribute('pxperbeat'))`);
 await check('Ctrl-wheel zoom keeps the ruler and host zoom control synchronized',aligned());
 await fill('[aria-label="钢琴卷帘缩放"]','64');
 await ev(`document.querySelector('.gsuiTimewindow-scrollArea').scrollLeft=0`);await sleep(100);
 await fill('[aria-label="片段起点（tick）"]','240');
 await keyboard('Enter','Enter');
 await until('document.querySelector(".piano-host .music-ruler").dataset.start==="240"');
 await check('Nonzero clip start uses absolute project meter positions',`document.querySelector('.ruler-position').textContent.includes('1 小节 1 拍 +240 tick')`);
 await fill('[aria-label="片段起点（tick）"]','2147483647');await keyboard('Enter','Enter');
 await until('document.querySelector(".piano-host .music-ruler").dataset.start==="2147483647"');
 await check('A clip at the maximum map tick still has a ruler extending beyond that tick',`document.querySelector('.piano-host .music-ruler [data-kind=bar]')!==null`);
 await fill('[aria-label="片段起点（tick）"]','0');await keyboard('Enter','Enter');await until('document.querySelector(".piano-host .music-ruler").dataset.start==="0"');
 await fill('[aria-label="片段起点（tick）"]','120');
 await ev(`(async()=>{const p=await getMusic();window.numberConflict=p.revision;await musicCall('SubmitProjectOps',p.id,'numeric-conflict','cli',p.revision,[{kind:'note.update',clipId:p.state.tracks[0].clips[0].id,noteDeltas:[{id:'n_seed',velocity:98}]}])})()`);
 await until('document.querySelector(".save-chip").textContent.includes("修订 "+(numberConflict+1))||(await getMusic()).revision>=numberConflict+1');
 await keyboard('Enter','Enter');await until('document.querySelector(".project-error")?.textContent.includes("修改未提交")');
 await check('Numeric Enter commits preserve the focus revision and reject concurrent edits',`(await getMusic()).revision===numberConflict+1&&(await getMusic()).state.tracks[0].clips[0].start===0`);

 await drag('[data-id=n_seed]',64);
 await until('(await getMusic()).state.tracks[0].clips[0].notes[0].start===960');
 report.checks.push('Real mouse move commits to SQLite');
 await click('撤销','.transport-bar');await until('(await getMusic()).state.tracks[0].clips[0].notes[0].start===480');
 await click('重做','.transport-bar');await until('(await getMusic()).state.tracks[0].clips[0].notes[0].start===960');
 report.checks.push('Shared undo/redo refreshes the editor');
 await drag('[data-id=n_seed] [data-action=cropB]',64);
 await until('(await getMusic()).state.tracks[0].clips[0].notes[0].duration===960');
 report.checks.push('Real resize commits duration');
 // Shift-click selects, velocity patches preserve controller data.
 const noteRect=await rect('[data-id=n_seed]');await mouse('mousePressed',noteRect.x,noteRect.y,{clickCount:1,modifiers:8});await mouse('mouseReleased',noteRect.x,noteRect.y,{clickCount:1,modifiers:8});
 await until('document.querySelector("[data-id=n_seed][data-selected]")');
 await fill('[aria-label="音符力度"]','72');await click('应用力度');
 await until('(await getMusic()).state.tracks[0].clips[0].notes[0].velocity===72');
 await check('MIDI velocity preserves controllers',`(await getMusic()).state.tracks[0].clips[0].controls[0].value===127`);
 await click('复制音符');await until('(await getMusic()).state.tracks[0].clips[0].notes.length===2');
 await click('全选本页');await click('删除音符');await until('(await getMusic()).state.tracks[0].clips[0].notes.length===0');
 await click('撤销','.transport-bar');await until('document.querySelectorAll(".gsuiPianoroll-block").length===2');
 report.checks.push('Selection, copy, atomic deletion and undo');
 // Stale revision must not be replaced by the event refresh during a gesture.
 await drag('[data-id=n_seed]',64,0,async()=>{
   await ev(`(async()=>{window.beforeConflict=await getMusic();await musicCall('SubmitProjectOps',beforeConflict.id,'external-conflict','cli',beforeConflict.revision,[{kind:'note.update',trackId:beforeConflict.state.tracks[0].id,clipId:beforeConflict.state.tracks[0].clips[0].id,noteDeltas:[{id:'n_seed',pitch:67}]}])})()`);
   await sleep(200);
 });
 await until('document.querySelector(".project-error")?.textContent.includes("修改")');
 await check('Gesture freezes revision and rejects conflicts',`(()=>getMusic().then(p=>p.revision===beforeConflict.revision+1&&p.state.tracks[0].clips[0].notes[0].start===beforeConflict.state.tracks[0].clips[0].notes[0].start&&p.state.tracks[0].clips[0].notes[0].pitch===67))()`);
 await until('!document.querySelector(".piano-host gsui-pianoroll[disabled]")');
 // Escape discards a live drag with no transaction.
 const cancelRect=await rect('[data-id=n_seed]');await ev('window.beforeCancel=(await getMusic()).revision');
 await mouse('mousePressed',cancelRect.x,cancelRect.y,{clickCount:1});await mouse('mouseMoved',cancelRect.x+64,cancelRect.y,{buttons:1});await keyboard('Escape','Escape');await mouse('mouseReleased',cancelRect.x+64,cancelRect.y,{clickCount:1});
 await sleep(200);await check('Escape cancels without committing',`(await getMusic()).revision===beforeCancel`);
 // Real empty-row click creates a note.
 const empty=await rect('.gsui-row[data-midi="62"] > div');
 const nr=await rect('[data-id=n_seed]');await mouse('mousePressed',nr.x+240,empty.y,{clickCount:1});await mouse('mouseReleased',nr.x+240,empty.y,{clickCount:1});
 await until('(await getMusic()).state.tracks[0].clips[0].notes.length===3');report.checks.push('Empty-row click adds a note');
 // Production approval card and ghost adapter, driven by actual scripted
 // Agent runs. Only the audio facade remains mocked in this browser suite.
 await ev(`(async()=>{window.previewBefore=await getMusic();window.previewClip=previewBefore.state.tracks[0].clips[0].id;window.previewCall=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(previewCall)})()`);
 await until('document.querySelector(".music-edit-approval")');
 await check('Music adoption is disabled until a valid preview is inspected',`Array.from(document.querySelectorAll('.music-edit-approval button')).find(b=>b.textContent==='采用这次修改').disabled`);
 await click('查看音符差异','.music-edit-approval');await until('document.querySelector(".note-diff-table tbody tr")');
 await check('Candidate preview reads real pending Agent parameters without changing project revision',`(await getMusic()).revision===previewBefore.revision&&document.querySelector('.note-diff-table').textContent.includes('力度')`);
 await click('在卷帘查看','.music-edit-approval');await until('document.querySelector(".piano-ghost-after")');
 await check('Ghosts are read-only and excluded from vendor note count',`getComputedStyle(document.querySelector('.piano-ghost')).pointerEvents==='none'&&Array.from(document.querySelectorAll('.piano-ghost')).every(e=>!e.dataset.id)&&(await getMusic()).state.tracks[0].clips[0].notes.length===3`);
 await click('试听候选','.music-edit-approval');await until('document.querySelector(".preview-playing")');
 await check('Preview transport explicitly labels the uncommitted candidate (mock audio)',`document.querySelector('.preview-playing').textContent.includes('候选')&&(await getMusic()).revision===previewBefore.revision`);
 await click('采用这次修改','.music-edit-approval');await until('(await getMusic()).revision===previewBefore.revision+1&&!document.querySelector(".music-edit-approval")&&!document.querySelector(".piano-ghost")');
 await check('Adoption executes the original Agent transaction and retires ghosts',`(await getMusic()).state.tracks[0].clips[0].notes.every((n,i)=>n.pitch===previewBefore.state.tracks[0].clips[0].notes[i].pitch+1)`);
 await click('撤销','.transport-bar');await until('(await getMusic()).revision===previewBefore.revision+2');
 await check('Agent candidate uses the same GUI undo history',`JSON.stringify((await getMusic()).state.tracks[0].clips[0].notes)===JSON.stringify(previewBefore.state.tracks[0].clips[0].notes)`);
 await ev(`(async()=>{window.discardBefore=(await getMusic()).revision;window.previewCall=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(previewCall)})()`);
 await click('查看音符差异','.music-edit-approval');await until('document.querySelector(".note-diff-table tbody tr")');await click('在卷帘查看','.music-edit-approval');
 await click('丢弃候选','.music-edit-approval');await until('!document.querySelector(".music-edit-approval")&&!document.querySelector(".piano-ghost")');
 await check('Discarding a pending candidate leaves music untouched',`(await getMusic()).revision===discardBefore`);
 await ev(`(async()=>{window.previewCall=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(previewCall)})()`);
 await click('查看音符差异','.music-edit-approval');await until('document.querySelector(".note-diff-table tbody tr")');await click('在卷帘查看','.music-edit-approval');
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'preview-user-edit','external',p.revision,[{kind:'project.rename',title:p.title}])})()`);
 await until('document.querySelector(".music-edit-approval [role=alert]")?.textContent.includes("过时")&&!document.querySelector(".piano-ghost")');
 await check('User edit invalidates candidate projection and disables adoption',`Array.from(document.querySelectorAll('.music-edit-approval button')).find(b=>b.textContent==='采用这次修改').disabled`);
 await click('丢弃候选','.music-edit-approval');await until('!document.querySelector(".music-edit-approval")');
 await ev(`(async()=>{window.previewCall=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(previewCall);window.musicPreviewHold=true})()`);
 await click('查看音符差异','.music-edit-approval');await until('window.musicPreviewRelease');
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'preview-inflight-user-edit','external',p.revision,[{kind:'project.rename',title:p.title}])})()`);
 await until('document.querySelector(".music-edit-approval [role=alert]")?.textContent.includes("过时")');
 await ev(`(()=>{window.musicPreviewHold=false;musicPreviewRelease();delete window.musicPreviewRelease})()`);
 await sleep(100);await check('Project change invalidates even an in-flight preview response',`!document.querySelector('.note-diff-table')&&Array.from(document.querySelectorAll('.music-edit-approval button')).find(b=>b.textContent==='采用这次修改').disabled`);
 await click('丢弃候选','.music-edit-approval');await until('!document.querySelector(".music-edit-approval")');
 for(const retire of ['stop','unmount']) {
   await ev(`(async()=>{window.previewCall=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(previewCall)})()`);
   await click('查看音符差异','.music-edit-approval');await until('document.querySelector(".note-diff-table tbody tr")');
   await ev(`(()=>{window.musicPreviewPlayHold=true;window.previewPlayBefore=window.musicPreviewPlayCompleted??0})()`);
   await click('试听候选','.music-edit-approval');await until('window.musicPreviewPlayRelease');
   if(retire==='stop')await click('停止候选试听','.music-edit-approval');
   else {await ev(`musicShowPreview(null)`);await until('!document.querySelector(".music-edit-approval")')}
   await ev(`(()=>{window.musicPreviewPlayHold=false;musicPreviewPlayRelease();delete window.musicPreviewPlayRelease})()`);
   await until('(window.musicPreviewPlayCompleted??0)>previewPlayBefore&&!musicTransport.previewId&&!musicTransport.playing');
   await check(retire+' retires a late audition request (mock audio)',`!musicTransport.previewId&&!musicTransport.playing`);
   await ev(`(async()=>{await musicCall('DenyToolCall',previewCall.id,'结束迟到试听');musicShowPreview(null)})()`);
 }
 await ev(`(async()=>{window.lateA=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(lateA);window.musicPreviewHold=true})()`);
 await click('查看音符差异','.music-edit-approval');await until('window.musicPreviewRelease');
 await ev(`(async()=>{window.musicPreviewHold=false;window.lateB=await musicCall('SeedProjectPreview',previewBefore.id,previewClip);musicShowPreview(lateB);musicPreviewRelease();delete window.musicPreviewRelease})()`);
 await sleep(100);await check('Unmounted candidate response cannot replace a newer candidate view',`!document.querySelector('.note-diff-table')&&!document.querySelector('.piano-ghost')`);
 await ev(`(async()=>{await musicCall('DenyToolCall',lateA.id,'结束旧候选');await musicCall('DenyToolCall',lateB.id,'结束新候选');musicShowPreview(null)})()`);
 // Keyboard edits use the same batch path; IME input in numeric controls
 // must not trigger note operations.
 await click('全选本页');
 await ev(`(()=>{document.querySelector('gsui-pianoroll').focus();window.keyboardBefore=null})()`);
 await ev('window.keyboardBefore=await getMusic()');await keyboard('ArrowUp','ArrowUp');
 await until('(await getMusic()).revision===keyboardBefore.revision+1');
 await check('Arrow key submits one atomic selected-note batch',`(await getMusic()).state.tracks[0].clips[0].notes.every((n,i)=>n.pitch===keyboardBefore.state.tracks[0].clips[0].notes[i].pitch+1)`);
 await keyboard('z','KeyZ',2);await until('(await getMusic()).revision===keyboardBefore.revision+2');
 await ev(`document.querySelector('[aria-label="音符力度"]').dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',isComposing:true,bubbles:true}))`);
 await ev(`document.querySelector('gsui-pianoroll').dispatchEvent(new KeyboardEvent('keydown',{key:'Delete',isComposing:true,bubbles:true}))`);
 await sleep(100);await check('Composition/input events do not edit notes',`(await getMusic()).revision===keyboardBefore.revision+2`);
 // View controls change projection rather than music state.
 await fill('[aria-label="钢琴卷帘缩放"]','96');
 await until('document.querySelector("gsui-timewindow").getAttribute("pxperbeat")==="96"');
 await check('Zoom does not change the project revision',`(await getMusic()).revision===keyboardBefore.revision+2`);
 await fill('[aria-label="钢琴卷帘缩放"]','64');
 await until('document.querySelector("gsui-timewindow").getAttribute("pxperbeat")==="64"');
 // Resizing past the clip boundary extends it in the same persisted batch.
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'near-end','cli',p.revision,[{kind:'clip.update',clipId:p.state.tracks[0].clips[0].id,clipPatch:{length:7680}},{kind:'note.add',clipId:p.state.tracks[0].clips[0].id,notes:[{id:'n_end',start:7200,duration:480,pitch:60,velocity:100,channel:0}]}])})()`);
 await until('document.querySelector("[data-id=n_end]")');await drag('[data-id=n_end] [data-action=cropB]',64);
 await until('(await getMusic()).state.tracks[0].clips[0].length===8160');
 await check('Boundary resize atomically extends the clip',`(await getMusic()).state.tracks[0].clips[0].notes.find(n=>n.id==='n_end').duration===960`);
 await ev('window.boundaryEdit=await getMusic()');
 await click('撤销','.transport-bar');await until('(await getMusic()).state.tracks[0].clips[0].length===7680');
 await check('Undo removes the boundary extension after restoring the note in one transaction',`(await getMusic()).revision===boundaryEdit.revision+1&&(await getMusic()).state.tracks[0].clips[0].notes.find(n=>n.id==='n_end').duration===480`);
 await click('重做','.transport-bar');await until('(await getMusic()).state.tracks[0].clips[0].length===8160');
 await check('Redo restores the extended clip and note together',`(await getMusic()).revision===boundaryEdit.revision+2&&JSON.stringify((await getMusic()).state)===JSON.stringify(boundaryEdit.state)`);
 // Large source clip: page projection bounds mounted nodes for spread-out notes.
 for(const count of [1000,10000]){
   const start=performance.now();await ev(`(async()=>{window.perfBase=await getMusic();await musicCall('SubmitProjectOps',perfBase.id,'perf-${count}','cli',perfBase.revision,[{kind:'clip.update',trackId:perfBase.state.tracks[0].id,clipId:perfBase.state.tracks[0].clips[0].id,clipPatch:{length:${count}*120+960}},{kind:'note.remove',trackId:perfBase.state.tracks[0].id,clipId:perfBase.state.tracks[0].clips[0].id,noteIds:perfBase.state.tracks[0].clips[0].notes.map(n=>n.id)},{kind:'note.add',trackId:perfBase.state.tracks[0].id,clipId:perfBase.state.tracks[0].clips[0].id,notes:Array.from({length:${count}},(_,i)=>({id:'n_perf_${count}_'+i,start:i*120,duration:120,pitch:48+i%36,velocity:100,channel:0}))}])})()`);
   await until(`document.body.innerText.includes('共 ${count} 个音符')`);
   const refreshMs=performance.now()-start;
   const projectionMs=await ev('Number(document.querySelector("gsui-pianoroll").dataset.projectionMs)');
   const mounted=await ev('document.querySelectorAll(".gsuiPianoroll-block").length');
   const flip=performance.now();await click('下一页');await until('document.body.innerText.includes("四分拍 17–32")');
   await check(`Ruler page offset follows absolute tick for ${count} notes`,`document.querySelector('.piano-host .music-ruler').dataset.start==='7680'&&document.querySelector('.ruler-position').textContent.includes('6 小节 4 拍')`);
   report.timings.push({count,mounted,serviceCommitAndRefreshMs:refreshMs,projectionMs,pageFlipMs:performance.now()-flip});
   if(mounted>64)throw Error('unbounded page projection');await click('上一页');
 }
 report.checks.push('1k/10k spread-out notes mount at most 64 notes per page');
 // A lost bridge reply must resolve by the original operation ID, exactly once.
 await ev('window.musicLoseReply=true');await click('全选本页');
 await fill('[aria-label="音符力度"]','81');await click('应用力度');
 await until('!document.querySelector(".piano-host gsui-pianoroll[disabled]") && document.body.innerText.includes("已保存")');
 await check('Lost commit reply is resolved without replay',`(await getMusic()).state.tracks[0].clips[0].notes.filter(n=>n.velocity===81).length===64 && !document.body.innerText.includes('保存结果未知')`);
 // Lose the result query as well: keep the original ID and stop edits until queried.
 await ev('(()=>{window.musicLoseReply=true; window.musicLookupFails=1})()');
 await fill('[aria-label="音符力度"]','82');await click('应用力度');
 await until('document.body.innerText.includes("保存结果未知")');
 await ev(`musicCloseRequest('unknown-save')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Unknown project outcome blocks native exit without replay',`!musicClose.confirmed.length&&document.querySelector('.dialog-body').textContent.includes('查询保存结果')`);
 await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("unknown-save")');
 const persistedOperation=await ev('musicCall("GetPendingProjectEdit")'), persistedRevision=await ev('(await getMusic()).revision');
 await send('Page.removeScriptToEvaluateOnNewDocument',{identifier:closeInit.identifier});
 await ev('sessionStorage.clear()');await send('Page.reload');await until('window.musicCall&&document.querySelector("[data-close-guard=ready]")&&document.body.innerText.includes("发现待确认工程操作")');
 await ev(`window.getMusic=async()=>{const ps=await musicCall('ListProjects');return musicCall('GetProject',ps.find(p=>p.title==='浏览器真实事务').id)}`);
 await check('Browser reload with empty sessionStorage restores the Go journal without replay',`(await musicCall('GetPendingProjectEdit')).opID===${JSON.stringify(persistedOperation.opID)}&&(await getMusic()).revision===${persistedRevision}`);
 await ev(`window.recoveryOther=await musicCall('CreateProject','恢复时查看的工程',480)`);
 await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.textContent.includes('恢复时查看的工程'))`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.textContent.includes('恢复时查看的工程')).click()`);
 await until('document.querySelector(".transport-title")?.textContent==="恢复时查看的工程"');
 await click('查询保存结果');
 await until('!document.body.innerText.includes("查询保存结果")');
 await check('Recovery queries the original operation while preserving the newly selected project',`document.querySelector('.transport-title')?.textContent==='恢复时查看的工程'&&document.querySelector('.save-chip')?.textContent.includes('浏览器真实事务')&&(await musicCall('GetPendingProjectEdit'))===null&&(await getMusic()).revision===${persistedRevision}`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.textContent.includes('浏览器真实事务')).click()`);
 await until('document.querySelector("[data-id=n_perf_10000_0]")');
 await check('Unknown outcome retains a queryable operation ID',`(await getMusic()).state.tracks[0].clips[0].notes.filter(n=>n.velocity===82).length===64`);
 // Deleting a project must not make its old pending marker impossible to resolve.
 await ev(`(async()=>{const p=await musicCall('CreateProject','已删除的恢复工程',480);await musicCall('BeginProjectEdit',p.id,'deleted-before-commit',0,'edit');await musicCall('DeleteProject',p.id)})()`);
 await send('Page.reload');await until('window.musicCall&&document.body.innerText.includes("发现待确认工程操作")');
 await ev(`window.getMusic=async()=>{const ps=await musicCall('ListProjects');return musicCall('GetProject',ps.find(p=>p.title==='浏览器真实事务').id)}`);
 await click('查询保存结果');await until('document.querySelector("#dialog-title")?.textContent==="尚未查到已提交结果"');
 await check('Deleted project recovery keeps its original identity and requires explicit stop-waiting',`document.querySelector('.dialog-body').textContent.includes('已删除的恢复工程')&&(await musicCall('GetPendingProjectEdit')).opID==='deleted-before-commit'`);
 await click('刷新工程并继续','dialog[open]');await until('(await musicCall("GetPendingProjectEdit"))===null');
 await check('Explicitly stopping recovery for a deleted project does not recreate or replay music',`!(await musicCall('ListProjects')).some(p=>p.title==='已删除的恢复工程')&&(await getMusic()).revision===${persistedRevision}`);
 // Rename through the production UI, then traverse the shared history and
 // reload. The service must persist its lightweight metadata with the state.
 const titledProject=await ev(`musicCall('CreateProject','名称持久化原名',480)`);
 await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.dataset.id===${JSON.stringify(titledProject.id)})`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(titledProject.id)}).click()`);
 await until('document.querySelector(".transport-title")?.textContent==="名称持久化原名"');
 await click('名称持久化原名','.transport-bar');
 await check('Rename dialog starts from the committed project name',`document.querySelector('dialog[open] input').value==='名称持久化原名'`);
 await fill('dialog[open] input','跨入口的新曲名 🎹');await ev('window.musicLoseReply=true');await click('应用','dialog[open]');
 await until('document.querySelector(".transport-title")?.textContent==="跨入口的新曲名 🎹"&&!document.body.innerText.includes("查询保存结果")');
 await check('Lost rename reply resolves once and updates header list and authoritative state',`(async()=>{const p=await musicCall('GetProject',${JSON.stringify(titledProject.id)});return p.revision===1&&p.title==='跨入口的新曲名 🎹'&&p.state.title===p.title&&Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===p.id).textContent.includes(p.title)})()`);
 await click('导出 MIDI','.transport-bar');await until('window.musicExportName');
 await check('Export picker receives the current committed name (mock picker cancelled)',`musicExportName==='跨入口的新曲名 🎹.mid'`);
 await click('撤销','.transport-bar');await until('document.querySelector(".transport-title")?.textContent==="名称持久化原名"');
 await check('Rename undo restores both visible metadata and the state title',`(async()=>{const p=await musicCall('GetProject',${JSON.stringify(titledProject.id)});return p.revision===2&&p.state.title===p.title&&Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===p.id).textContent.includes('名称持久化原名')})()`);
 await click('重做','.transport-bar');await until('document.querySelector(".transport-title")?.textContent==="跨入口的新曲名 🎹"');
 await check('Rename redo restores the committed name across projections',`(async()=>{const p=await musicCall('GetProject',${JSON.stringify(titledProject.id)});return p.revision===3&&p.title===p.state.title&&Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===p.id).textContent.includes(p.title)})()`);
 await until('(await musicCall("GetPendingProjectEdit"))===null');const titlePageOrigin=await ev('performance.timeOrigin');await send('Page.reload');
 await until(`performance.timeOrigin!==${titlePageOrigin}&&window.musicCall&&document.querySelector('[data-close-guard=ready]')&&document.querySelector('.transport-title')?.textContent==='跨入口的新曲名 🎹'`);
 await check('Reload keeps the renamed selected project without adding a revision',`(async()=>{const p=await musicCall('GetProject',${JSON.stringify(titledProject.id)});return p.revision===3&&p.title===p.state.title&&Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===p.id).textContent.includes(p.title)})()`);
 // Service-generated IDs must survive GUI undo/redo, including later edits
 // that refer to those IDs. Seed via the real command service, not UI IDs.
 const generatedStates=[(await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`)).state];
 // Blank uses null for tracks; undoing the only track uses []. Both mean
 // no objects. Normalize only this test baseline, not production state.
 generatedStates[0].tracks??=[];
 const generatedEdit=async ops=>{
   const p=await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`);
   const result=await ev(`musicCall('SubmitProjectOps',${JSON.stringify(titledProject.id)},'generated-${generatedStates.length}','cli',${p.revision},${JSON.stringify(ops)})`);
   generatedStates.push((await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`)).state);return result.createdIds;
 };
 const [generatedTrack]=await generatedEdit([{kind:'track.add',track:{name:'自动身份音轨',channel:0,program:0}}]);
 const [generatedClip]=await generatedEdit([{kind:'clip.add',clip:{trackId:generatedTrack,start:0,length:1920}}]);
 const [generatedNote]=await generatedEdit([{kind:'note.add',clipId:generatedClip,notes:[{start:480,duration:480,pitch:60,velocity:100,channel:0}]},{kind:'control.add',clipId:generatedClip,events:[{kind:'cc',tick:0,channel:0,controller:64,value:127}]}]);
 await generatedEdit([{kind:'note.update',clipId:generatedClip,noteDeltas:[{id:generatedNote,pitch:64}]}]);
 await until(`document.querySelector('[data-id="${generatedNote}"]')`);
 await check('Service-generated notes are visible in the real editor',`document.querySelector('[aria-label="选择片段"]').value===${JSON.stringify(generatedClip)}`);
 for(const undo of [true,false]){
   for(let i=0;i<4;i++){
     const before=await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`);
     await click(undo?'撤销':'重做','.transport-bar');
     await until(`(await musicCall('GetProject',${JSON.stringify(titledProject.id)})).revision===${before.revision+1}&&!document.body.innerText.includes('查询保存结果')`);
     const index=undo?3-i:i+1;
     await check(`${undo?'Undo':'Redo'} generated-ID history step ${i+1} preserves objects and references`,`JSON.stringify((await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state)===${JSON.stringify(JSON.stringify(generatedStates[index]))}`);
   }
 }
 await until(`document.querySelector('[data-id="${generatedNote}"]')`);
 await click('撤销','.transport-bar');await until(`(await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state.tracks[0].clips[0].notes[0].pitch===60`);
 await click('重做','.transport-bar');await until(`(await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state.tracks[0].clips[0].notes[0].pitch===64`);
 await check('Later note edit remains undoable after restoring its generated creation',`document.querySelector('[data-id="${generatedNote}"]')!==null&&JSON.stringify((await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state)===${JSON.stringify(JSON.stringify(generatedStates[4]))}`);
 // Delete through the production editor, then attempt reuse through its
 // shared service. Rejected new edits must not obstruct real history undo.
 const beforeIdentityDelete=await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`);
 const generatedRect=await rect(`[data-id="${generatedNote}"]`);
 await mouse('mousePressed',generatedRect.x,generatedRect.y,{clickCount:1,modifiers:8});await mouse('mouseReleased',generatedRect.x,generatedRect.y,{clickCount:1,modifiers:8});
 await until(`document.querySelector('[data-id="${generatedNote}"][data-selected]')`);
 await keyboard('Delete','Delete');await until(`(await musicCall('GetProject',${JSON.stringify(titledProject.id)})).revision===${beforeIdentityDelete.revision+1}`);
 const afterIdentityDelete=await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`);
 await check('GUI deletion retires the note while retaining control events',`(await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state.tracks[0].clips[0].notes.length===0&&JSON.stringify((await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state.tracks[0].clips[0].controls)===${JSON.stringify(JSON.stringify(beforeIdentityDelete.state.tracks[0].clips[0].controls))}`);
 const reuseFailure=await ev(`(async()=>{try{await musicCall('SubmitProjectOps',${JSON.stringify(titledProject.id)},'browser-identity-reuse','undo',${afterIdentityDelete.revision},[{kind:'note.add',clipId:${JSON.stringify(generatedClip)},notes:[{id:${JSON.stringify(generatedNote)},start:0,duration:120,pitch:72,velocity:100,channel:0}]}]);return ''}catch(e){return String(e)}})()`);
 if(!reuseFailure.includes('已使用'))throw Error('Expected identity reuse rejection: '+reuseFailure);
 await check('Shared service rejects deleted-note reuse and spoofed restoration without a commit',`JSON.stringify(await musicCall('GetProject',${JSON.stringify(titledProject.id)}))===${JSON.stringify(JSON.stringify(afterIdentityDelete))}&&!(await musicCall('GetProjectTransaction',${JSON.stringify(titledProject.id)},'browser-identity-reuse'))[1]`);
 await click('撤销','.transport-bar');await until(`document.querySelector('[data-id="${generatedNote}"]')`);
 await check('GUI undo restores the original note identity after a rejected reuse',`JSON.stringify((await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state)===${JSON.stringify(JSON.stringify(beforeIdentityDelete.state))}`);
 const restoredIdentity=await ev(`musicCall('GetProject',${JSON.stringify(titledProject.id)})`);
 const payloadFailure=await ev(`(async()=>{try{await musicCall('SubmitProjectOps',${JSON.stringify(titledProject.id)},'browser-inverse-injection','gui',${restoredIdentity.revision},[{kind:'track.add',track:{id:'t_injected',channel:0,program:0},invClips:[{id:${JSON.stringify(generatedClip)},start:0,length:960,notes:[],controls:[],raw:[]}]}]);return ''}catch(e){return String(e)}})()`);
 if(!payloadFailure.includes('内部恢复载荷'))throw Error('Expected restoration payload rejection: '+payloadFailure);
 await check('Ordinary edits cannot inject internal restoration payloads',`JSON.stringify(await musicCall('GetProject',${JSON.stringify(titledProject.id)}))===${JSON.stringify(JSON.stringify(restoredIdentity))}&&!(await musicCall('GetProjectTransaction',${JSON.stringify(titledProject.id)},'browser-inverse-injection'))[1]`);
 await click('重做','.transport-bar');await until(`(await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state.tracks[0].clips[0].notes.length===0`);
 await click('撤销','.transport-bar');await until(`document.querySelector('[data-id="${generatedNote}"]')`);
 await check('Rejected new edits leave the existing redo and nested state intact',`JSON.stringify((await musicCall('GetProject',${JSON.stringify(titledProject.id)})).state)===${JSON.stringify(JSON.stringify(beforeIdentityDelete.state))}`);
 await ev(`window.getMusic=async()=>{const ps=await musicCall('ListProjects');return musicCall('GetProject',ps.find(p=>p.title==='浏览器真实事务').id)}`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.textContent.includes('浏览器真实事务')).click()`);
 await until('document.querySelector("[data-id=n_perf_10000_0]")');
 // A delayed read of A must not replace selected B; unmounting an active
 // drag must also remove vendor body listeners without committing its preview.
 await ev(`window.first=await getMusic()`);
 const switching=await rect('[data-id="n_perf_10000_0"]');
 await mouse('mousePressed',switching.x,switching.y,{clickCount:1});await mouse('mouseMoved',switching.x+24,switching.y,{buttons:1});
 await ev(`(async()=>{window.musicDelayID=first.id;window.second=await musicCall('CreateProject','另一个工程',480);await musicCall('SubmitProjectOps',first.id,'delay-notify','cli',first.revision,[{kind:'project.rename',title:'浏览器真实事务'}])})()`);
 await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.textContent.includes('另一个工程'))`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.textContent.includes('另一个工程')).click()`);
 await mouse('mouseReleased',switching.x+24,switching.y,{clickCount:1});await sleep(600);
 await check('Delayed project reads and drag unmount cannot retarget edits',`document.querySelector('.transport-title')?.textContent==='另一个工程' && !document.querySelector('gsui-pianoroll') && (await musicCall('GetProject',first.id)).revision===first.revision+1`);
 await ev(`(()=>{window.musicDelayID=null;Array.from(document.querySelectorAll('.project-select')).find(b=>b.textContent.includes('浏览器真实事务')).click()})()`);
 await until('document.querySelector("[data-id=n_perf_10000_0]")');
 await sleep(150);
 await check('Remounted editor centers on its first note',`(()=>{const n=document.querySelector('[data-id=n_perf_10000_0]').getBoundingClientRect(),a=document.querySelector('.gsuiTimewindow-scrollArea').getBoundingClientRect();return n.top>=a.top&&n.bottom<=a.bottom})()`);
 report.checks.push('React remount restores a single editor');
 if(await ev('document.querySelectorAll("gsui-pianoroll").length')!==1)throw Error('duplicate editors after remount');
 // Durable time-map drafts preserve raw invalid input and the original
 // project/revision. Recovery only opens a form; it never submits music.
 const draftBefore=await ev('getMusic()');
 await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','');await click('添加速度点','.time-map-dialog');
 const draftNewKey=await ev(`Number(Array.from(document.querySelectorAll('.time-map-dialog [aria-label^="速度位置 "]')).at(-1).getAttribute('aria-label').split(' ').at(-1))`);
 await fill(`[aria-label="速度位置 ${draftNewKey}"]`,'0');await fill(`[aria-label="速度 BPM ${draftNewKey}"]`,'73');
 await until(`(await musicCall('GetTimeMapDraft'))?.payload&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm===''&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo.at(-1).bpm==='73'`);
 await check('Invalid raw time-map rows are checkpointed without changing music',`(await getMusic()).revision===${draftBefore.revision}&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).baseRevision===${draftBefore.revision}`);
 await click('保留草稿并关闭','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")&&document.querySelector(".map-draft-recovery")');
 const draftReadInit=await send('Page.addScriptToEvaluateOnNewDocument',{source:'window.musicDraftReadFails=2'});
 const draftPageOrigin=await ev('performance.timeOrigin');await send('Page.reload');
 await until(`performance.timeOrigin!==${draftPageOrigin}&&window.musicCall&&document.querySelector('[data-close-guard=ready]')&&document.body.innerText.includes('test draft read failed')`);
 await check('Draft read failure disables time-map editing and keeps a retry entry',`Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='编辑速度与拍号').disabled&&!document.querySelector('.time-map-dialog')`);
 await ev(`musicCloseRequest('draft-read-failed')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Unfinished draft recovery check blocks native-close approval',`document.querySelector('.dialog-body').textContent.includes('草稿检查')&&!musicClose.confirmed.length`);
 await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("draft-read-failed")');
 await click('重试草稿检查');await until('document.querySelector(".map-draft-recovery")');await send('Page.removeScriptToEvaluateOnNewDocument',{identifier:draftReadInit.identifier});
 await ev(`window.getMusic=async()=>{const ps=await musicCall('ListProjects');return musicCall('GetProject',ps.find(p=>p.title==='浏览器真实事务').id)}`);
 await check('Reload offers the saved draft without opening or applying it',`!document.querySelector('.time-map-dialog')&&(await getMusic()).revision===${draftBefore.revision}&&JSON.stringify(JSON.parse((await musicCall('GetTimeMapDraft')).payload).tempo)===${JSON.stringify(JSON.stringify(draftBefore.state.tempo))}`);
 const draftOther=await ev(`musicCall('CreateProject','草稿隔离工程',480)`);
 await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.dataset.id===${JSON.stringify(draftOther.id)})`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(draftOther.id)}).click()`);
 await until('document.querySelector(".transport-title")?.textContent==="草稿隔离工程"');
 await check('A different selected project cannot replace the draft target',`JSON.parse((await musicCall('GetTimeMapDraft')).payload).projectID===${JSON.stringify(draftBefore.id)}&&Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='编辑速度与拍号').disabled`);
 await click('前往草稿工程','.map-draft-recovery');await until('document.querySelector(".transport-title")?.textContent==="浏览器真实事务"');await click('恢复时间图草稿');
 await check('Explicit recovery restores blank values row keys and the frozen base',`document.querySelector('[aria-label="速度 BPM 0"]').value===''&&document.querySelector('[aria-label="速度位置 ${draftNewKey}"]').value==='0'&&document.querySelector('.time-map-dialog').textContent.includes('基准修订 ${draftBefore.revision}')`);
 await ev(`musicCall('SubmitProjectOps',${JSON.stringify(draftBefore.id)},'draft-concurrent','cli',${draftBefore.revision},[{kind:'project.rename',title:'浏览器真实事务'}])`);
 await until('document.querySelector(".time-map-dialog").textContent.includes("工程已变为修订")');
 await fill('[aria-label="速度 BPM 0"]','121');await fill(`[aria-label="速度位置 ${draftNewKey}"]`,'2147483647');
 await click('应用时间图','.time-map-dialog');await until('document.querySelector(".time-map-dialog").textContent.includes("草稿已保留")');
 await check('Recovered stale draft rejects application without rebasing or losing its input',`(await getMusic()).revision===${draftBefore.revision+1}&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).baseRevision===${draftBefore.revision}&&document.querySelector('[aria-label="速度 BPM 0"]').value==='121'`);
 await click('关闭时间图','.time-map-dialog');await click('丢弃草稿','dialog[open]:not(.time-map-dialog)');await until('!document.querySelector(".time-map-dialog")');
 await check('Explicit discard clears draft content and retains a token barrier',`(await musicCall('GetTimeMapDraft')).payload===''&&!!(await musicCall('GetTimeMapDraft')).token`);
 // Failure and lost-reply paths use the same real Go file methods.
 await click('编辑速度与拍号');await ev('window.musicDraftSaveFails=true');await fill('[aria-label="速度 BPM 0"]','123');
 await until('document.querySelector(".map-checkpoint").textContent.includes("test draft save failed")');
 await ev(`musicCloseRequest('draft-not-durable')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Failed draft checkpoint blocks close approval and retains input',`!musicClose.confirmed.length&&document.querySelector('.dialog-body').textContent.includes('尚未保留')&&document.querySelector('[aria-label="速度 BPM 0"]').value==='123'`);
 await click('返回工程处理','dialog[open]:not(.time-map-dialog)');await until('musicClose.cancelled.includes("draft-not-durable")');
 await ev('window.musicDraftSaveFails=false');await click('重试保留草稿','.time-map-dialog');await until('document.querySelector(".map-checkpoint").textContent.includes("草稿已保留")');
 await check('Checkpoint retry persists the current draft without a music revision',`(await getMusic()).revision===${draftBefore.revision+1}&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm==='123'`);
 await ev('window.musicDraftHold=true');await fill('[aria-label="速度 BPM 0"]','125');await until('window.musicDraftRelease');await fill('[aria-label="速度 BPM 0"]','126');
 await ev(`musicCloseRequest('draft-in-flight')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await ev('(()=>{window.musicDraftHold=false;musicDraftRelease();delete window.musicDraftRelease})()');
 await click('返回工程处理','dialog[open]:not(.time-map-dialog)');await until('musicClose.cancelled.includes("draft-in-flight")&&document.querySelector(".map-checkpoint").textContent.includes("草稿已保留")');
 await check('Serial checkpoints preserve newer input after a delayed write without resuming quit',`JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm==='126'&&document.querySelector('[aria-label="速度 BPM 0"]').value==='126'&&!musicClose.confirmed.length`);
 await ev('window.musicDraftClearFails=true');await click('关闭时间图','.time-map-dialog');await click('丢弃草稿','dialog[open]:not(.time-map-dialog)');
 await until('document.querySelector(".time-map-dialog").textContent.includes("草稿清理失败")');
 await check('Discard failure keeps the form and the last persisted draft',`document.querySelector('[aria-label="速度 BPM 0"]').value==='126'&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm==='126'`);
 await ev('window.musicDraftClearFails=false');await click('关闭时间图','.time-map-dialog');await click('丢弃草稿','dialog[open]:not(.time-map-dialog)');await until('!document.querySelector(".time-map-dialog")');
 await click('编辑速度与拍号');await ev('(()=>{window.musicDraftLoseReply=true;window.musicDraftReadFails=1})()');await fill('[aria-label="速度 BPM 0"]','124');
 await until('document.querySelector(".map-checkpoint").textContent.includes("草稿尚未保留")');await click('重试保留草稿','.time-map-dialog');await until('document.querySelector(".map-checkpoint").textContent.includes("草稿已保留")');
 await check('Lost checkpoint reply and failed lookup retry the original token safely',`JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm==='124'&&(await getMusic()).revision===${draftBefore.revision+1}`);
 await ev('window.musicDraftClearFails=true');await click('应用时间图','.time-map-dialog');await until('document.querySelector(".time-map-dialog").textContent.includes("修改已保存，草稿清理失败")');
 await check('Committed music remains saved when draft cleanup fails and cannot be resubmitted',`(await getMusic()).revision===${draftBefore.revision+2}&&document.querySelector('[aria-label="速度 BPM 0"]').disabled&&Array.from(document.querySelectorAll('.time-map-dialog button')).find(b=>b.textContent==='应用时间图').disabled`);
 await ev('window.musicDraftClearFails=false');await click('关闭时间图','.time-map-dialog');await until('!document.querySelector(".time-map-dialog")');
 await check('Retrying committed draft cleanup does not send a second edit',`(await getMusic()).revision===${draftBefore.revision+2}&&(await musicCall('GetTimeMapDraft')).payload===''`);
 await click('撤销','.transport-bar');await until(`(await getMusic()).revision===${draftBefore.revision+3}`);
 const orphanDraft=await ev(`musicCall('CreateProject','孤立草稿工程',480)`);
 await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.dataset.id===${JSON.stringify(orphanDraft.id)})`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(orphanDraft.id)}).click()`);await until('document.querySelector(".transport-title")?.textContent==="孤立草稿工程"');
 await click('编辑速度与拍号');await ev('window.musicDraftHold=true');await fill('[aria-label="速度 BPM 0"]','88');await until('window.musicDraftRelease');await fill('[aria-label="速度 BPM 0"]','89');
 await ev(`musicCall('DeleteProject',${JSON.stringify(orphanDraft.id)})`);await until('!document.querySelector(".time-map-dialog")');
 await ev(`musicCloseRequest('unmounted-draft')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Unmounting a dialog retains its in-flight checkpoint as a global close blocker',`document.querySelector('.dialog-body').textContent.includes('草稿写入尚未确认')&&!musicClose.confirmed.length`);
 await ev('(()=>{window.musicDraftHold=false;musicDraftRelease();delete window.musicDraftRelease})()');await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("unmounted-draft")&&document.querySelector(".map-draft-recovery")?.textContent.includes("原工程已删除")');
 await check('Unmount flush coalesces the latest input into the original deleted-project draft',`JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm==='89'`);
 await check('Deleting a project preserves its draft without recreating music',`JSON.parse((await musicCall('GetTimeMapDraft')).payload).projectID===${JSON.stringify(orphanDraft.id)}&&!(await musicCall('ListProjects')).some(p=>p.id===${JSON.stringify(orphanDraft.id)})`);
 await click('丢弃已保留草稿','.map-draft-recovery');await click('丢弃草稿','dialog[open]');await until('!document.querySelector(".map-draft-recovery")');
 await check('Orphan draft can be explicitly discarded without restoring the deleted project',`(await musicCall('GetTimeMapDraft')).payload===''&&!(await musicCall('ListProjects')).some(p=>p.id===${JSON.stringify(orphanDraft.id)})`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(draftBefore.id)}).click()`);await until('document.querySelector("[data-id=n_perf_10000_0]")');
 // Large maps are edited in bounded pages without replacing hidden rows.
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'map-pages','cli',p.revision,[{kind:'tempo.set',tempo:Array.from({length:50},(_,i)=>({tick:i*960,usPerQuarter:500001+i}))}])})()`);
 await until('Array.from(document.querySelectorAll("button")).find(b=>b.textContent.trim()==="编辑速度与拍号").title.includes("50 个速度点")');
 await click('编辑速度与拍号');
 await check('Large time maps mount only 24 form rows per map page',`document.querySelectorAll('.time-map-dialog [aria-label^="速度 BPM "]').length===24`);
 await click('下页速度','.time-map-dialog');await click('下页速度','.time-map-dialog');await fill('[aria-label="速度 BPM 49"]','80');
 await ev(`document.querySelector('[aria-label="删除速度点 48"]').click()`);await click('应用时间图','.time-map-dialog');
 await until('!document.querySelector(".time-map-dialog")');
 await check('Paginated edits and point deletion preserve hidden rows and exact values',`(()=>getMusic().then(p=>p.state.tempo.length===49&&p.state.tempo[0].usPerQuarter===500001&&p.state.tempo[24].usPerQuarter===500025&&!p.state.tempo.some(t=>t.tick===48*960)&&p.state.tempo[48].tick===49*960&&p.state.tempo[48].usPerQuarter===750000))()`);
 await click('撤销','.transport-bar');await until('(await getMusic()).state.tempo.length===50');

 // Native close events/facade are simulated; editing still uses real Go/SQLite.
 const closeGesture=await rect('[data-id="n_perf_10000_0"]');await ev('window.gestureExitRevision=(await getMusic()).revision');
 await mouse('mousePressed',closeGesture.x,closeGesture.y,{clickCount:1});await mouse('mouseMoved',closeGesture.x+24,closeGesture.y,{buttons:1});
 await ev(`musicCloseRequest('gesture-active')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Active note gesture blocks exit before committing its preview',`document.querySelector('.dialog-body').textContent.includes('音符手势')&&!musicClose.confirmed.length`);
 await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("gesture-active")');
 await ev('document.querySelector("gsui-pianoroll").focus()');await keyboard('Escape','Escape');
 await mouse('mouseReleased',closeGesture.x+24,closeGesture.y,{clickCount:1});
 await until('(await getMusic()).revision===gestureExitRevision');
 // Earlier rejected/unknown input remains visible until explicitly discarded.
 while(await ev(`document.querySelector('[aria-label="已保留音乐输入"]')!==null`)) { await click('丢弃输入','[aria-label="已保留音乐输入"]');await click('丢弃输入','dialog[open]'); }
 // Music field checkpoints retain raw strings, original revisions and selection.
 await ev('window.inputBase=await getMusic()');await fill('[aria-label="片段长度（tick）"]','');await keyboard('Enter','Enter');
 await check('Blank numeric music input is retained without a zero edit',`(await getMusic()).revision===inputBase.revision&&JSON.parse(JSON.parse(localStorage.getItem('test.musicInputsRecord')).payload).entries[0].raw===''&&document.querySelector('[aria-label="片段长度（tick）"]').value===''`);
 const inputOrigin=await ev('performance.timeOrigin');await send('Page.reload');await until(`performance.timeOrigin!==${inputOrigin}&&document.querySelector('[aria-label="已保留音乐输入"]')&&document.querySelector('[data-close-guard=ready]')`);
 await ev(`window.getMusic=()=>musicCall('GetProject',localStorage.getItem('icetudio.lastProject'))`);
 await until('document.querySelector(\'[aria-label="片段长度（tick）"]\')');
 await check('Reload offers music input without automatically restoring or applying it',`document.querySelector('[aria-label="片段长度（tick）"]').disabled&&document.querySelector('[aria-label="片段长度（tick）"]').value!==''`);
 await click('恢复输入','[aria-label="已保留音乐输入"]');await until(`document.querySelector('[aria-label="片段长度（tick）"]').value===''`);
 await ev(`(async()=>{const p=await getMusic();await musicCall('SubmitProjectOps',p.id,'input-conflict','cli',p.revision,[{kind:'track.update',trackId:p.state.tracks[0].id,trackPatch:{name:'输入冻结检查'}}])})()`);await until('document.querySelector(".track-name").textContent==="输入冻结检查"');
 await fill('[aria-label="片段长度（tick）"]','1201200');await keyboard('Enter','Enter');await until('document.querySelector(".project-error")?.textContent.includes("刷新")');
 await check('Recovered numeric input keeps its base revision and refuses silent rebase',`JSON.parse(JSON.parse(localStorage.getItem('test.musicInputsRecord')).payload).entries[0].target.revision===(await getMusic()).revision-1&&document.querySelector('[aria-label="片段长度（tick）"]').value==='1201200'`);
 await click('丢弃输入','[aria-label="已保留音乐输入"]');await click('丢弃输入','dialog[open]');
 await click('全选本页');await fill('[aria-label="音符力度"]','');
 await check('Blank velocity is preserved and cannot submit an invalid MIDI value',`document.querySelector('[aria-label="音符力度"]').value===''&&Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='应用力度').disabled&&JSON.parse(JSON.parse(localStorage.getItem('test.musicInputsRecord')).payload).entries[0].noteIDs.length===64`);
 const velocityOrigin=await ev('performance.timeOrigin');await send('Page.reload');await until(`performance.timeOrigin!==${velocityOrigin}&&document.querySelector('[aria-label="已保留音乐输入"]')`);await ev(`window.getMusic=()=>musicCall('GetProject',localStorage.getItem('icetudio.lastProject'))`);await click('恢复输入','[aria-label="已保留音乐输入"]');await fill('[aria-label="音符力度"]','79');await click('应用力度');
 await until(`!document.querySelector('[aria-label="已保留音乐输入"]')`);
 await check('Explicit velocity recovery applies the original note IDs once with shared history',`(await getMusic()).state.tracks[0].clips[0].notes.slice(0,64).every(n=>n.velocity===79)`);
 await fill('[aria-label="循环起点"]','');
 await check('Playback draft preserves an empty raw tick without coercing it to zero',`JSON.parse(JSON.parse(JSON.parse(localStorage.getItem('test.musicInputsRecord')).payload).entries[0].raw).start===''`);
 await click('丢弃输入','[aria-label="已保留音乐输入"]');await click('丢弃输入','dialog[open]');
 await ev(`window.musicInputsSaveFails=true`);
 await fill('[aria-label="片段长度（tick）"]','');await ev(`musicCloseRequest('input-store-failure')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await check('Music input persistence failure retains memory and blocks native-close approval',`document.querySelector('.dialog-body').textContent.includes('音乐输入草稿尚未保存')&&!musicClose.confirmed.length`);
 await click('返回工程处理','dialog[open]');await ev('window.musicInputsSaveFails=false');await click('重试音乐输入保存/读取');await until(`localStorage.getItem('test.musicInputsRecord')&&!document.body.textContent.includes('test music inputs save failed')`);
 await check('Music input retry saves the original blank string without editing music',`JSON.parse(JSON.parse(localStorage.getItem('test.musicInputsRecord')).payload).entries[0].raw===''`);
 await click('丢弃输入','[aria-label="已保留音乐输入"]');await click('丢弃输入','dialog[open]');
 // Same-page dense notes: real service, clipped DOM, all-page selection/edits.
 const returnInputProject=await ev(`localStorage.getItem('icetudio.lastProject')`);
 const denseProject=await ev(`musicCall('CreateProject','密集音符性能',480)`);
 await until(`Array.from(document.querySelectorAll('.project-select')).some(b=>b.dataset.id===${JSON.stringify(denseProject.id)})`);
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(denseProject.id)}).click()`);await until('document.querySelector(".transport-title")?.textContent==="密集音符性能"&&document.querySelector(".music-editor")');await click('添加音轨');await until('document.querySelector("gsui-pianoroll")');
 for(const count of [1000,10000]) {
  await ev(`(()=>{window.denseFrames=[];window.denseCapture=true;let last=performance.now();const frame=t=>{if(!denseCapture)return;denseFrames.push(t-last);last=t;requestAnimationFrame(frame)};requestAnimationFrame(frame)})()`);
  const began=performance.now();
  await ev(`(async()=>{const p=await getMusic(),t=p.state.tracks[0],c=t.clips[0];await musicCall('SubmitProjectOps',p.id,'dense-'+${count},'cli',p.revision,[...(c.notes.length?[{kind:'note.remove',clipId:c.id,noteIds:c.notes.map(n=>n.id)}]:[]),{kind:'note.add',trackId:t.id,clipId:c.id,notes:Array.from({length:${count}},(_,i)=>({id:'n_dense_${count}_'+i,start:i%64*120,duration:120,pitch:48+i%36,velocity:100,channel:0}))}])})()`);
  await until(`document.body.textContent.includes('共 ${count} 个音符')`);await ev('new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))');
  const projected=await ev(`({projectionMs:Number(document.querySelector('gsui-pianoroll').dataset.projectionMs),mounted:document.querySelectorAll('.gsuiPianoroll-block').length,nodes:document.querySelectorAll('gsui-pianoroll *').length})`);
  const scrollBegan=performance.now();await ev(`(()=>{const a=document.querySelector('.gsuiTimewindow-scrollArea');a.scrollTop+=200;a.scrollLeft+=120})()`);await ev('new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))');
  const scrollMs=performance.now()-scrollBegan,selectBegan=performance.now();await click('全选本页');await ev('new Promise(requestAnimationFrame)');const selectMs=performance.now()-selectBegan;
  await check(`Dense ${count} all-page selection includes notes outside the mounted viewport`,`document.body.textContent.includes('${count} 个已选')`);
  if(count===1000) {
   await check('Dense note selection displays without per-note transitions',`(()=>{const n=document.querySelector('.piano-host .gsuiBlocksManager-block[data-selected]');return !!n&&getComputedStyle(n).transitionDuration==='0s'})()`);
   await check('Selected velocity slider retains the correct stacking order',`(()=>{const s=document.querySelector('.gsuiSliderGroup-sliderSelected');return !!s&&Number(s.style.zIndex)===Math.floor(parseFloat(s.style.left)*100)+1000})()`);
   await click('取消选择');await until(`document.body.textContent.includes('0 个已选')`);
   await check('Deselected velocity slider restores its stacking order',`(()=>{const s=document.querySelector('.gsuiSliderGroup-slider');return !!s&&!s.classList.contains('gsuiSliderGroup-sliderSelected')&&Number(s.style.zIndex)===Math.floor(parseFloat(s.style.left)*100)})()`);
   await click('全选本页');await until(`document.body.textContent.includes('1000 个已选')`);
  }
  const profileDense=process.env.ICETUDIO_PROFILE_DENSE==='1'&&count===10000;
  let traceDone;
  if(profileDense){
   traceDone=new Promise(resolve=>{const listener=e=>{const m=JSON.parse(e.data);if(m.method==='Tracing.tracingComplete'){ws.removeEventListener('message',listener);resolve(m.params.stream)}};ws.addEventListener('message',listener)});
   await send('Profiler.enable');await send('Profiler.start');await send('Tracing.start',{categories:'devtools.timeline,v8',transferMode:'ReturnAsStream'});
  }
  const editBegan=performance.now();await fill('[aria-label="音符力度"]','99');await click('应用力度');await until(`(await getMusic()).state.tracks[0].clips[0].notes.every(n=>n.velocity===99)&&!document.querySelector('gsui-pianoroll[disabled]')`);
  if(profileDense){
   await send('Tracing.end');const stream=await traceDone;let trace='';for(;;){const part=await send('IO.read',{handle:stream});trace+=part.data;if(part.eof)break}await send('IO.close',{handle:stream});writeFileSync(resolve(dir,'dense-edit-trace.json'),trace);
   const profile=await send('Profiler.stop');writeFileSync(resolve(dir,'dense-edit-profile.json'),JSON.stringify(profile.profile));await send('Profiler.disable');
  }
  await check(`Dense ${count} edit keeps every source note and updates offscreen notes`,`(await getMusic()).state.tracks[0].clips[0].notes.length===${count}`);
  const frames=await ev(`(()=>{window.denseCapture=false;const v=denseFrames.slice().sort((a,b)=>a-b);return {max:v.at(-1),p95:v[Math.floor(v.length*.95)],samples:v.length}})()`);
  report.timings.push({shape:'same-page-dense',count,...projected,commitAndInteractionMs:performance.now()-began,scrollMs,selectMs,editToVisibleMs:performance.now()-editBegan,frameGaps:frames});
  if(projected.projectionMs> (count===1000?200:500))throw Error('dense projection budget exceeded '+JSON.stringify(projected));
 }
 await ev(`Array.from(document.querySelectorAll('.project-select')).find(b=>b.dataset.id===${JSON.stringify(returnInputProject)}).click()`);await until('document.querySelector(".transport-title")?.textContent==="浏览器真实事务"&&document.querySelector("[data-id=n_perf_10000_0]")');
 await ev('window.musicHoldSave=true');await fill('[aria-label="片段起点（tick）"]','240');
 await ev(`musicCloseRequest('number-blur')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"&&window.musicReleaseSave');
 await check('Native close blurs numeric input and waits for its original commit',`(await getMusic()).state.tracks[0].clips[0].start===240&&!musicClose.confirmed.length`);
 await ev('(()=>{window.musicHoldSave=false;musicReleaseSave();delete window.musicReleaseSave})()');
 await click('返回工程处理','dialog[open]');await until('musicClose.cancelled.includes("number-blur")&&!document.querySelector(".piano-host gsui-pianoroll[disabled]")');
 await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','101');
 await until('document.querySelector(".map-checkpoint").textContent.includes("草稿已保留")');
 await ev(`(()=>{musicCloseRequest('dirty-cancel');musicCloseRequest('dirty-cancel')})()`);
 await until('document.querySelector("#dialog-title")?.textContent==="保留音乐编辑草稿并退出？"');
 await check('Repeated close requests show one preserve-draft dialog and disable editor input',`document.querySelectorAll('#dialog-title').length===1&&document.querySelector('[data-close-guard]').hasAttribute('inert')&&document.querySelector('[aria-label="速度 BPM 0"]').disabled&&!musicClose.confirmed.length`);
 await click('继续编辑','dialog.app-dialog:not(.time-map-dialog)[open]');await until('musicClose.cancelled.includes("dirty-cancel")');
 await check('Cancelled native exit retains time-map draft and frozen revision',`document.querySelector('[aria-label="速度 BPM 0"]').value==='101'&&document.querySelector('.time-map-dialog[open]')&&!document.querySelector('[data-close-guard]').hasAttribute('inert')`);
 await ev(`musicCloseRequest('draft-changed')`);await until('document.querySelector("#dialog-title")?.textContent==="保留音乐编辑草稿并退出？"');
 // Simulate a late input callback, which inert alone does not prevent.
 await fill('[aria-label="速度 BPM 0"]','102');
 await click('保留草稿并退出','dialog.app-dialog:not(.time-map-dialog)[open]');
 await until('document.querySelector("#dialog-title")?.textContent==="编辑状态已经变化"');
 await click('返回检查','dialog.app-dialog:not(.time-map-dialog)[open]');await until('musicClose.cancelled.includes("draft-changed")');
 await check('Preserve-exit confirmation cannot cover a later draft version',`!musicClose.confirmed.length&&document.querySelector('[aria-label="速度 BPM 0"]').value==='102'`);
 await ev('window.musicHoldSave=true');await click('应用时间图','.time-map-dialog');await until('window.musicReleaseSave');
 await ev(`musicCloseRequest('save-in-flight')`);await until('document.querySelector("#dialog-title")?.textContent==="工程操作尚未确认"');
 await ev('(()=>{window.musicHoldSave=false;musicReleaseSave()})()');
 await click('返回工程处理','dialog.app-dialog:not(.time-map-dialog)[open]');await until('musicClose.cancelled.includes("save-in-flight")&&!document.querySelector(".time-map-dialog")');
 await check('In-flight project save blocks exit; completion never silently resumes it',`!musicClose.confirmed.length&&(await getMusic()).state.tempo[0].usPerQuarter===588235`);
 await click('编辑速度与拍号');await fill('[aria-label="速度 BPM 0"]','103');
 await until('document.querySelector(".map-checkpoint").textContent.includes("草稿已保留")');
 await ev(`(()=>{window.musicCloseFailure=true;musicCloseRequest('quit-failure')})()`);await until('document.querySelector("#dialog-title")');
 await click('保留草稿并退出','dialog.app-dialog:not(.time-map-dialog)[open]');await until('musicClose.cancelled.includes("quit-failure")');
 await check('Quit transport failure leaves the draft and permits retry',`document.querySelector('.close-guard-status[role=alert]').textContent.includes('test close reply failed')&&!document.querySelector('[data-close-guard]').hasAttribute('inert')&&document.querySelector('[aria-label="速度 BPM 0"]').value==='103'`);
 await ev(`(async()=>{window.musicCloseFailure=false;window.beforeExit=(await getMusic()).revision;musicCloseRequest('dirty-discard')})()`);await until('document.querySelector("#dialog-title")');
 await click('保留草稿并退出','dialog.app-dialog:not(.time-map-dialog)[open]');await until('musicClose.confirmed.includes("dirty-discard")');
 await check('Explicit preserve permits exit without submitting draft or enabling new edits',`(await getMusic()).revision===beforeExit&&document.querySelector('[data-close-guard]').hasAttribute('inert')&&document.querySelector('[aria-label="速度 BPM 0"]').disabled&&JSON.parse((await musicCall('GetTimeMapDraft')).payload).draft.tempo[0].bpm==='103'`);
 await send('Page.reload');await until('window.musicCloseRequest&&document.querySelector("[data-close-guard=ready]")');
 await ev(`musicCloseRequest('clean-exit')`);await until('musicClose.confirmed.includes("clean-exit")');
 await check('Clean editor exits without a discard prompt after checks initialize',`!document.querySelector('#dialog-title')&&document.querySelector('[data-close-guard]').hasAttribute('inert')`);
 const shot=await send('Page.captureScreenshot');writeFileSync(resolve(dir,'workspace.png'),Buffer.from(shot.data,'base64'));
 if(report.errors.length)throw Error('Browser errors: '+report.errors.join('\n'));
 console.log(JSON.stringify(report,null,2));
}catch(e){report.failure=String(e);try{report.body=await debug?.()}catch{}console.error(JSON.stringify(report,null,2));process.exitCode=1}
finally{writeFileSync(resolve(dir,'results.json'),JSON.stringify(report,null,2));ws?.close();for(const child of [vite,chrome]){try{process.kill(-child.pid,'SIGTERM')}catch{}}}
