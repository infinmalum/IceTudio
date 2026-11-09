import test from 'node:test';
import assert from 'node:assert/strict';
import {intersectNotes,boxSelection,copyNotes,pasteNotes,coincidentNotes} from '../src/music/pianoViewport.ts';
import {gridNote} from '../src/music/pianoProjection.ts';
const n={id:'sustain',start:0,duration:480*1024,pitch:60,velocity:90,channel:0};
test('continuous window retains sustained note and all-ID intersection selection',()=>{
 const notes=[n,{...n,id:'offscreen',start:480*2000,duration:240,pitch:64}];
 assert.deepEqual(intersectNotes(notes,480*256,480*512).map(n=>n.id),['sustain']);
 const visible=gridNote(n,480*256,480,true);assert.equal(visible.when,0);assert.equal(visible.duration,768);assert.equal(n.duration,480*1024);
 assert.deepEqual(boxSelection(notes,480*2001,480*2000-1,64,60),['offscreen']);
 assert.deepEqual(boxSelection(notes,480*2000-1,480*2001,60,64),['offscreen']);
});
test('clipboard is an immutable explicit snapshot, scales PPQ and extends atomically',()=>{
 const target:any={id:'p',revision:7,ppq:480,track:{id:'t',channel:1},clip:{id:'c',length:960,notes:[{...n,duration:240},{...n,id:'b',start:480,duration:480,pitch:64}]}};
 const clipboard=copyNotes(target,['sustain','b'])!;target.clip.notes[0].pitch=1;
 const destination={...target,ppq:960,track:{id:'other',channel:4}};
 const ops=pasteNotes(destination,clipboard,1920);assert.equal(ops.length,2);assert.equal(ops[0].kind,'clip.update');assert.equal(ops[0].clipPatch?.length,3840);
 assert.equal(ops[1].notes?.[0].pitch,60);assert.equal(ops[1].notes?.[0].channel,0);assert.equal(ops[1].notes?.[0].duration,480);assert.equal(ops[1].notes?.[1].start,2880);
 assert.throws(()=>pasteNotes(destination,clipboard,2147483647));assert.throws(()=>pasteNotes(destination,clipboard,-1));
});

test('coincident glyphs retain distinct selection states and all original IDs',()=>{
 const notes=[n,{...n,id:'other'},{...n,id:'velocity',velocity:50}];
 const grouped=coincidentNotes(notes,new Set());assert.equal(grouped.length,2);assert.equal(grouped[0].count,2);assert.equal(grouped[0].note.id,'other');
 assert.equal(coincidentNotes(notes,new Set(['sustain'])).length,3);
 assert.deepEqual(boxSelection(notes,0,480,60,60),['sustain','other','velocity']);assert.equal(notes.length,3);
});
