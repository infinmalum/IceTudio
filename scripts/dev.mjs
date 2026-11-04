// Development builds use bin/, never go run's temporary executable directory,
// so the normal adjacent application data directory remains persistent.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
const root=fileURLToPath(new URL('../',import.meta.url));
const npm=process.platform==='win32'?'npm.cmd':'npm';
const binary=resolve(root,'bin',process.platform==='win32'?'icetudio.exe':'icetudio');
for(const [program,args] of [[npm,['--prefix','frontend','run','build']],['go',['build','-o',binary,'.']],[binary,[]]]){
 const result=spawnSync(program,args,{cwd:root,stdio:'inherit',env:process.env});
 if(result.error)throw result.error;if(result.status!==0)process.exit(result.status??1);
}
