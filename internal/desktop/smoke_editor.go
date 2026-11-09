package desktop

import (
	"fmt"
	"icetudio/internal/project"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// E1 only: all musical writes target a new disposable project. Restore the
// original selection afterward so the existing migration probe stays intact.
func (a *Workspace) verifyEditorWorkspace(dir string) error {
	original := a.preview.Context()
	stage := func(s string) {
		v, _ := a.piano.Page().Eval("JSON.stringify({...document.documentElement.dataset})")
		f, _ := os.OpenFile(filepath.Join(dir, "editor-stage.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if f != nil {
			fmt.Fprintf(f, "%s: %v\n", s, v)
			f.Close()
		}
	}
	stage("start")
	p, err := a.service.CreateProject("E1 验证", 480)
	if err != nil {
		return err
	}
	// Use supported atomic add operations rather than replacing storage state.
	result, err := a.service.SubmitProjectOps(p.ID, project.NewID("e1"), "gui", p.Revision, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "e1_track", Name: "长音与远端"}}, {Kind: "clip.add", Clip: &project.ClipSpec{ID: "e1_clip", TrackID: "e1_track", Name: "连续片段", Length: 480 * 1024}}, {Kind: "note.add", ClipID: "e1_clip", Notes: []project.NewNote{{ID: "e1_long", Duration: 480 * 300, Pitch: 60, Velocity: 90}, {ID: "e1_far", Start: 480 * 260, Duration: 240, Pitch: 64, Velocity: 80}, {ID: "e1_edge", Start: 480*16 - 120, Duration: 480, Pitch: 67, Velocity: 70}}}, {Kind: "control.add", ClipID: "e1_clip", Events: []project.EventSpec{{ID: "e1_cc", Kind: project.KindCC, Controller: 64, Value: 127}}}})
	if err != nil {
		return err
	}
	_ = result
	before, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	if err = a.onMain(func() {
		a.preferences.View = "split"
		a.install(before)
		a.selectClip(before.State.Tracks[0], before.State.Tracks[0].Clips[0])
	}); err != nil {
		return err
	}
	if err = waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
		return err
	}
	emit := func(name string, value int) error { return a.onMain(func() { a.sendPianoCommand(name, value) }) }
	if err = emit("selectAll", 0); err != nil {
		return err
	}
	if err = waitCondition(func() bool { return a.preview.selectionCount() == 3 }); err != nil {
		return err
	}
	if err = emit("copy", 0); err != nil {
		return err
	}
	if err = emit("locate", 480*256); err != nil {
		return err
	}
	if err = waitCondition(func() bool {
		v, e := a.piano.Page().Eval(`(()=>{const p=document.querySelector('gsui-pianoroll');return Number(p?.dataset.windowOrigin)>0&&!!p.querySelector('[data-id="e1_long"]')&&!!p.querySelector('[data-id="e1_far"]')})()`)
		return e == nil && v == true
	}); err != nil {
		return fmt.Errorf("远端长音尾部不可达：%w", err)
	}
	if a.preview.selectionCount() != 3 {
		return fmt.Errorf("滚动丢失离屏选区")
	}
	if err = emit("paste", 0); err != nil {
		return err
	}
	if err = waitCondition(func() bool {
		p, e := a.service.GetProject(before.ID)
		return e == nil && p.Revision == before.Revision+1 && a.preview.Context().Revision == p.Revision && a.edits.State().Pending == nil
	}); err != nil {
		return fmt.Errorf("粘贴事务：%w", err)
	}
	after, err := a.service.GetProject(before.ID)
	if err != nil {
		return err
	}
	if len(after.State.Tracks[0].Clips[0].Notes) != 6 || !reflect.DeepEqual(after.State.Tracks[0].Clips[0].Controls, before.State.Tracks[0].Clips[0].Controls) {
		return fmt.Errorf("粘贴丢失原事件")
	}
	if err = a.onMain(func() { a.mutate("undo", nil) }); err != nil {
		return err
	}
	if err = waitCondition(func() bool { return a.preview.Context().Revision == before.Revision+2 && a.preview.toolsReady() }); err != nil {
		return err
	}
	restored, _ := a.service.GetProject(before.ID)
	if !reflect.DeepEqual(restored.State, before.State) {
		return fmt.Errorf("粘贴未作为一次意图撤销")
	}
	stage("paste and undo passed")
	// Native view changes must keep the underlying page and its close protocol.
	if err = a.onMain(func() {
		a.arrangementUI.gesture = &clipGesture{Target: EditTarget{restored.ID, restored.Revision}, Track: restored.State.Tracks[0], Clip: restored.State.Tracks[0].Clips[0]}
		a.preferences.View = "arrangement"
		a.preferences.HideAgent = true
		a.preferences.HideBrowser = true
	}); err != nil {
		return err
	}
	if err = a.verifyCloseGuard(false); err != nil {
		return fmt.Errorf("隐藏卷帘关闭取消：%w", err)
	}
	if err = a.onMain(func() {
		a.preferences.View = "split"
		a.preferences.HideAgent = false
		a.preferences.HideBrowser = false
	}); err != nil {
		return err
	}
	if err = emit("locate", 0); err != nil {
		return err
	}
	stage("hidden page close passed")
	// DOM composition/input exclusion: these synthetic key events prove host
	// filtering, not a physical OS input method.
	value, err := a.piano.Page().Eval(`(()=>{const p=document.querySelector('gsui-pianoroll'),input=document.createElement('input');p.append(input);input.dispatchEvent(new KeyboardEvent('keydown',{key:'Delete',bubbles:true,isComposing:true}));input.dispatchEvent(new KeyboardEvent('keydown',{key:'z',ctrlKey:true,bubbles:true}));input.remove();return true})()`)
	if err != nil || value != true {
		return fmt.Errorf("输入法过滤检查：%v", err)
	}
	stage("input filter passed")
	// Scroll-to-two-RAF latency covers WebKit projection, not audio or hardware.
	_, err = a.piano.Page().Eval(`(()=>{window.__e1Perf=null;(async()=>{const area=document.querySelector('.gsuiTimewindow-scrollArea'),p=document.querySelector('gsui-pianoroll'),samples=[];for(let i=0;i<40;i++){const t=performance.now();area.scrollLeft=(i%2)*300;await new Promise(requestAnimationFrame);await new Promise(requestAnimationFrame);samples.push(performance.now()-t);window.__editorProgress=JSON.stringify({i,elapsed:samples[samples.length-1],mounted:p.dataset.mountedNotes,visibility:document.visibilityState,error:document.documentElement.dataset.loadError})}samples.sort((a,b)=>a-b);window.__e1Perf=JSON.stringify({samples:40,p50:samples[19],p95:samples[37],mounted:Number(p.dataset.mountedNotes),scope:'WebKit scroll-to-two-RAF; synthetic input; no physical IME/audio claim'})})();return true})()`)
	if err != nil {
		return err
	}
	var perf string
	if err = waitCondition(func() bool {
		v, e := a.piano.Page().Eval("window.__e1Perf")
		if e == nil {
			perf, _ = v.(string)
		}
		return perf != ""
	}); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "editor-e1.json"), []byte(perf), 0600); err != nil {
		return err
	}
	if err = waitCondition(func() bool {
		v, e := a.piano.Page().Eval("document.querySelector('[role=alert]')?.textContent||''")
		return e == nil && v == ""
	}); err != nil {
		return fmt.Errorf("过时回复污染当前卷帘：%w", err)
	}
	snapshot, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "editor-e1-native.png"), snapshot, 0600); err != nil {
		return err
	}
	snapshot, err = a.piano.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "editor-e1-piano.png"), snapshot, 0600); err != nil {
		return err
	}
	for _, size := range []struct {
		name          string
		width, height int
		scale         float32
	}{{"1440", 1440, 900, 1}, {"1280", 1280, 800, 1}, {"200-percent", 1440, 900, 2}} {
		if err = a.onMain(func() { a.preferences.Scale = size.scale; a.win.SetSize(size.width, size.height) }); err != nil {
			return err
		}
		// Wait for the actual WebView viewport resize to settle through its callback.
		time.Sleep(100 * time.Millisecond)
		shot, e := a.win.CapturePage()
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dir, "editor-e1-"+size.name+".png"), shot, 0600); e != nil {
			return e
		}
	}
	if err = a.onMain(func() { a.preferences.Scale = 1; a.win.SetSize(1440, 900) }); err != nil {
		return err
	}
	if err = a.service.DeleteProject(before.ID); err != nil {
		return err
	}
	stage("all E1 assertions and capture passed")
	prior, err := a.service.GetProject(original.ProjectID)
	if err != nil {
		return err
	}
	if err = a.onMain(func() {
		a.install(prior)
		for _, t := range prior.State.Tracks {
			for _, c := range t.Clips {
				if c.ID == original.ClipID {
					a.selectClip(t, c)
				}
			}
		}
	}); err != nil {
		return err
	}
	return nil
}

