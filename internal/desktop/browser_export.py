#!/usr/bin/env python3
"""One-shot WebKit reader of an isolated profile COPY, never the source.
No Wails framework, service bridge, model connection or external page is loaded.
Output contains private draft data: caller must keep stdout off logs.
"""
import json, os, sys
import gi
gi.require_version('Gtk', '4.0')
gi.require_version('WebKit', '6.0')
from gi.repository import Gtk, WebKit, GLib, Gio

profile, cache = sys.argv[1:3]
GLib.set_prgname('icetudio')
Gtk.init()
session = WebKit.NetworkSession.new(profile, cache)
context = WebKit.WebContext.new()
def serve(request):
    body = GLib.Bytes.new(b'<!doctype html><meta charset="utf-8"><title>Local draft recovery</title>')
    request.finish(Gio.MemoryInputStream.new_from_bytes(body), body.get_size(), 'text/html')
context.register_uri_scheme('wails', serve)
view = WebKit.WebView(web_context=context, network_session=session)
window = Gtk.Window()
window.set_default_size(320, 180)
window.set_child(view)
loop = GLib.MainLoop()
status = [1]

def complete(view, result, _):
    try:
        value = view.call_async_javascript_function_finish(result)
        output = value.to_string()
        parsed = json.loads(output)
        if parsed.get('version') != 1: raise ValueError('invalid export')
        print(output, flush=True)
        status[0] = 0
    except Exception as e:
        print('Draft export failed: ' + str(e), file=sys.stderr)
    loop.quit()

script = r'''
const text = {}, preferences = {};
for(let i=0;i<localStorage.length;i++) {
 const k=localStorage.key(i),v=localStorage.getItem(k);
 if(k.startsWith('icetudio.draft.')) text[k.slice('icetudio.draft.'.length)]=v;
 else if(k.startsWith('icetudio.')) preferences[k]=v;
}
const databases=await indexedDB.databases();
let attachments={};
if(databases.some(d=>d.name==='icetudio-drafts')) {
 const db=await new Promise((resolve,reject)=>{const r=indexedDB.open('icetudio-drafts');r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(r.error);r.onblocked=()=>reject(Error('attachment store blocked'));r.onupgradeneeded=()=>{r.transaction.abort();reject(Error('unexpected attachment schema'))}});
 if(!db.objectStoreNames.contains('attachments'))throw Error('missing attachment store');
 const entries=await new Promise((resolve,reject)=>{const tx=db.transaction('attachments','readonly'),s=tx.objectStore('attachments'),keys=s.getAllKeys(),values=s.getAll();tx.oncomplete=()=>resolve(keys.result.map((k,i)=>[k,values.result[i]]));tx.onerror=()=>reject(tx.error);tx.onabort=()=>reject(tx.error||Error('aborted'))});
 let total=0;
 for(const [key,files] of entries){
  if(!Array.isArray(files))throw Error('invalid attachment record');
  attachments[key]=[];
  for(const f of files){if(!(f instanceof Blob)||typeof f.name!=='string')throw Error('invalid attachment');if(f.size>10*1024*1024)throw Error('attachment too large');total+=f.size;if(total>64*1024*1024)throw Error('export too large');const bytes=new Uint8Array(await f.arrayBuffer());let s='';for(let i=0;i<bytes.length;i+=8192)s+=String.fromCharCode(...bytes.subarray(i,i+8192));attachments[key].push({name:f.name,data:btoa(s)})}
 }
 db.close();
}
return JSON.stringify({version:1,text,attachments,preferences});
'''
def loaded(view, event):
    if event == WebKit.LoadEvent.FINISHED:
        view.call_async_javascript_function(script, -1, None, None, None, None, complete, None)
view.connect('load-changed', loaded)
def timeout():
    print('Draft export timed out; source profile is unchanged', file=sys.stderr)
    loop.quit()
    return False
GLib.timeout_add_seconds(25, timeout)
window.present()
view.load_uri('wails://localhost/')
loop.run()
window.destroy()
sys.exit(status[0])
