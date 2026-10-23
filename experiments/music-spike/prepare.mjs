import {execFileSync} from 'node:child_process';
import {existsSync,mkdirSync} from 'node:fs';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'../..');
const cache=resolve(root,'.cache/music-spike');
mkdirSync(cache,{recursive:true});
for(const [name,sha] of [
 ['gs-ui-components','c45632a4b6fbc38af87a6e68058bdb9d2c444242'],
 ['webaudio-pianoroll','523bc10e4b7e5d0a70b20caaeff5c14e4864c9de'],
]){
 const dir=resolve(cache,name);
 if(!existsSync(dir)){
  execFileSync('git',['clone',`https://github.com/${name==='gs-ui-components'?'gridsound':'g200kg'}/${name}`,dir],{stdio:'inherit'});
  execFileSync('git',['checkout','--detach',sha],{cwd:dir,stdio:'inherit'});
 }
 const actual=execFileSync('git',['rev-parse','HEAD'],{cwd:dir,encoding:'utf8'}).trim();
 if(actual!==sha)throw Error(`${name}: expected ${sha}, found ${actual}; use a separate cache checkout`);
}
execFileSync('git',['submodule','update','--init','--depth','1','gs-utils'],{cwd:resolve(cache,'gs-ui-components'),stdio:'inherit'});
execFileSync('npm',['install','--prefix',cache,'--save-exact','react@18.2.0','react-dom@18.2.0'],{stdio:'inherit'});
