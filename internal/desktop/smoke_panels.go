package desktop

import (
	"fmt"
	"github.com/egoist/mygo"
	"icetudio/internal/materials"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"icetudio/internal/app"
	"icetudio/internal/storage"
)

// Uses only disposable host data. The native integration test supplies a local
// HTTP model fixture; a normal --smoke-edit without that fixture skips the
// Agent write probe, never silently sending test instructions to user models.
func (a *Workspace) verifyNativePanels(dir string) error {
	waitUI := func(read func() bool) error {
		return waitCondition(func() bool {
			value := false
			if err := a.onMain(func() { value = read() }); err != nil {
				return false
			}
			return value
		})
	}
	if err := a.onMain(func() { a.openSettings("执行配置") }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.settings != nil && !a.settings.busy }); err != nil {
		return err
	}
	var d *nativeSettings
	var next storage.RuntimeConfig
	if err := a.onMain(func() {
		d = a.settings
		if len(d.configs) > 0 {
			next = cloneNative(d.configs[0])
			next.Data.Description = "Native 完整执行配置往返"
			d.form = newNativeForm(&next)
		}
	}); err != nil {
		return err
	}
	if next.ID == "" {
		return fmt.Errorf("Native 执行配置未加载：%s", d.err)
	}
	var saved storage.RuntimeConfig
	if err := a.onMain(func() {
		a.settingsAction(d, func() error { var err error; saved, err = a.service.UpdateRuntimeConfig(next); return err }, func() { d.form = newNativeForm(&saved) })
	}); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !d.busy }); err != nil {
		return err
	}
	if saved.Revision != next.Revision+1 || !reflect.DeepEqual(saved.Data.Execution, next.Data.Execution) {
		return fmt.Errorf("执行组件保存丢失字段或修订")
	}
	image, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "native-settings.png"), image, 0600); err != nil {
		return err
	}
	if err := a.onMain(func() { d.open = false; a.openSettings("权限") }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.settings != nil && !a.settings.busy }); err != nil {
		return err
	}
	if err := a.onMain(func() { d = a.settings }); err != nil {
		return err
	}
	if d.context == nil {
		return fmt.Errorf("Native 权限读取：%s", d.err)
	}
	base := d.context.Policy
	edited := cloneNative(base)
	edited.ApprovalMode = "semi"
	if err := a.onMain(func() {
		a.settingsAction(d, func() error {
			latest, err := a.service.GetLocalContext(d.contextID)
			if err != nil {
				return err
			}
			merged, err := mergeNativePolicy(base, edited, latest.Policy)
			if err != nil {
				return err
			}
			_, err = a.service.UpdateContextPolicy(d.contextID, merged)
			return err
		}, nil)
	}); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !d.busy }); err != nil {
		return err
	}
	actual, err := a.service.GetLocalContext(d.contextID)
	if err != nil || actual.Policy.ApprovalMode != "semi" {
		return fmt.Errorf("Native 权限保存：%v", err)
	}
	if _, err := a.service.UpdateContextPolicy(d.contextID, base); err != nil {
		return err
	}
	if err := a.onMain(func() { d.open = false; a.openSkillPanel() }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.skillPanel != nil && !a.skillPanel.busy }); err != nil {
		return err
	}
	if err := a.onMain(func() { a.skillPanel.open = false; a.skillPanel = nil; a.settings = nil; a.smokePanels = true }); err != nil {
		return err
	}
	if err := a.verifyReadOnlyIsolation(); err != nil {
		return err
	}
	if os.Getenv("ICETUDIO_MYGO_LOCAL_MODEL_FIXTURE") != "1" {
		return nil
	}
	return a.verifyNativeAgentEdit(dir)
}
func (a *Workspace) verifyNativeAgentEdit(dir string) error {
	waitUI := func(read func() bool) error {
		return waitCondition(func() bool {
			value := false
			if err := a.onMain(func() { value = read() }); err != nil {
				return false
			}
			return value
		})
	}
	ctx := a.preview.Context()
	before, err := a.service.GetProject(ctx.ProjectID)
	if err != nil {
		return err
	}
	var previous string
	if err := a.onMain(func() {
		previous = a.agent.selected
		a.selectConversation("")
		a.checkpointComposer("new", app.ConversationDraft{Text: "Native 合成模型：只修改所选片段的音符力度"})
	}); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.composer.writes == 0 }); err != nil {
		return err
	}
	if err := a.onMain(a.prepareNativeSend); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !a.composer.busy }); err != nil {
		return err
	}
	var n *nativeSend
	if err := a.onMain(func() {
		n = a.composer.confirmation
		if n != nil {
			a.confirmNativeSend(n)
		}
	}); err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("Native Agent 发送确认未加载")
	}
	if err := waitUI(func() bool { return !a.composer.busy }); err != nil {
		return err
	}
	if err := waitUI(func() bool {
		for _, call := range a.agent.calls {
			if call.ToolName == "project__edit" && call.State == storage.CallPending {
				a.openApproval(call)
				return true
			}
		}
		return false
	}); err != nil {
		runs, _ := a.service.ListRuns(n.key)
		calls, _ := a.service.ListToolCalls(n.key)
		return fmt.Errorf("Native 审批请求：%w；runs=%s calls=%s", err, prettyDetail(runs), prettyDetail(calls))
	}
	var d *nativeApproval
	if err := a.onMain(func() {
		d = a.approval
		a.approvalAction(d, func() error {
			p, err := a.service.PreviewProjectEdit(d.call.ID)
			if err == nil {
				a.update(func() { d.preview = &p })
			}
			return err
		}, nil)
	}); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !d.busy && d.preview != nil }); err != nil {
		return fmt.Errorf("Native 候选差异：%w", err)
	}
	if d.preview.Diff.Updated != 1 || !d.preview.NotesOnly {
		return fmt.Errorf("候选差异与合成请求不符")
	}
	if err := a.onMain(func() {
		a.preview.mu.Lock()
		a.preview.value.Candidate = d.preview
		a.preview.mu.Unlock()
		a.notifyPiano()
	}); err != nil {
		return err
	}
	if err := waitCondition(func() bool {
		value, err := a.piano.Page().Eval("document.querySelectorAll('.piano-ghost').length")
		return err == nil && fmt.Sprint(value) == "2"
	}); err != nil {
		return fmt.Errorf("只读卷帘 ghost：%w", err)
	}
	image, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "native-approval.png"), image, 0600); err != nil {
		return err
	}
	hash := d.preview.ArgumentsHash
	if err := a.onMain(func() {
		a.approvalAction(d, func() error { _, err := a.service.ApproveProjectEditPreview(d.call.ID, hash); return err }, func() { d.open = false })
	}); err != nil {
		return err
	}
	a.service.WaitIdle()
	after, err := a.service.GetProject(before.ID)
	if err != nil {
		return err
	}
	if after.Revision != before.Revision+1 {
		return fmt.Errorf("Agent 候选未提交一次，revision %d → %d", before.Revision, after.Revision)
	}
	if err := a.onMain(func() { d.open = false; a.mutateAt(EditTarget{before.ID, after.Revision}, "undo", nil) }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !a.editBusy && a.active.Revision == before.Revision+2 }); err != nil {
		return err
	}
	restored, err := a.service.GetProject(before.ID)
	if err != nil || !reflect.DeepEqual(restored.State, before.State) {
		return fmt.Errorf("Agent 编辑后的 Native 共同撤销未恢复音乐：%v", err)
	}
	if err := a.service.DeleteConversation(n.key); err != nil {
		return err
	}
	if err := a.onMain(func() { a.selectConversation(previous); a.refreshConversations(); a.smokeAgentEdit = true }); err != nil {
		return err
	}
	return nil
}