// Measures actual WebKit scrolling/painting scheduling on the current project.
// No waits for network/model/device audio are included in this measurement.
func (a *Workspace) benchmarkEditorScroll() (string, error) {

	if err := a.onMain(func() { a.win.Focus(); a.piano.Focus() }); err != nil {
		return "", err
	}
	_, err := a.piano.Page().Eval(`(()=>{window.__editorProgress=null;window.__editorPerf=null;(async()=>{const area=document.querySelector('.gsuiTimewindow-scrollArea'),p=document.querySelector('gsui-pianoroll'),samples=[];for(let i=0;i<40;i++){const t=performance.now();area.scrollLeft=(i%2)*300;await new Promise(requestAnimationFrame);await new Promise(requestAnimationFrame);samples.push(performance.now()-t);window.__editorProgress=JSON.stringify({i,elapsed:samples[samples.length-1],mounted:p.dataset.mountedNotes,visibility:document.visibilityState,error:document.documentElement.dataset.loadError})}samples.sort((a,b)=>a-b);window.__editorPerf=JSON.stringify({samples:40,p50:samples[19],p95:samples[37],mounted:Number(p.dataset.mountedNotes),scope:'WebKit scroll-to-two-RAF; synthetic input; no physical IME/audio claim'})})();return true})()`)
	if err != nil {
		return "", err
	}
	var value string
	err = waitCondition(func() bool {
		v, e := a.piano.Page().Eval("window.__editorPerf")
		if e == nil {
			value, _ = v.(string)
		}
		return value != ""
	})
	if err != nil {
		diagnostic, _ := a.piano.Page().Eval("window.__editorProgress")
		return value, fmt.Errorf("%w: %v", err, diagnostic)
	}
	return value, err
}
