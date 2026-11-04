package desktop

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/storage"
)

type nativeSkills struct {
	open, busy                         bool
	session, messageID                 string
	note, request, source, err         string
	drafts                             []storage.SkillDraft
	feedback                           []storage.FeedbackNote
	evaluations                        []storage.SkillEvaluation
	catalog                            []app.SkillInfo
	selectedFeedback, selectedEvidence map[string]bool
	runs                               []storage.Run
	selected                           *storage.SkillDraft
	proposal                           *nativeForm
	evalSkill, evalOutcome, evalNote   string
	runID                              string
}

func (a *Workspace) openSkillPanel() {
	if a.skillPanel != nil && (a.skillPanel.busy || a.skillPanel.request != "" || a.skillPanel.note != "" || a.skillPanel.proposal != nil && a.skillPanel.proposal.dirty) {
		a.skillPanel.open = true
		return
	}
	d := &nativeSkills{open: true, session: a.agent.selected, selectedFeedback: map[string]bool{}, selectedEvidence: map[string]bool{}, evalOutcome: "worked"}
	a.skillPanel = d
	a.reloadSkills(d)
}
func (a *Workspace) skillAction(d *nativeSkills, run func() error, done func()) {
	if d.busy || a.closeFrozen {
		return
	}
	d.busy = true
	go func() {
		err := run()
		a.update(func() {
			d.busy = false
			if err != nil {
				d.err = err.Error()
				return
			}
			d.err = ""
			if done != nil {
				done()
			}
		})
	}()
}
func (a *Workspace) reloadSkills(d *nativeSkills) {
	var drafts []storage.SkillDraft
	var feedback []storage.FeedbackNote
	var catalog []app.SkillInfo
	var runs []storage.Run
	a.skillAction(d, func() error {
		var err error
		drafts, err = a.service.ListSkillDrafts()
		if err != nil {
			return err
		}
		catalog, err = a.service.ListSkills()
		if err != nil {
			return err
		}
		if d.session != "" {
			feedback, err = a.service.ListFeedback(d.session)
			if err != nil {
				return err
			}
			runs, err = a.service.ListRuns(d.session)
		}
		return err
	}, func() { d.drafts = drafts; d.feedback = feedback; d.catalog = catalog; d.runs = runs })
}
func ensureNativeConsent(svc *app.Service, win *mygo.Window, session, role string, attachments bool) error {
	preview, err := svc.PreviewSend(session, attachments, role)
	if err != nil {
		return err
	}
	conv, err := svc.GetConversation(session)
	if err != nil {
		return err
	}
	for _, item := range preview {
		if len(item.Missing) == 0 {
			continue
		}
		yes, err := confirmNative(win, "允许发送这些材料？", item.Destination+"\n范围："+nativeScopeNames(item.Scopes)+"\n授权用于本对话及主动共享权限的对话；文件读取等仍单独授权。")
		if err != nil {
			return err
		}
		if !yes {
			return errors.New("已取消发送；输入仍保留")
		}
		grants, err := svc.ListExfilGrants(conv.ContextID)
		if err != nil {
			return err
		}
		scopes := append([]string{}, item.Scopes...)
		for _, grant := range grants {
			if grant.Destination == item.Destination {
				scopes = append(scopes, grant.Scopes...)
			}
		}
		if _, err = svc.GrantExfil(conv.ContextID, item.Destination, scopes); err != nil {
			return err
		}
	}
	return nil
}
func (a *Workspace) skillDialog(c *ui.Context) {
	d := a.skillPanel
	if d == nil || !d.open || a.closeFrozen {
		return
	}
	ui.Modal(c, &d.open, func() {
		ui.Text(c, "反馈与工作方法").FontSize(18).Bold()
		ui.Scroll(c).Width(780).MaxHeight(550).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				if d.session != "" {
					ui.Text(c, "记录反馈（只属于打开此面板时的对话）").Bold()
					if d.messageID == "" {
						ui.Text(c, "请从对话中的用户消息点击“记录反馈”，再填写补充说明。").MaxWidth(750)
					}
					ui.TextArea(c, &d.note).FillWidth().Height(70).Label("反馈原话").Disabled(d.busy)
					ui.Button(c, "记录这条反馈").Disabled(d.busy || d.messageID == "").OnClick(func() {
						session, message, note := d.session, d.messageID, d.note
						a.skillAction(d, func() error { _, err := a.service.RecordFeedback(session, message, note); return err }, func() { d.note = ""; a.reloadSkills(d) })
					})
					for _, feedback := range d.feedback {
						ui.Column(c.Key(feedback.ID)).FillWidth().Children(func() {
							ui.Text(c, feedback.Note+" · "+feedback.State).MaxWidth(750)
							if feedback.State == "recorded" {
								on := d.selectedFeedback[feedback.ID]
								ui.Checkbox(c, &on, "用于本次方法提炼").Disabled(d.busy).OnChange(func() { d.selectedFeedback[feedback.ID] = on })
								ui.Button(c, "取消此反馈的提炼资格").Disabled(d.busy).OnClick(func() {
									session, id := d.session, feedback.ID
									a.skillAction(d, func() error { return a.service.DismissFeedback(session, id) }, func() { delete(d.selectedFeedback, id); a.reloadSkills(d) })
								})
							}
						})
					}
					ui.Text(c, "提炼可复用方法").Bold()
					names := []string{""}
					for _, s := range d.catalog {
						names = append(names, s.Name)
					}
					nativeSelect(c, "参考已安装方法（可留空）", &d.source, names, d.busy, nil)
					ui.TextArea(c, &d.request).FillWidth().Height(90).Label("方法改进要求").Disabled(d.busy)
					ui.Text(c, "仅显式选中的任务证据和反馈会用于生成；不会自动读取其他对话。").MaxWidth(750)
					for _, run := range d.runs {
						on := d.selectedEvidence[run.ID]
						ui.Checkbox(c, &on, run.CreatedAt+" · "+run.Status+" · 作为证据").Disabled(d.busy).OnChange(func() { d.selectedEvidence[run.ID] = on })
					}
					ui.Button(c, "生成方法草案").Disabled(d.busy || strings.TrimSpace(d.request) == "").OnClick(func() {
						session, source, request := d.session, d.source, d.request
						var evidence, feedback []string
						for id, on := range d.selectedFeedback {
							if on {
								feedback = append(feedback, id)
							}
						}
						for id, on := range d.selectedEvidence {
							if on {
								evidence = append(evidence, "run:"+id)
							}
						}
						var run storage.Run
						a.skillAction(d, func() error {
							if err := ensureNativeConsent(a.service, a.win, session, "improver", false); err != nil {
								return err
							}
							var err error
							run, err = a.service.StartSkillDraft(session, source, request, evidence, feedback)
							return err
						}, func() { d.runID = run.ID; d.request = ""; a.loadAgentHistory(false); a.reloadSkills(d) })
					})
					if d.runID != "" {
						ui.Button(c, "刷新生成状态与草案").Disabled(d.busy).OnClick(func() { a.reloadSkills(d) })
						ui.Button(c, "停止草案生成").Disabled(d.busy).OnClick(func() { id := d.runID; a.skillAction(d, func() error { return a.service.CancelTask(id) }, nil) })
					}
				} else {
					ui.Text(c, "选择对话后可记录反馈、提炼方法；已有草案管理仍可用。").MaxWidth(750)
				}
				ui.Text(c, "方法草案与采用记录").Bold()
				for _, item := range d.drafts {
					ui.Button(c, item.Proposal.Summary+fmt.Sprintf(" · %s · r%d", item.State, item.Revision)).FillWidth().Disabled(d.busy || d.proposal != nil && d.proposal.dirty).OnClick(func() { next := cloneNative(item); d.selected = &next; d.proposal = newNativeForm(&next.Proposal) })
				}
				if item := d.selected; item != nil {
					ui.Text(c, item.Proposal.Summary).MaxWidth(750)
					ui.Text(c, "来源与影响（只读）\n"+prettyDetail(struct {
						Evidence  []storage.DraftEvidence
						Feedback  []storage.FeedbackNote
						Impact    []storage.ConfigReference
						AllSkills []storage.ConfigReference
					}{item.Evidence, item.Feedback, item.Impact, item.AllSkillsImpact})).MaxWidth(750).FontSize(11)
					if d.proposal != nil {
						d.proposal.draw(c, d.busy)
					}
					ui.Row(c).Gap(8).Children(func() {
						if item.State == "draft" {
							ui.Button(c, "保存草案修订").Disabled(d.busy || len(d.proposal.errors) > 0).OnClick(func() {
								copy := cloneNative(*item)
								var next storage.SkillDraft
								a.skillAction(d, func() error {
									var err error
									next, err = a.service.EditSkillDraft(copy.ID, copy.Revision, copy.Proposal)
									return err
								}, func() { d.selected = &next; d.proposal = newNativeForm(&next.Proposal); a.reloadSkills(d) })
							})
							ui.Button(c, "采用为独立方法").Disabled(d.busy || d.proposal.dirty).OnClick(func() {
								copy := cloneNative(*item)
								a.skillAction(d, func() error {
									yes, err := confirmNative(a.win, "采用工作方法", "将发布独立副本，不覆盖源方法。请核对正文、隐私、所列影响及适用限制；仅影响后续运行。")
									if err != nil || !yes {
										return err
									}
									_, err = a.service.ApplySkillDraft(copy.ID, copy.Revision)
									return err
								}, func() { d.selected = nil; d.proposal = nil; a.reloadSkills(d) })
							})
							ui.Button(c, "丢弃草案").Disabled(d.busy).OnClick(func() {
								id, revision := item.ID, item.Revision
								a.skillAction(d, func() error { return a.service.DiscardSkillDraft(id, revision) }, func() { d.selected = nil; d.proposal = nil; a.reloadSkills(d) })
							})
						}
						if item.State == "applied" {
							ui.Button(c, "撤销采用并回退引用").Disabled(d.busy).OnClick(func() {
								id, revision := item.ID, item.Revision
								a.skillAction(d, func() error {
									yes, err := confirmNative(a.win, "撤销采用", "撤回已发布方法并按服务检查恢复受影响引用；在途运行保留冻结版本。")
									if err != nil || !yes {
										return err
									}
									return a.service.WithdrawSkillDraft(id, revision)
								}, func() { d.selected = nil; d.proposal = nil; a.reloadSkills(d) })
							})
						}
						ui.Button(c, "放弃本次草案编辑").Disabled(d.busy).OnClick(func() { d.selected = nil; d.proposal = nil })
					})
				}
				names := []string{""}
				for _, s := range d.catalog {
					names = append(names, s.Name)
				}
				nativeSelect(c, "评价已安装方法", &d.evalSkill, names, d.busy, nil)
				nativeSelect(c, "使用评价", &d.evalOutcome, []string{"worked", "partial", "failed"}, d.busy, nil)
				ui.TextArea(c, &d.evalNote).FillWidth().Height(60).Label("评价依据").Disabled(d.busy)
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "记录方法评价").Disabled(d.busy || d.evalSkill == "" || d.session == "").OnClick(func() {
						name, session, outcome, note := d.evalSkill, d.session, d.evalOutcome, d.evalNote
						a.skillAction(d, func() error { _, err := a.service.RecordSkillEvaluation(name, session, "", outcome, note); return err }, func() { d.evalNote = "" })
					})
					ui.Button(c, "读取评价历史").Disabled(d.busy || d.evalSkill == "").OnClick(func() {
						name := d.evalSkill
						var items []storage.SkillEvaluation
						a.skillAction(d, func() error { var err error; items, err = a.service.ListSkillEvaluations(name); return err }, func() { d.evaluations = items })
					})
				})
				for _, evaluation := range d.evaluations {
					ui.Text(c, fmt.Sprintf("%s · %s · %s", evaluation.CreatedAt, evaluation.Outcome, evaluation.Note)).MaxWidth(750)
				}
			})
		})
		if d.err != "" {
			ui.Text(c, d.err).MaxWidth(760).TextColor(c.Theme().Warning)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "刷新列表").Disabled(d.busy).OnClick(func() { a.reloadSkills(d) })
			ui.Button(c, "返回工作区").Disabled(d.busy).OnClick(func() { d.open = false })
		})
	})
}