func (a *Workspace) verifyReadOnlyIsolation() error {
	var view *mygo.WebView
	var createErr error
	if err := a.onMain(func() {
		view, createErr = a.readOnlyView()
		if createErr == nil {
			a.attachment = &nativeAttachment{open: true, file: materials.File{Name: "只读隔离回归", MediaType: "application/pdf", Data: "test"}, media: view}
		}
	}); err != nil {
		return err
	}
	if createErr != nil {
		return createErr
	}
	markup := `<p>Local read-only content</p><iframe sandbox srcdoc="&lt;script&gt;parent.__escaped=true&lt;/script&gt;"></iframe>`
	if err := setReadOnlyBody(view, markup); err != nil {
		return err
	}
	_, err := view.Page().Eval(`window.__escaped=false;window.__bridgeCheck='waiting';(async()=>{try{await window.mygo.call('PianoPreview.Context');window.__bridgeCheck='ALLOWED'}catch(e){window.__bridgeCheck='denied:'+String(e)}})();true`)
	if err != nil {
		return err
	}
	var result string
	if err := waitCondition(func() bool {
		value, e := view.Page().Eval("window.__bridgeCheck")
		if e != nil {
			return false
		}
		result = fmt.Sprint(value)
		return result != "waiting" && result != "<nil>"
	}); err != nil {
		return err
	}
	if !strings.HasPrefix(result, "denied:") {
		return fmt.Errorf("只读正文越权调用 Go：%s", result)
	}
	value, err := view.Page().Eval("window.__escaped===true")
	if err != nil {
		return err
	}
	if value == true {
		return fmt.Errorf("不可信正文逃出 sandbox")
	}
	return a.onMain(func() { a.attachment.open = false })
}
