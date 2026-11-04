package desktop

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"icetudio/internal/storage"
)

func waitCondition(read func() bool) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if read() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("编辑验证等待超时")
}
func (a *Workspace) onMain(fn func()) error {
	done := make(chan struct{})
	a.update(func() { defer close(done); fn() })
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("Native 回调超时")
	}
}

// Explicit --smoke-edit writes only through the same Native command handlers
// and reusable Piano Roll transform hook used by the normal UI. Test callers
// must supply a disposable project: undo restores music, not revision history.
func (a *Workspace) verifyPianoEdits() error {
	if a.service == nil || a.edits == nil {
		return errors.New("编辑验证需要实际测试工程")
	}
	ctx := a.preview.Context()
	before, err := a.service.GetProject(ctx.ProjectID)
	if err != nil {
		return err
	}
	if len(ctx.Clip.Notes) == 0 {
		return errors.New("编辑验证需要包含音符的所选测试片段")
	}
	start := before.Revision
	if err = waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
		return fmt.Errorf("等待编辑就绪：%w", err)
	}
	// Test a Native gesture close blocker before writing any music. The actual
	// pointer capture is covered by MyGo headless input tests; here the live
	// window must freeze/cancel and restore the page's editing readiness.
	if err = a.onMain(func() {
		a.arrangementUI.defaults()
		a.arrangementUI.gesture = &clipGesture{Target: EditTarget{ctx.ProjectID, ctx.Revision}, Track: ctx.Track, Clip: ctx.Clip, PPQ: ctx.PPQ, Zoom: a.arrangementUI.zoom, Snap: 1, Start: ctx.Clip.Start, Length: ctx.Clip.Length}
		a.arrangementActions().Freeze(true)
	}); err != nil {
		return err
	}
	if err = a.verifyCloseGuard(false); err != nil {
		return fmt.Errorf("Native 手势关闭取消：%w", err)
	}
	if err = waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
		return fmt.Errorf("取消退出后卷帘未恢复编辑：%w", err)
	}
	selectNotes := func() error {
		if err := a.onMain(func() { pianoCommand.EmitPage(a.piano.Page(), a.preview.command("selectAll", 0)) }); err != nil {
			return err
		}
		return waitCondition(func() bool { return a.preview.toolsReady() && len(a.preview.command("transpose", 1).NoteIDs) > 0 })
	}
	waitRevision := func(rev int64, pending bool) error {
		return waitCondition(func() bool {
			p, err := a.service.GetProject(before.ID)
			s := a.edits.State()
			return err == nil && p.Revision == rev && !s.Busy && (s.Pending != nil) == pending && a.preview.Context().Revision == rev
		})
	}
	if err = selectNotes(); err != nil {
		return err
	}

	var velocityForm *propertyForm
	if err = a.onMain(func() {
		a.beginVelocity()
		velocityForm = a.form
		if velocityForm != nil {
			velocityForm.values.Name = "101"
			a.changedForm(velocityForm)
		}
	}); err != nil {
		return err
	}
	if velocityForm == nil {
		return errors.New("Native 力度表单不可用")
	}
	if err = waitCondition(func() bool {
		ready := false
		_ = a.onMain(func() { ready = velocityForm.writes == 0 && velocityForm.err == "" })
		return ready
	}); err != nil {
		return fmt.Errorf("力度输入保存：%w", err)
	}
	if err = a.onMain(func() { a.applyForm(velocityForm) }); err != nil {
		return err
	}
	if err = waitRevision(start+1, false); err != nil {
		return fmt.Errorf("Native 力度提交：%w", err)
	}
	if err = waitCondition(func() bool { ready := false; _ = a.onMain(func() { ready = a.form == nil }); return ready }); err != nil {
		return err
	}
	if err = a.onMain(func() { a.mutate("undo", nil) }); err != nil {
		return err
	}
	if err = waitRevision(start+2, false); err != nil {
		return fmt.Errorf("力度共享撤销：%w", err)
	}
	start += 2
	if err = waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
		return err
	}
	if err = selectNotes(); err != nil {
		return err
	}
	if err = a.onMain(func() { pianoCommand.EmitPage(a.piano.Page(), a.preview.command("transpose", 1)) }); err != nil {
		return err
	}
	if err = waitRevision(start+1, false); err != nil {
		return fmt.Errorf("卷帘变换及 ack：%w; %s", err, a.editDiagnostics())
	}
	if err = a.onMain(func() { a.mutate("undo", nil) }); err != nil {
		return err
	}
	if err = waitRevision(start+2, false); err != nil {
		return fmt.Errorf("Native 撤销：%w", err)
	}
	restored, err := a.service.GetProject(before.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before.State, restored.State) {
		return errors.New("Native 撤销后音乐内容不一致")
	}
	// Inject an acknowledgement endpoint failure only in explicit disposable
	// smoke mode. MyGo's runtime is frozen; mutating mygo.call is not supported.
	a.preview.mu.Lock()
	a.preview.ackFault = true
	a.preview.mu.Unlock()
	if err = waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
		return err
	}
	if err = selectNotes(); err != nil {
		return err
	}
	if err = a.onMain(func() { pianoCommand.EmitPage(a.piano.Page(), a.preview.command("transpose", 1)) }); err != nil {
		return err
	}
	if err = waitRevision(start+3, true); err != nil {
		return fmt.Errorf("丢失页面确认时保留 journal：%w; %s", err, a.editDiagnostics())
	}
	if err = a.verifyCloseGuard(true); err != nil {
		return err
	}
	a.preview.mu.Lock()
	a.preview.ackFault = false
	a.preview.mu.Unlock()
	if err = a.onMain(a.queryEdit); err != nil {
		return err
	}
	if err = waitRevision(start+3, false); err != nil {
		return fmt.Errorf("查询原结果不重放：%w", err)
	}
	if err = a.onMain(func() { a.mutate("undo", nil) }); err != nil {
		return err
	}
	if err = waitRevision(start+4, false); err != nil {
		return err
	}
	restored, err = a.service.GetProject(before.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before.State, restored.State) {
		return errors.New("恢复测试后音乐内容不一致")
	}
	return a.assertNoReplay(before, start+4)
}
func (a *Workspace) assertNoReplay(before storage.Project, revision int64) error {
	item, err := a.service.GetProject(before.ID)
	if err != nil {
		return err
	}
	if item.Revision != revision {
		return errors.New("恢复查询意外增加修订")
	}
	return nil
}

