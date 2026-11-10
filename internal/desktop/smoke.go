package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
	// Normal startup shows the Playlist. Mount the embedded page for the
	// layout and Native-to-WebView checks.
	if err := a.onMain(func() {
		a.preferences.View = "split"
		a.split = 48
	}); err != nil {
		return err
	}
	first, err := a.waitPage(0)
	if err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		if err := a.verifyPianoViewport(); err != nil {
			return err
		}
	}
	// Wait for the independent Piano Roll to receive its full height
	// before selecting a clip. GridSound centers once on clip mount.
	splitHeight, err := mygo.EvalAs[int](a.piano.Page(), "innerHeight")
	if err != nil {
		return err
	}
	if err := a.onMain(func() { a.preferences.View = "piano" }); err != nil {
		return err
	}
	if err := waitCondition(func() bool {
		height, err := mygo.EvalAs[int](a.piano.Page(), "innerHeight")
		return err == nil && height >= splitHeight+120
	}); err != nil {
		return fmt.Errorf("独立卷帘尚未完成尺寸分配：%w", err)
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
	// GridSound mounts the visible pitches after centering the new clip in
	// requestAnimationFrame, which can follow the page's ready notification.
	var ids []string
	if err := waitCondition(func() bool {
		keys, err := mygo.EvalAs[string](a.piano.Page(), "JSON.stringify([...document.querySelectorAll('.gsuiBlocksManager-block[data-id]')].map(e=>e.dataset.id))")
		return err == nil && json.Unmarshal([]byte(keys), &ids) == nil && len(ids) == len(expected.Notes)
	}); err != nil {
		return fmt.Errorf("切换后 DOM 音符数量 %d，预期 %d：%w", len(ids), len(expected.Notes), err)
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
		if err = a.verifyEditorWorkspace(dir); err != nil {
			return fmt.Errorf("E1 工作区：%w", err)
		}
		if err = a.verifyMIDICreation(dir); err != nil {
			return fmt.Errorf("E2 MIDI 创作：%w", err)
		}
		if err = a.verifyAudioE3(dir); err != nil {
			return fmt.Errorf("E3 音频：%w", err)
		}
		if err = a.verifyMixerE4(dir); err != nil {
			return fmt.Errorf("E4 混音：%w", err)
		}
		if err = a.verifyMaterialsE5(dir); err != nil {
			return fmt.Errorf("E5 素材与采样：%w", err)
		}
		if folder := os.Getenv("ICETUDIO_PLUGIN_SMOKE_DIR"); folder != "" {
			if err = a.verifyPluginCatalogE6(folder, dir); err != nil {
				return fmt.Errorf("E6 插件库与运行：%w", err)
			}
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
	mygoVersion := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/egoist/mygo" {
				mygoVersion = dep.Version
				if dep.Replace != nil {
					mygoVersion = dep.Replace.Path + "@" + dep.Replace.Version
				}
				break
			}
		}
	}
	payload, err := json.MarshalIndent(map[string]any{"mygo": mygoVersion, "readOnly": !a.smokeEdit, "editFlowVerified": a.smokeEdit, "nativeWorkspaceVerified": a.smokeEdit, "nativeHistoryReadVerified": a.smokeAgentHistory, "nativeComposerVerified": a.smokeAgentComposer, "nativePanelsVerified": a.smokePanels, "nativeAgentEditVerified": a.smokeAgentEdit, "initial": first, "afterNativeSelection": second, "mountedNotes": len(ids), "scope": "Native/WebView captures and typed context selection; editFlowVerified additionally covers Native project lifecycle/properties/time map/checkpoints/MIDI roundtrip, transforms, undo, lost acknowledgement recovery and pending-close cancellation; no physical pointer, IME or audio acceptance"}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "result.json"), payload, 0600)
}

// A view change must resize the real embedded page without a window resize
// or a page reload. GTK previously left the child at its old allocation.
func (a *Workspace) verifyPianoViewport() error {
	size := func() (int, error) { return mygo.EvalAs[int](a.piano.Page(), "innerHeight") }
	var splitHeight int
	if err := waitCondition(func() bool {
		splitHeight, _ = size()
		return splitHeight > 150
	}); err != nil {
		return fmt.Errorf("分屏卷帘未获得初始视口（高度 %d）：%w", splitHeight, err)
	}
	generation := a.preview.Context().Generation
	pageStart, err := mygo.EvalAs[float64](a.piano.Page(), "performance.timeOrigin")
	if err != nil {
		return err
	}
	for _, mode := range []string{"arrangement", "piano", "split", "piano", "split"} {
		if err := a.onMain(func() { a.preferences.View = mode }); err != nil {
			return err
		}
		if mode == "arrangement" {
			continue
		}
		var height int
		if err := waitCondition(func() bool {
			height, _ = size()
			if mode == "piano" {
				return height >= splitHeight+120
			}
			return height >= splitHeight-4 && height <= splitHeight+4
		}); err != nil {
			return fmt.Errorf("切换到 %s 后卷帘高度为 %d，原分屏高度 %d：%w", mode, height, splitHeight, err)
		}
	}
	// Resize while the WebView is hidden, then reveal it without another
	// top-level resize. This is the failing path on the Linux preview.
	wide, err := mygo.EvalAs[int](a.piano.Page(), "innerWidth")
	if err != nil {
		return err
	}
	var originalW, originalH int
	if err := a.onMain(func() {
		originalW, originalH = a.win.Size()
		a.preferences.View = "arrangement"
		a.win.SetSize(900, 640)
	}); err != nil {
		return err
	}
	if err := waitCondition(func() bool {
		w, _ := a.win.Size()
		return w <= 920
	}); err != nil {
		return fmt.Errorf("无法建立窄窗口：%w", err)
	}
	if err := a.onMain(func() { a.preferences.View = "piano" }); err != nil {
		return err
	}
	var narrow int
	if err := waitCondition(func() bool {
		narrow, _ = mygo.EvalAs[int](a.piano.Page(), "innerWidth")
		return narrow > 300 && narrow < wide-250
	}); err != nil {
		return fmt.Errorf("窄窗口重新显示卷帘后仍使用原尺寸（宽度 %d，原宽度 %d）：%w", narrow, wide, err)
	}
	if err := a.onMain(func() {
		a.preferences.View = "split"
		a.win.SetSize(originalW, originalH)
	}); err != nil {
		return err
	}
	if err := waitCondition(func() bool {
		w, _ := mygo.EvalAs[int](a.piano.Page(), "innerWidth")
		h, _ := size()
		return w >= wide-4 && w <= wide+4 && h >= splitHeight-4 && h <= splitHeight+4
	}); err != nil {
		return fmt.Errorf("恢复宽窗口后卷帘尺寸未恢复：%w", err)
	}
	settledPage, err := mygo.EvalAs[float64](a.piano.Page(), "performance.timeOrigin")
	if err != nil {
		return err
	}
	if settledPage != pageStart || a.preview.Context().Generation != generation {
		return fmt.Errorf("切换布局重载了卷帘页面或编辑上下文")
	}
	return nil
}
