package desktop

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/materials"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

type agentComposer struct {
	loadError   string
	actionError string
	ready       bool
	entries     map[string]app.ConversationDraft
	// workerTokens is owned exclusively by the serial draft queue.
	workerTokens map[string]string
	errors       map[string]string
	writes       int
	busy         bool
	version      uint64
	confirmation *nativeSend
	manage       bool
	title        string
}
type nativeSend struct {
	open         bool
	key          string
	draft        app.ConversationDraft
	conversation storage.Conversation
	selection    *app.ProjectScopeSelection
	consent      []app.SendConsent
	scope        string
}

func (a *Workspace) composerKey() string {
	if a.agent.selected == "" {
		return "new"
	}
	return a.agent.selected
}
func (a *Workspace) composerBlocked() bool {
	return !a.composer.ready || a.composer.busy || a.composer.writes > 0 || a.closeFrozen || a.composer.confirmation != nil
}
func (a *Workspace) loadComposer() {
	a.composer.busy = true
	a.draftQueue <- func() {
		entries, err := a.service.GetConversationDrafts()
		tokens := map[string]string{}
		for k, d := range entries {
			tokens[k] = d.Token
		}
		a.composer.workerTokens = tokens
		a.update(func() {
			a.composer.busy = false
			if err != nil {
				a.composer.loadError = err.Error()
				return
			}
			a.composer.loadError = ""
			a.composer.entries = entries
			a.composer.errors = map[string]string{}
			a.composer.ready = true
		})
	}
}
func (a *Workspace) checkpointComposer(key string, d app.ConversationDraft) {
	d.Token = project.NewID("chatdraft")
	a.composer.entries[key] = d
	a.composer.version++
	a.composer.writes++
	a.draftQueue <- func() {
		err := a.service.SaveConversationDraft(key, a.composer.workerTokens[key], d)
		if err == nil {
			a.composer.workerTokens[key] = d.Token
		}
		a.update(func() {
			a.composer.writes--
			if err != nil {
				a.composer.errors[key] = err.Error()
			} else {
				delete(a.composer.errors, key)
			}
		})
	}
}
func (a *Workspace) nativeComposer(c *ui.Context) {
	x := &a.composer
	key := a.composerKey()
	d := x.entries[key]
	if !x.ready {
		ui.Text(c, "正在读取 Native 输入草稿；旧浏览器草稿仍保留原位。").MaxWidth(296).FontSize(11)
		if x.loadError != "" {
			ui.Text(c, "输入草稿读取失败："+x.loadError).MaxWidth(296).TextColor(c.Theme().Warning)
		}
		if !x.busy {
			ui.Button(c, "重新读取输入草稿").OnClick(a.loadComposer)
		}
		return
	}
	if x.actionError != "" {
		ui.Text(c, x.actionError).MaxWidth(296).TextColor(c.Theme().Warning)
	}
	ui.Row(c).Gap(4).Children(func() {
		ui.Button(c, "新对话").Disabled(a.composerBlocked()).OnClick(func() { a.selectConversation("") })
		ui.Button(c, "对话设置").Disabled(a.composerBlocked() || key == "new").OnClick(func() {
			x.manage = true
			x.title = ""
			for _, v := range a.agent.conversations {
				if v.ID == key {
					x.title = v.Title
				}
			}
		})
	})
	ui.Text(c, a.sendScopeLabel()).MaxWidth(296).FontSize(11).TextColor(c.Theme().TextMuted)
	for _, item := range a.agent.conversations {
		if item.ID == key && item.Kind == storage.SessionKindDelegation {
			ui.Text(c, "子任务会话仅供查看与审批，请在主对话发送新消息。").MaxWidth(296)
			return
		}
	}
	// No Enter-to-send handler: composition and multiline editing stay in the
	// Native text editor. Only an explicit send click starts a task.
	ui.TextArea(c.Key(key), &d.Text).FillWidth().Height(92).Label("助手消息").Disabled(a.closeFrozen || x.busy || x.confirmation != nil || x.writes > 16 || d.Dispatching).OnChange(func() { a.checkpointComposer(key, d) })
	if len(d.Uploads) > 0 {
		ui.Scroll(c).FillWidth().MaxHeight(100).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for i, u := range d.Uploads {
					ui.Row(c.Key(fmt.Sprintf("file-%d", i))).Gap(4).Children(func() {
						ui.Text(c, u.Name).SingleLine().Grow(1)
						ui.Button(c, "移除").Disabled(a.composerBlocked() || d.Dispatching).OnClick(func() {
							d.Uploads = append(append([]materials.Upload{}, d.Uploads[:i]...), d.Uploads[i+1:]...)
							a.checkpointComposer(key, d)
						})
					})
				}
			})
		})
	}
	if err := x.errors[key]; err != "" {
		ui.Text(c, "草稿保存失败："+err).MaxWidth(296).TextColor(c.Theme().Warning)
		ui.Button(c, "重试保存原输入").Disabled(a.composerBlocked()).OnClick(func() { a.checkpointComposer(key, d) })
	}
	if d.Dispatching {
		ui.Text(c, "上次发送结果待核对。请先刷新消息与任务；不会自动重发。确认未发送后才可重新编辑。").MaxWidth(296).TextColor(c.Theme().Warning)
		ui.Button(c, "已核对，保留输入并解锁").Disabled(a.composerBlocked()).OnClick(a.unlockComposer)
	}
	ui.Row(c).Gap(6).Children(func() {
		ui.Button(c, "附件").Disabled(a.composerBlocked() || d.Dispatching).OnClick(a.pickAgentAttachments)
		ui.Button(c, "发送前确认").Disabled(a.composerBlocked() || d.Dispatching || x.errors[key] != "" || strings.TrimSpace(d.Text) == "" && len(d.Uploads) == 0).OnClick(a.prepareNativeSend)
		if x.writes > 0 {
			ui.Text(c, "保存中…").FontSize(11)
		}
	})
}
func (a *Workspace) sendScopeLabel() string {
	for _, v := range a.agent.conversations {
		if v.ID == a.agent.selected {
			if v.ProjectID == "" {
				return "任务范围：普通对话（未绑定工程）"
			}
			if v.ProjectID == a.active.ID && a.selected != "" {
				return "任务范围：当前片段（确认发送时冻结）"
			}
			return "任务范围：绑定的整个工程"
		}
	}
	if a.active.ID != "" {
		if a.selected != "" {
			return "新对话将绑定当前工程；当前片段作为任务范围"
		}
		return "新对话将绑定当前整个工程"
	}
	return "新对话：普通聊天"
}
func readAgentAttachments(paths []string, old []materials.Upload) ([]materials.Upload, error) {
	if len(paths)+len(old) > materials.MaxFiles {
		return nil, errors.New("每次最多 8 个附件")
	}
	uploads := append([]materials.Upload{}, old...)
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > materials.MaxFileBytes {
			f.Close()
			return nil, errors.New("附件必须是最多 10 MiB 的普通文件")
		}
		b, err := io.ReadAll(io.LimitReader(f, materials.MaxFileBytes+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > materials.MaxFileBytes {
			return nil, errors.New("附件超过 10 MiB")
		}
		uploads = append(uploads, materials.Upload{Name: filepath.Base(path), Data: base64.StdEncoding.EncodeToString(b)})
	}
	if _, err := materials.Prepare(uploads); err != nil {
		return nil, err
	}
	return uploads, nil
}
func (a *Workspace) pickAgentAttachments() {
	if a.composerBlocked() {
		return
	}
	key := a.composerKey()
	d := a.composer.entries[key]
	a.composer.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "添加创作材料（最多 8 个，总量 20 MiB）", Multiple: true})
		uploads := d.Uploads
		if err == nil && len(paths) > 0 {
			uploads, err = readAgentAttachments(paths, d.Uploads)
		}
		a.update(func() {
			a.composer.busy = false
			if err != nil {
				a.composer.actionError = err.Error()
				return
			}
			if len(paths) > 0 {
				d.Uploads = uploads
				a.checkpointComposer(key, d)
			}
		})
	}()
}
func nativeSelection(bound, active, clip string) *app.ProjectScopeSelection {
	if bound != "" && bound == active && clip != "" {
		return &app.ProjectScopeSelection{Clips: []string{clip}}
	}
	return nil
}
func (a *Workspace) prepareNativeSend() {
	if a.composerBlocked() {
		return
	}
	key := a.composerKey()
	d := a.composer.entries[key]
	if d.Dispatching || a.composer.errors[key] != "" {
		return
	}
	a.composer.actionError = ""
	active, clip := a.active.ID, a.selected
	clipName := "未命名片段"
	for _, track := range a.active.State.Tracks {
		for _, item := range track.Clips {
			if item.ID == clip && item.Name != "" {
				clipName = item.Name
			}
		}
	}
	a.composer.busy = true
	a.draftQueue <- func() {
		newCleared := false
		createdID := ""
		var conv storage.Conversation
		var err error
		if key == "new" {
			conv, err = a.service.CreateConversation("", "", "")
			createdID = conv.ID
			if err == nil && active != "" {
				err = a.service.SetConversationProject(conv.ID, active)
			}
			if err == nil {
				d.Token = project.NewID("chatdraft")
				err = a.service.SaveConversationDraft(conv.ID, "", d)
				if err == nil {
					a.composer.workerTokens[conv.ID] = d.Token
					tomb := app.ConversationDraft{Token: project.NewID("chatdraft")}
					err = a.service.SaveConversationDraft("new", a.composer.workerTokens["new"], tomb)
					if err == nil {
						a.composer.workerTokens["new"] = tomb.Token
						newCleared = true
					}
				}
			}
		} else {
			conv, err = a.service.GetConversation(key)
		}
		var consent []app.SendConsent
		if err == nil {
			conv, err = a.service.GetConversation(conv.ID)
		}
		if err == nil {
			consent, err = a.service.PreviewSend(conv.ID, len(d.Uploads) > 0, "main")
		}
		selection := nativeSelection(conv.ProjectID, active, clip)
		scope := "普通对话（未绑定工程）"
		if conv.ProjectID != "" {
			scope = "绑定的整个工程"
		}
		if selection != nil {
			scope = "片段「" + clipName + "」"
		}
		copied := key == "new" && createdID != "" && a.composer.workerTokens[createdID] == d.Token
		newToken := a.composer.workerTokens["new"]
		a.update(func() {
			a.composer.busy = false
			// Once copied, retain both source/recovery records on every later error.
			if copied {
				a.composer.entries[createdID] = d
				if newCleared {
					a.composer.entries["new"] = app.ConversationDraft{Token: newToken}
				}
				a.selectConversation(createdID)
				a.refreshConversations()
			}
			if err != nil {
				a.composer.actionError = err.Error()
				return
			}
			a.composer.confirmation = &nativeSend{open: true, key: conv.ID, draft: d, conversation: conv, selection: selection, consent: consent, scope: scope}
		})
	}
}
func (a *Workspace) confirmNativeSend(n *nativeSend) {
	if a.composer.busy || a.closeFrozen {
		return
	}
	a.composer.busy = true
	a.draftQueue <- func() {
		err := a.dispatchNativeSend(n)
		a.update(func() {
			a.composer.busy = false
			a.composer.confirmation = nil
			a.composer.version++
			if err != nil {
				a.composer.actionError = err.Error()
			} else {
				a.composer.actionError = ""
				a.agent.revealLatest = true
			}
			a.loadAgentHistory(false)
			a.refreshConversations()
		})
	}
}
func (a *Workspace) dispatchNativeSend(n *nativeSend) error {
	d, err := executeNativeSend(a.service, n, a.composer.workerTokens[n.key])
	a.composer.workerTokens[n.key] = d.Token
	a.update(func() { a.composer.entries[n.key] = d })
	return err
}
func executeNativeSend(svc *app.Service, n *nativeSend, expected string) (draft app.ConversationDraft, err error) {
	draft = n.draft
	// Revalidate the exact conversation binding and endpoint preview. Changed
	// policies/configuration require a new confirmation, never an expanded grant.
	conv, err := svc.GetConversation(n.key)
	if err != nil {
		return draft, err
	}
	if conv.ProjectID != n.conversation.ProjectID || conv.ContextID != n.conversation.ContextID || conv.ConfigID != n.conversation.ConfigID {
		return draft, errors.New("对话绑定或执行配置已变化，请重新确认")
	}
	preview, err := svc.PreviewSend(n.key, len(n.draft.Uploads) > 0, "main")
	if err != nil {
		return draft, err
	}
	if !reflect.DeepEqual(preview, n.consent) {
		return draft, errors.New("发送目的地或数据范围已变化，请重新确认")
	}
	for _, item := range preview {
		if len(item.Missing) > 0 {
			grants, err := svc.ListExfilGrants(conv.ContextID)
			if err != nil {
				return draft, err
			}
			scopes := append([]string{}, item.Scopes...)
			for _, g := range grants {
				if g.Destination == item.Destination {
					for _, scope := range g.Scopes {
						found := false
						for _, s := range scopes {
							if s == scope {
								found = true
							}
						}
						if !found {
							scopes = append(scopes, scope)
						}
					}
				}
			}
			if _, err := svc.GrantExfil(conv.ContextID, item.Destination, scopes); err != nil {
				return draft, err
			}
		}
	}
	d := n.draft
	d.Token = project.NewID("chatdraft")
	d.Dispatching = true
	d.RunID = ""
	if err := svc.SaveConversationDraft(n.key, expected, d); err != nil {
		return draft, err
	}
	draft = d
	run, err := svc.StartConversationTaskForProject(n.key, n.conversation.ProjectID, d.Text, n.selection, d.Uploads...)
	if err != nil {
		return draft, fmt.Errorf("发送未确认成功，输入已保留；请核对任务记录：%w", err)
	}
	consumed := app.ConversationDraft{Token: project.NewID("chatdraft"), RunID: run.ID}
	if err := svc.SaveConversationDraft(n.key, d.Token, consumed); err != nil {
		return draft, fmt.Errorf("任务已创建，但草稿消费记录未确认；请先核对任务：%w", err)
	}
	return consumed, nil
}
func (a *Workspace) unlockComposer() {
	if a.composerBlocked() {
		return
	}
	key := a.composerKey()
	d := a.composer.entries[key]
	a.composer.busy = true
	go func() {
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Title: "确认已核对发送结果", Message: "请先核对本对话最新消息与任务。解锁仅允许重新编辑，不会撤销或重发原任务；再次发送可能创建重复任务。", Buttons: []string{"返回核对", "保留输入并解锁"}})
		a.update(func() {
			a.composer.busy = false
			if err != nil {
				a.composer.actionError = err.Error()
				return
			}
			if answer.Button == 1 {
				d.Dispatching = false
				a.checkpointComposer(key, d)
			}
		})
	}()
}
func (a *Workspace) sendConfirmation(c *ui.Context) {
	n := a.composer.confirmation
	if n == nil || a.closeFrozen {
		return
	}
	if !n.open && !a.composer.busy {
		a.composer.confirmation = nil
		return
	}
	ui.Modal(c, &n.open, func() {
		ui.Text(c, "确认发送").FontSize(18).Bold()
		ui.Scroll(c).Width(540).MaxHeight(460).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "冻结范围："+n.scope).MaxWidth(520)
				ui.Text(c, n.draft.Text).MaxWidth(520)
				for _, u := range n.draft.Uploads {
					ui.Text(c, "附件："+u.Name).MaxWidth(520)
				}
				for _, item := range n.consent {
					ui.Text(c, "实际接收地址："+item.Destination).MaxWidth(520).Bold()
					ui.Text(c, "内容范围："+nativeScopeNames(item.Scopes)).MaxWidth(520)
					if len(item.Missing) > 0 {
						ui.Text(c, "继续将为本对话授权上述范围；仅主动共享权限的对话共用。读取文件等仍需单独允许，可在权限设置撤销。").MaxWidth(520)
					}
					for _, s := range item.Scopes {
						if s == storage.ScopeCredential {
							ui.Text(c, "包括被标记为凭据的敏感材料，请核实接收端可信。").MaxWidth(520).TextColor(c.Theme().Warning)
						}
					}
				}
				ui.Text(c, "确认后启动任务；后续选区切换不改变这次发送的片段。").MaxWidth(520)
			})
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "返回修改").Disabled(a.composer.busy).OnClick(func() { a.composer.confirmation = nil })
			ui.Button(c, "允许并发送").Disabled(a.composer.busy).OnClick(func() { a.confirmNativeSend(n) })
		})
	})
}

func nativeScopeNames(scopes []string) string {
	names := map[string]string{"personal": "个人与对话内容", "workspace": "工程与工作区资料", "unknown": "来源未分类的材料", "credential": "凭据等敏感内容", "public": "公开资料"}
	labels := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if label, ok := names[s]; ok {
			labels = append(labels, label)
		} else {
			labels = append(labels, s)
		}
	}
	return strings.Join(labels, "、")
}