func (a *Workspace) editDiagnostics() string {
	p, _ := a.service.GetProject(a.preview.Context().ProjectID)
	value, _ := a.piano.Page().Eval(`JSON.stringify({status:document.body.innerText,ready:document.body.dataset.ready})`)
	return fmt.Sprintf("revision=%d contextRevision=%d writable=%v edits=%+v page=%v", p.Revision, a.preview.Context().Revision, a.preview.Context().Writable, a.edits.State(), value)
}

// Exercise interception and cancel against a real pending durable edit and page.
func (a *Workspace) verifyCloseGuard(expectPending bool) error {
	// A late cancel cannot unfreeze a newer close, nor a canceled inspection
	// arriving late freeze a page again.
	safe, err := a.piano.Page().Eval(`(()=>{const api=window.__icetudioPiano,r=document.getElementById("root"),id=crypto.randomUUID();api.inspect(id+"_old");api.resume(id+"_old");api.inspect(id+"_new");api.resume(id+"_old");const newer=r.inert;api.resume(id+"_new");api.resume(id+"_late");api.inspect(id+"_late");return newer&&!r.inert})()`)
	if err != nil || safe != true {
		return fmt.Errorf("关闭 token 隔离失败：%v %v", safe, err)
	}

	prevented := false
	if err := a.onMain(func() { a.requestClose(func() { prevented = true }) }); err != nil {
		return err
	}
	if !prevented {
		return errors.New("待确认操作未拦截退出")
	}
	var inspected bool
	if err := waitCondition(func() bool {
		_ = a.onMain(func() { inspected = a.closeFrozen && !a.closeInspectBusy && !a.closeApproved })
		return inspected
	}); err != nil {
		return err
	}
	inert, err := a.piano.Page().Eval("document.getElementById('root').inert")
	if err != nil || inert != true {
		return fmt.Errorf("关闭检查未冻结页面：%v %v", inert, err)
	}
	if (a.edits.State().Pending != nil) != expectPending {
		return errors.New("关闭检查改变了恢复标记状态")
	}
	if err = a.onMain(a.cancelClose); err != nil {
		return err
	}
	return waitCondition(func() bool {
		v, e := a.piano.Page().Eval("document.getElementById('root').inert")
		return e == nil && v == false
	})
}
