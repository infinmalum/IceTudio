package desktop

import (
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"icetudio/internal/app"
	"icetudio/internal/storage"
)

// Agent UI projections only. Runtime roles, scheduling and permissions remain
// owned by the existing app service. History, composer and task actions delegate to the same service; pending
// approvals retain their old carrier until their Native migration is validated.
type agentHistory struct {
	conversations  []storage.Conversation
	selected       string
	messages       []storage.Message
	runs           []storage.Run
	calls          []storage.ToolCall
	before         int
	hasMore        bool
	loading, dirty bool
	generation     uint64
	listSequence   uint64
	err, stream    string
	details        *nativeTaskDetails
	revealLatest   bool
}
type nativeTaskDetails struct {
	input, actionError string
	actionBusy         bool
	open               bool
	run                storage.Run
	graph              storage.TaskGraph
	reviews            []storage.TaskReview
	retrospectives     []storage.TaskRetrospective
	workers            []storage.Run
	traces             []storage.WorkerTrace
	summary            storage.SessionSummary
	plan               app.SessionPlanView
	errors             []string
	diagnostic         string
}

func mergeChatMessages(old, next []storage.Message) []storage.Message {
	if len(next) == 0 {
		return old
	}
	out := make([]storage.Message, 0, len(old)+len(next))
	for _, m := range old {
		if m.Seq < next[0].Seq {
			out = append(out, m)
		}
	}
	return append(out, next...)
}
func (a *Workspace) refreshConversations() {
	if a.service == nil {
		return
	}
	a.agent.listSequence++
	request := a.agent.listSequence
	go func() {
		items, err := a.service.ListConversations()
		a.update(func() {
			if request != a.agent.listSequence {
				return
			}
			if err != nil {
				a.agent.err = err.Error()
			} else {
				a.agent.conversations = items
				if a.agent.selected == "" && a.preferences.LastConversation != "" {
					for _, item := range items {
						if item.ID == a.preferences.LastConversation {
							a.selectConversation(item.ID)
							break
						}
					}
				}
			}
		})
	}()
}
func (a *Workspace) selectConversation(id string) {
	if a.composer.busy || a.composer.confirmation != nil {
		return
	}
	if a.agent.selected == id {
		return
	}
	a.agent.generation++
	a.agent.selected = id
	if a.preferences.LastConversation != id {
		a.preferences.LastConversation = id
		a.savePreferences()
	}
	a.agent.messages = nil
	a.agent.runs = nil
	a.agent.calls = nil
	a.agent.stream = ""
	a.agent.before = 0
	a.agent.hasMore = false
	a.agent.loading = false
	a.agent.dirty = false
	a.agent.details = nil
	a.loadAgentHistory(false)
}
func (a *Workspace) loadAgentHistory(earlier bool) {
	h := &a.agent
	if a.service == nil || h.selected == "" {
		return
	}
	if h.loading {
		h.dirty = true
		return
	}
	h.loading = true
	id, generation, before := h.selected, h.generation, 0
	if earlier {
		before = h.before
	}
	go func() {
		page, err := a.service.ListChatMessages(id, before, 80)
		runs, runErr := a.service.ListRuns(id)
		calls, callErr := a.service.ListToolCalls(id)
		if conv, e := a.service.GetConversation(id); e == nil {
			if pending, e := a.service.PendingApprovals(conv.ContextID); e == nil {
				for _, call := range calls {
					if call.State != storage.CallPending && call.State != storage.CallExecutionUnknown {
						pending = append(pending, call)
					}
				}
				calls = pending
			} else {
				callErr = e
			}
		} else {
			callErr = e
		}
		stream := ""
		for _, r := range runs {
			if r.ParentRunID == "" && (r.Status == "running" || r.Status == "pending") {
				stream, _ = a.service.PendingStreamText(r.ID)
				break
			}
		}
		a.update(func() {
			if a.agent.selected != id || a.agent.generation != generation {
				return
			}
			h.loading = false
			if err != nil {
				h.err = err.Error()
			} else {
				if earlier {
					h.messages = append(page.Messages, h.messages...)
					h.before = page.BeforeSeq
					h.hasMore = page.HasMore
				} else {
					h.messages = mergeChatMessages(h.messages, page.Messages)
					if h.before == 0 {
						h.before = page.BeforeSeq
						h.hasMore = page.HasMore
					}
				}
				h.err = ""
			}
			if runErr != nil {
				h.err += " 任务：" + runErr.Error()
			} else {
				h.runs = runs
			}
			if callErr != nil {
				h.err += " 工具：" + callErr.Error()
			} else {
				h.calls = calls
				if d := a.approval; d != nil && !d.busy {
					for _, call := range calls {
						if call.ID == d.call.ID && call.State != storage.CallPending && call.State != storage.CallExecutionUnknown {
							d.open = false
						}
					}
				}
			}
			h.stream = stream
			if h.dirty {
				h.dirty = false
				a.loadAgentHistory(false)
			}
		})
	}()
}
func (a *Workspace) showTaskDetails(run storage.Run) {
	if a.agent.details != nil && (a.agent.details.actionBusy || a.agent.details.input != "") {
		a.agent.details.open = true
		return
	}
	id, generation := a.agent.selected, a.agent.generation
	go func() {
		d := &nativeTaskDetails{open: true, run: run}
		add := func(label string, err error) {
			if err != nil {
				d.errors = append(d.errors, label+"："+err.Error())
			}
		}
		var err error
		d.graph, err = a.service.GetTaskGraph(run.ID)
		add("任务图", err)
		d.reviews, err = a.service.ListTaskReviews(run.ID)
		add("审查", err)
		d.retrospectives, err = a.service.ListTaskRetrospectives(run.ID)
		add("复盘", err)
		d.workers, err = a.service.ListChildRuns(run.ID)
		add("子任务", err)
		d.traces, err = a.service.ListWorkerTraces(run.ID, "")
		add("执行证据", err)
		d.summary, err = a.service.GetSessionSummary(id)
		add("会话摘要", err)
		d.plan, err = a.service.GetSessionPlan(id)
		add("会话计划", err)
		d.diagnostic = "### 执行诊断\n\n```json\n" + prettyDetail(struct {
			Run            storage.Run
			Graph          storage.TaskGraph
			Reviews        []storage.TaskReview
			Retrospectives []storage.TaskRetrospective
			Workers        []storage.Run
			Evidence       []storage.WorkerTrace
			Summary        storage.SessionSummary
			Plan           app.SessionPlanView
		}{d.run, d.graph, d.reviews, d.retrospectives, d.workers, d.traces, d.summary, d.plan}) + "\n```"
		a.update(func() {
			if a.agent.selected == id && a.agent.generation == generation {
				a.agent.details = d
			}
		})
	}()
}
func (a *Workspace) nativeAgentPanel(c *ui.Context) {
	h := &a.agent
	t := c.Theme()
	ui.Column(c).Fill().Padding(12).Gap(8).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, "创作助手").FontSize(16).Bold().Grow(1)
			ui.Button(c, "联网").Disabled(h.selected == "" || a.closeFrozen).OnClick(a.openSearch)
			ui.Button(c, "最新").OnClick(func() { h.revealLatest = true })
			ui.Button(c, "刷新").OnClick(func() { a.refreshConversations(); a.loadAgentHistory(false) })
		})
		previous := h.selected
		p := ui.SelectBase(c, &h.selected)
		p.Trigger.FillWidth().Padding(6).Background(t.SurfaceHover).Label("选择对话").Children(func() {
			name := "选择已有对话 ▾"
			for _, item := range h.conversations {
				if item.ID == h.selected {
					name = item.Title + " ▾"
				}
			}
			ui.Text(c, name).SingleLine()
		})
		p.Popup(func(panel ui.Element) {
			panel.Padding(4).Background(t.Surface)
			ui.Scroll(c).Width(320).MaxHeight(320).Children(func() {
				for _, item := range h.conversations {
					p.Item(item.ID).Padding(6).Children(func() { ui.Text(c, item.Title).SingleLine() })
				}
				for id, d := range a.composer.entries {
					if id == "new" || d.Text == "" && len(d.Uploads) == 0 && !d.Dispatching {
						continue
					}
					found := false
					for _, v := range h.conversations {
						if v.ID == id {
							found = true
						}
					}
					if !found {
						p.Item(id).Padding(6).Children(func() { ui.Text(c, "保留输入 · 未列出的对话").SingleLine() })
					}
				}
			})
		})
		if p.Trigger.Changed() {
			id := h.selected
			h.selected = previous
			a.selectConversation(id)
		}
		if h.err != "" {
			ui.Text(c, h.err).MaxWidth(296).TextColor(t.Warning)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				if h.hasMore {
					ui.Button(c, "读取更早消息").Disabled(h.loading).OnClick(func() { a.loadAgentHistory(true) })
				}
				for _, m := range h.messages {
					ui.Column(c.Key(m.ID)).FillWidth().Gap(4).Children(func() {
						label := map[string]string{"user": "你", "assistant": "助手", "tool": "工具"}[m.Role]
						if label == "" {
							label = m.Role
						}
						ui.Text(c, label).FontSize(11).TextColor(t.TextMuted)
						if m.Role == "user" {
							ui.Button(c, "记录对这条消息的反馈").OnClick(func() {
								a.openSkillPanel()
								if a.skillPanel.session == a.agent.selected {
									a.skillPanel.messageID = m.ID
								} else {
									a.skillPanel.err = "当前方法面板保留了另一对话的输入，请先处理后再选择反馈"
								}
							})
						}
						markdown.View(c, m.Content, &a.md)
						for index, file := range m.Attachments {
							ui.Button(c, fmt.Sprintf("附件 · %s · %d 字节", file.Name, file.Size)).FillWidth().OnClick(func() { a.openAttachment(m.ID, index) })
						}
					})
				}
				if h.stream != "" {
					ui.Column(c.Key("stream")).FillWidth().Children(func() { ui.Text(c, "正在生成…").FontSize(11); markdown.View(c, h.stream, &a.md) })
				}
				for _, run := range h.runs {
					ui.Button(c.Key(run.ID), "任务 · "+run.Status).FillWidth().OnClick(func() { a.showTaskDetails(run) })
					if run.ParentRunID == "" && (run.Status == "running" || run.Status == "pending") {
						ui.Button(c, "停止此任务").Disabled(a.composerBlocked()).OnClick(func() { a.cancelNativeTask(run) })
					}
				}
				for _, call := range h.calls {
					if call.State == storage.CallPending || call.State == storage.CallExecutionUnknown {
						ui.Button(c.Key(call.ID), "核对此操作 · "+call.ToolName+" · "+call.State).FillWidth().Disabled(a.closeFrozen).OnClick(func() { a.openApproval(call) })
					}
				}
				anchor := ui.Box(c).Height(1)
				if h.revealLatest {
					anchor.ScrollIntoView()
					h.revealLatest = false
				}
				if h.selected == "" {
					ui.Text(c, "选择已有对话以阅读消息与任务记录。").MaxWidth(296)
				}
			})
		})
		a.nativeComposer(c)
		ui.Text(c, "未发送输入自动保留。旧输入可在设置的外观页中恢复。").FontSize(11).MaxWidth(296).TextColor(t.TextMuted)
	})
}
func prettyDetail(value any) string {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(err)
	}
	return string(b)
}
func (a *Workspace) taskDetailsDialog(c *ui.Context) {
	d := a.agent.details
	if d == nil || !d.open || a.closeFrozen {
		return
	}
	ui.Modal(c, &d.open, func() {
		ui.Text(c, "任务详情").FontSize(18).Bold()
		ui.Text(c, d.run.Status+" · "+d.run.CreatedAt)
		ui.Scroll(c).Width(720).MaxHeight(560).Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Text(c, d.graph.Goal).MaxWidth(700).Bold()
				for _, task := range d.graph.Tasks {
					ui.Text(c, task.State+" · "+task.Spec.Task).MaxWidth(700)
					if task.Spec.Acceptance != "" {
						ui.Text(c, "验收："+task.Spec.Acceptance).MaxWidth(700).FontSize(11)
					}
				}
				if d.graph.WaitingPrompt != "" {
					ui.Text(c, "等待补充："+d.graph.WaitingPrompt).MaxWidth(700)
				}
				for _, err := range d.errors {
					ui.Text(c, err).MaxWidth(700).FontSize(11).TextColor(c.Theme().Warning)
				}
				// Raw runtime IDs/evidence stay inside explicit task diagnostics.
				a.taskActions(c, d)
				markdown.View(c, d.diagnostic, &a.md)
			})
		})
		ui.Button(c, "返回工作区").OnClick(func() { d.open = false })
	})
}
