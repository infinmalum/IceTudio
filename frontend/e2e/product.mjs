// Ordinary UI is Native. Run real Native controls/application-service tests;
// this is intentionally not presented as a browser or physical desktop test.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../../',import.meta.url));
const result=spawnSync('go',['test','./internal/desktop','-run','^Test(Native|Piano|TimeMap|MusicDraft|Arrangement)','-count=1'],{cwd:root,stdio:'inherit',env:process.env});
if(result.error)throw result.error;
process.exit(result.status??1);
