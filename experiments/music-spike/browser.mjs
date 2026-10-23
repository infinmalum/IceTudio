import {readFileSync, writeFileSync, mkdirSync, existsSync} from 'node:fs';
import {resolve, dirname, extname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createServer} from 'node:http';
import {spawn} from 'node:child_process';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'../..');
const cache=resolve(root,'.cache/music-spike');
const gs=resolve(cache,'gs-ui-components');
const selected=new Set(['gs-utils','gsui0ne','gsuiBeatlines','gsuiBlocksManager','gsuiDragline','gsuiKeys','gsuiPropSelect','gsuiScrollShadow','gsuiSlider','gsuiSliderGroup','gsuiStepSelect','gsuiTimeline','gsuiTimewindow','gsuiTimeCursors','gsuiIcon','gsuiCursor']);
const html=readFileSync(resolve(gs,'try.html'),'utf8');
const tags=[...html.matchAll(/<(?:script src|link rel="stylesheet" href)="([^"]+)"[^>]*>(?:<\/script>)?/g)]
 .map(m=>({tag:m[0],path:m[1]})).filter(x=>existsSync(resolve(gs,x.path))&&(selected.has(x.path.split('/')[0])||!x.path.includes('/')));
// Load pianoroll last: its class instantiates other custom elements.
for(const p of ['gsuiDragline/gsuiDragline.css','gsuiDragline/gsuiDragline.html.js','gsuiDragline/gsuiDragline.js','gsuiPianoroll/gsuiPianoroll.css','gsuiPianoroll/gsuiPianoroll-block.css','gsuiPianoroll/gsuiPianoroll.html.js','gsuiPianoroll/gsuiPianoroll.js']) tags.push({path:p,tag:p.endsWith('.css')?`<link rel="stylesheet" href="${p}">`:`<script src="${p}"></script>`});
const setup=`
window.calls=[];window.historyStack=[];window.revision=0;
window.data={n1:{key:60,when:1,duration:1,gain:.8,pan:0,lowpass:1,highpass:1,selected:false}};
window.p=document.createElement('gsui-pianoroll');p.style.cssText='display:block;width:1000px;height:500px';document.getElementById('mount').append(p);
p.$octaves(3,4);p.$timedivision('4/4');p.$changeDuration(16);p.$setData(data);p.$addKey('n1',data.n1);
window.apply=(id,patch,base=revision)=>{if(base!==revision)throw Error('revision conflict');historyStack.push(structuredClone(data));Object.assign(data[id],patch);for(const [k,v] of Object.entries(patch))p.$changeKeyProp(id,k,v);revision++};
window.undo=()=>{const prev=historyStack.pop();for(const [id,n] of Object.entries(prev)){Object.assign(data[id],n);for(const [k,v]of Object.entries(n))p.$changeKeyProp(id,k,v)}revision++};
p.$setCallbacks((type,...args)=>{calls.push([type,...args]);if(type==='move'){for(const id of args[0])apply(id,{when:data[id].when+args[1],key:data[id].key-args[2]})}else if(type==='cropEnd'){for(const id of args[0])apply(id,{duration:data[id].duration+args[1]})}});
window.ready=true;
`;
const page=`<!doctype html><meta charset="utf-8"><body data-skin="gray">${tags.map(x=>x.tag.replaceAll('="'+x.path+'"','="/gs-ui-components/'+x.path+'"')).join('\n')}<script src="/node_modules/react/umd/react.development.js"></script><script src="/node_modules/react-dom/umd/react-dom.development.js"></script><div id="mount"></div><script>function Adapter(){React.useEffect(()=>{${setup}\nreturn ()=>{p.remove()};},[]);return null}window.reactRoot=ReactDOM.createRoot(document.getElementById('mount'));reactRoot.render(React.createElement(Adapter));</script>`;
const web=`<!doctype html><body><script src="/webaudio-pianoroll/webaudio-pianoroll.js"></script><webaudio-pianoroll id="p" width="1000" height="500" editmode="dragpoly" timebase="480" xrange="1920" yoffset="48" yrange="36"></webaudio-pianoroll><script>window.p=document.getElementById('p');window.events=[];for(const name of ['change','input'])p.addEventListener(name,e=>events.push(e.type));p.sequence=[{id:'n1',t:480,g:480,n:60,v:100,f:0}];p.redraw();window.ready=true;</script>`;
const server=createServer((req,res)=>{try{if(req.url==='/gs'){res.setHeader('Content-Type','text/html');res.end(page);return}if(req.url==='/web'){res.setHeader('Content-Type','text/html');res.end(web);return}const path=resolve(cache,'.'+decodeURIComponent(req.url.split('?')[0]));if(!path.startsWith(cache+'/'))throw Error('path');res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.svg':'image/svg+xml'})[extname(path)]||'application/octet-stream');res.end(readFileSync(path))}catch{res.statusCode=404;res.end()}});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const port=server.address().port;mkdirSync(resolve(cache,'chrome'),{recursive:true});
const chrome=spawn('chromium',['--headless','--no-sandbox','--disable-gpu','--disable-background-networking','--no-first-run',`--user-data-dir=${resolve(cache,'chrome')}`,'--remote-debugging-port=9398','--window-size=1280,900','about:blank'],{stdio:'ignore'});
const sleep=ms=>new Promise(r=>setTimeout(r,ms));let ws;const report={scripts:tags.filter(t=>t.path.endsWith('.js')).map(t=>t.path),results:[],errors:[]};
try{
 let targets;for(let i=0;i<100;i++){try{targets=await(await fetch('http://127.0.0.1:9398/json/list')).json();break}catch{await sleep(100)}}
 ws=new WebSocket(targets.find(t=>t.type==='page').webSocketDebuggerUrl);await new Promise(r=>ws.onopen=r);
 let seq=0;const pending=new Map();ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id){const q=pending.get(m.id);pending.delete(m.id);clearTimeout(q.timer);m.error?q.reject(Error(JSON.stringify(m.error))):q.resolve(m.result)}else if(m.method==='Runtime.exceptionThrown')report.errors.push(m.params.exceptionDetails.exception?.description||m.params.exceptionDetails.text)};
 const send=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>reject(Error('timeout '+method)),15000);pending.set(id,{resolve,reject,timer});ws.send(JSON.stringify({id,method,params}))});
 const ev=async expression=>{const v=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(v.exceptionDetails)throw Error(v.exceptionDetails.exception?.description||v.exceptionDetails.text);return v.result.value};
 const check=(name,ok,detail)=>{report.results.push({name,ok,detail});if(!ok)throw Error(name+': '+JSON.stringify(detail))};
 const drag=async(x,y,dx,dy=0)=>{await send('Input.dispatchMouseEvent',{type:'mouseMoved',x,y});await send('Input.dispatchMouseEvent',{type:'mousePressed',x,y,button:'left',clickCount:1});for(let i=1;i<=5;i++)await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:x+dx*i/5,y:y+dy*i/5,buttons:1,button:'left'});await send('Input.dispatchMouseEvent',{type:'mouseReleased',x:x+dx,y:y+dy,button:'left',clickCount:1});await sleep(100)};
 await send('Runtime.enable');await send('Page.enable');
 await send('Page.navigate',{url:`http://127.0.0.1:${port}/gs`});await sleep(800);
 check('GridSound mounts',await ev('window.ready===true'),report.errors);
 await ev('p.querySelector("[data-id=n1]").scrollIntoView({block:"center"})');await sleep(100);
 const rect=await ev(`(()=>{const r=p.querySelector('[data-id="n1"]').getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,hit:document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)?.outerHTML}})()`);report.rect=rect;
 await drag(rect.x+rect.w/2,rect.y+rect.h/2,64);
 let state=await ev('({data,calls,revision})');check('GridSound real mouse drag emits command',state.calls.some(c=>c[0]==='move')&&state.data.n1.when===2,state);
 await ev('undo()');check('GridSound host undo',await ev('data.n1.when===1 && p.querySelector("[data-id=n1]").dataset.when==="1"'));
 await ev('apply("n1",{key:64,when:3})');check('GridSound external edit visible',await ev('p.querySelector("[data-id=n1]").dataset.keyNote==="64" && p.querySelector("[data-id=n1]").dataset.when==="3"'));
 check('Host stale revision rejected',await ev('(()=>{try{apply("n1",{when:99},0);return false}catch{return data.n1.when===3}})()'));
 await ev('undo()');check('GridSound external edit undo',await ev('data.n1.key===60&&data.n1.when===1'));
 const resize=await ev(`(()=>{const r=p.querySelector('[data-id=n1] [data-action=cropB]').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
 await drag(resize.x,resize.y,64);check('GridSound real resize emits command',await ev('data.n1.duration===2&&calls.some(c=>c[0]==="cropEnd")'));await ev('undo()');
 const gsShot=await send('Page.captureScreenshot');writeFileSync(resolve(cache,'gridsound.png'),Buffer.from(gsShot.data,'base64'));
 // Bounded batch timings are diagnostic, not an FPS benchmark.
 for(const count of [1000,10000]){const m=await ev(`(()=>{const start=performance.now();for(let i=0;i<${count};i++){const id='b'+i;const n={...data.n1,when:i/4,key:48+i%36};data[id]=n;p.$addKey(id,n)}const insert=performance.now()-start;const u=performance.now();for(let i=0;i<${count};i++)p.$changeKeyProp('b'+i,'when',i/4+.25);const update=performance.now()-u;const nodes=p.querySelectorAll('*').length;for(let i=0;i<${count};i++){p.$removeKey('b'+i);delete data['b'+i]}return {count:${count},insertMs:insert,updateMs:update,nodes}})()`);report.results.push({name:'GridSound batch timing',...m})}
 await ev('reactRoot.unmount()');check('React unmount removes editor',await ev('document.querySelectorAll("gsui-pianoroll").length===0'));
 await ev('window.ready=false;reactRoot=ReactDOM.createRoot(document.getElementById("mount"));reactRoot.render(React.createElement(Adapter))');await sleep(200);check('React remount restores one editor',await ev('ready&&document.querySelectorAll("gsui-pianoroll").length===1'));
 await send('Page.navigate',{url:`http://127.0.0.1:${port}/web`});await sleep(500);
 check('webaudio mounts',await ev('window.ready===true'));
 const wr=await ev(`(()=>{const r=p.canvas.getBoundingClientRect();return {x:r.x+p.yruler+p.kbwidth+(480-p.xoffset)*p.stepw+30,y:r.y+p.height-(60-p.yoffset+.5)*p.steph,stepw:p.stepw}})()`);
 await drag(wr.x,wr.y,0);await drag(wr.x,wr.y,wr.stepw*120);
 state=await ev('({sequence:p.sequence,events})');check('webaudio mutates notes on real drag',state.sequence[0].t!==480,state);
 check('webaudio edit has no change/input notification',state.events.length===0,state);
 await ev('p.sequence=[{id:"n1",t:480,g:480,n:60,v:100,f:0}];p.redraw()');check('webaudio external snapshot restore',await ev('p.sequence[0].t===480&&p.sequence[0].id==="n1"'));
 const shot=await send('Page.captureScreenshot');writeFileSync(resolve(cache,'webaudio.png'),Buffer.from(shot.data,'base64'));
}catch(e){report.failure=String(e);process.exitCode=1}finally{writeFileSync(resolve(cache,'browser-results.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));ws?.close();chrome.kill();server.close()}
