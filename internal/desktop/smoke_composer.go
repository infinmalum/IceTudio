package desktop

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"icetudio/internal/app"
	"icetudio/internal/materials"
)

// Only --smoke-edit on disposable host data. Exercises Native draft queue and
// send preparation/cancellation; no model send or remote provider is invoked.
func (a *Workspace) verifyNativeComposer(dir string) error {
	waitUI := func(read func() bool) error {
		return waitCondition(func() bool {
			value := false
			if err := a.onMain(func() { value = read() }); err != nil {
				return false
			}
			return value
		})
	}
	if err := waitUI(func() bool { return a.composer.ready && !a.composer.busy && a.composer.writes == 0 }); err != nil {
		return err
	}
	var original string
	if err := a.onMain(func() {
		original = a.agent.selected
		a.selectConversation("")
		a.checkpointComposer("new", app.ConversationDraft{Text: "真实窗口的未发送输入\n保留附件并冻结片段", Uploads: []materials.Upload{{Name: "材料.txt", Data: base64.StdEncoding.EncodeToString([]byte("冻结后的附件内容"))}}})
	}); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.composer.writes == 0 && len(a.composer.errors) == 0 }); err != nil {
		return err
	}
	saved, err := a.service.GetConversationDrafts()
	if err != nil || saved["new"].Text == "" || len(saved["new"].Uploads) != 1 {
		return fmt.Errorf("Native 输入未持久保存：%v", err)
	}
	if err := a.onMain(a.prepareNativeSend); err != nil {
		return err
	}
	if err := waitUI(func() bool {
		return !a.composer.busy && (a.composer.confirmation != nil || a.composer.actionError != "")
	}); err != nil {
		return err
	}
	var n *nativeSend
	var message string
	if err := a.onMain(func() { n = a.composer.confirmation; message = a.composer.actionError }); err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("Native 发送准备：%s", message)
	}
	if n.draft.Text != saved["new"].Text || len(n.draft.Uploads) != 1 || n.selection == nil {
		return fmt.Errorf("发送准备丢失输入或工程片段范围")
	}
	snap, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "agent-compose.png"), snap, 0600); err != nil {
		return err
	}
	// Cancel the real modal state without granting or dispatching anything.
	if err := a.onMain(func() {
		a.composer.confirmation = nil
		a.composer.manage = true
		a.composer.title = "Native 输入恢复验证"
		a.manageAgent(func() error { _, err := a.service.RenameConversation(n.key, "Native 输入恢复验证"); return err }, nil)
	}); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !a.composer.busy }); err != nil {
		return err
	}
	runs, err := a.service.ListRuns(n.key)
	if err != nil || len(runs) != 0 {
		return fmt.Errorf("取消确认创建了任务：%v", err)
	}
	saved, err = a.service.GetConversationDrafts()
	if err != nil || saved[n.key].Text == "" || saved["new"].Text != "" {
		return fmt.Errorf("发送建对话未安全接管 Native 草稿：%v", err)
	}
	// Restore the same recorded bytes, as a restart would, then explicitly
	// consume this disposable test draft and remove only its extra conversation.
	if err := a.onMain(func() { a.composer.manage = false; a.loadComposer() }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return !a.composer.busy && a.composer.entries[n.key].Text == n.draft.Text }); err != nil {
		return err
	}
	if err := a.onMain(func() { a.checkpointComposer(n.key, app.ConversationDraft{}) }); err != nil {
		return err
	}
	if err := waitUI(func() bool { return a.composer.writes == 0 && len(a.composer.errors) == 0 }); err != nil {
		return err
	}
	if err := a.service.DeleteConversation(n.key); err != nil {
		return err
	}
	if err := a.onMain(func() { a.selectConversation(original); a.refreshConversations(); a.smokeAgentComposer = true }); err != nil {
		return err
	}
	return nil
}
