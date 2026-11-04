package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

type pageSnapshot struct {
	Ready      string `json:"ready"`
	Error      string `json:"error"`
	Notes      string `json:"notes"`
	Clip       string `json:"clip"`
	Generation string `json:"generation"`
}

// smoke exercises the actual WebView, bound read method, typed context event
// and captures both surfaces. It does not claim real pointer/IME/audio QA.
func (a *Workspace) smoke(dir string) {
	err := a.captureSmoke(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "MyGo smoke:", err)
		// Explicit disposable smoke mode must terminate after reporting failure;
		// production close paths remain guarded and recovery files stay intact.
		_ = a.onMain(func() { a.closeApproved = true })
	}
	a.smokeResult <- err
	mygo.App.Quit()
}
func (a *Workspace) waitPage(expected uint64) (pageSnapshot, error) {
	deadline := time.Now().Add(20 * time.Second)
	var snapshot pageSnapshot
	for time.Now().Before(deadline) {
		value, err := mygo.EvalAs[string](a.piano.Page(), "JSON.stringify({...document.body.dataset,error:document.documentElement.dataset.loadError||document.body.dataset.error})")
		if err == nil {
			if err = json.Unmarshal([]byte(value), &snapshot); err != nil {
				return snapshot, err
			}
			if snapshot.Error != "" {
				return snapshot, fmt.Errorf("卷帘加载失败：%s", snapshot.Error)
			}
			if snapshot.Ready == "true" && (expected == 0 || snapshot.Generation == fmt.Sprint(expected)) {
				return snapshot, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	body, _ := mygo.EvalAs[string](a.piano.Page(), "JSON.stringify({text:document.body.innerText,html:document.documentElement.outerHTML.slice(0,2500),href:location.href,runtime:typeof window.mygo,scripts:[...document.scripts].map(s=>s.src),resources:performance.getEntriesByType('resource').map(r=>({name:r.name,size:r.decodedBodySize}))})")
	return snapshot, fmt.Errorf("卷帘未在 20 秒内确认上下文 %d：%s", expected, body)
}
func (a *Workspace) captureSmoke(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("smoke-dir 必须是绝对路径")
	}
	first, err := a.waitPage(0)
	if err != nil {
		return err
	}
	// Exercise the Native callback -> typed event -> bound Context -> DOM path
	// again, with another clip, not just the page's initial read.
	updated := make(chan struct{})
	a.update(func() {
		defer close(updated)
		for _, track := range a.active.State.Tracks {
			if len(track.Clips) > 1 {
				a.selectClip(track, track.Clips[1])
				return
			}
		}
	})
	select {
	case <-updated:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("Native 选择回调未完成")
	}
	expected := a.preview.Context()
	second, err := a.waitPage(expected.Generation)
	if err != nil {
		return err
	}
	if second.Clip != expected.ClipID || second.Notes != fmt.Sprint(len(expected.Notes)) {
		return fmt.Errorf("页面上下文与 Native 不一致：%+v", second)
	}
	keys, err := mygo.EvalAs[string](a.piano.Page(), "JSON.stringify([...document.querySelectorAll('.gsuiBlocksManager-block[data-id]')].map(e=>e.dataset.id))")
	if err != nil {
		return err
	}
	var ids []string
	if err = json.Unmarshal([]byte(keys), &ids); err != nil {
		return err
	}
	if len(ids) != len(expected.Notes) {
		return fmt.Errorf("切换后 DOM 音符数量 %d，预期 %d", len(ids), len(expected.Notes))
	}
	valid := map[string]bool{}
	for _, note := range expected.Notes {
		valid[note.ID] = true
	}
	for _, id := range ids {
		if !valid[id] {
			return fmt.Errorf("切换后残留旧音符 %s", id)
		}
	}
	if a.smokeEdit {
		if err = a.verifyNativeWorkspace(dir); err != nil {
			return fmt.Errorf("Native 生命周期：%w", err)
		}
		if err = a.verifyNativeComposer(dir); err != nil {
			return fmt.Errorf("Native 输入与确认：%w", err)
		}
		if err = a.verifyNativePanels(dir); err != nil {
			return fmt.Errorf("Native 设置与 Agent：%w", err)
		}
		if err = a.verifyDensePiano(dir); err != nil {
			return fmt.Errorf("Native 密集卷帘：%w", err)
		}
		if err = a.verifyPianoEdits(); err != nil {
			return err
		}
		expected = a.preview.Context()
		second, err = a.waitPage(expected.Generation)
		if err != nil {
			return err
		}
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Captures intentionally separate the surfaces; the native image may have a
	// hole where WebKit composites its page. Neither is a full desktop capture.
	native, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "native.png"), native, 0600); err != nil {
		return err
	}
	web, err := a.piano.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "piano.png"), web, 0600); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(map[string]any{"mygo": "v0.7.4", "readOnly": !a.smokeEdit, "editFlowVerified": a.smokeEdit, "nativeWorkspaceVerified": a.smokeEdit, "nativeHistoryReadVerified": a.smokeAgentHistory, "nativeComposerVerified": a.smokeAgentComposer, "nativePanelsVerified": a.smokePanels, "nativeAgentEditVerified": a.smokeAgentEdit, "initial": first, "afterNativeSelection": second, "mountedNotes": len(ids), "scope": "Native/WebView captures and typed context selection; editFlowVerified additionally covers Native project lifecycle/properties/time map/checkpoints/MIDI roundtrip, transforms, undo, lost acknowledgement recovery and pending-close cancellation; no physical pointer, IME or audio acceptance"}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "result.json"), payload, 0600)
}
