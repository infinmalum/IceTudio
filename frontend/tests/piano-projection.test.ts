import test from "node:test";
import assert from "node:assert/strict";
import { pageNotes,ghostNotes,gridNote } from "../src/music/pianoProjection.ts";

const n={id:"n1",start:0,duration:480,pitch:60,velocity:90,channel:0};
test("authoritative paging retains onset semantics and exact PPQ mapping",()=>{
  const notes=[n,{...n,id:"n2",start:16*480},{...n,id:"n3",start:16*480-1,duration:960}];
  assert.deepEqual(pageNotes(notes,1,480).map(n=>n.id),["n2"]);
  assert.equal(gridNote(notes[1],16*480,480,true).when,0);
  assert.equal(gridNote(notes[1],16*480,480,true).selected,true);
});
test("ghosts clip sustained notes at the page edge without creating editable notes",()=>{
  const change={trackId:"t1",trackName:"钢琴",clipId:"c1",clipName:"片段",clipStart:500,before:{...n,start:16*480-240},after:{...n,start:16*480-240,pitch:62}};
  const ghosts=ghostNotes([change],"c1",1,480);
  assert.equal(ghosts.length,2);assert.equal(ghosts[0].when,0);assert.equal(ghosts[0].duration,.5);
  assert.equal(change.before.start,16*480-240);
  assert.equal(ghostNotes([change],"other",1,480).length,0);
  assert.equal(ghostNotes([change],"c1",1,480,{pitches:new Set([62]),left:16*480,right:17*480}).length,1);
});
