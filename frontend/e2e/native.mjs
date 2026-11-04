// Actual MyGo/WebKit windows on disposable data, including a local model
// fixture, approvals, bulk offscreen edits and recovery. No cloud credentials.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../../',import.meta.url));
const env={...process.env,ICETUDIO_MYGO_NATIVE:'1'};
for(const args of [['build','-o','bin/icetudio-mygo','./cmd/icetudio-mygo'],['test','./internal/desktop','-run','^(TestMyGoNative|TestActualWebKitProfile)','-count=1','-v']]){
 const r=spawnSync('go',args,{cwd:root,stdio:'inherit',env});if(r.error)throw r.error;if(r.status!==0)process.exit(r.status??1);
}
