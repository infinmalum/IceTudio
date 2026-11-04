package desktop

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/materials"
	"icetudio/internal/storage"
)

type nativeSearch struct {
	open, busy   bool
	session, err string
	conv         storage.Conversation
	availability app.SearchAvailability
	records      []storage.SearchRequest
	suggestions  *mygo.WebView
}
type nativeAttachment struct {
	open, busy bool
	file       materials.File
	err        string
	media      *mygo.WebView
	image      ui.ImageSource
}

func (a *Workspace) openSearch() {
	if a.search != nil && a.search.busy {
		a.search.open = true
		return
	}
	if a.agent.selected == "" || a.closeFrozen {
		return
	}
	d := &nativeSearch{open: true, busy: true, session: a.agent.selected}
	a.search = d
	go func() {
		var err error
		dconv, e := a.service.GetConversation(d.session)
		if e != nil {
			err = e
		}
		availability, e := a.service.ConversationSearchAvailability(d.session)
		if e != nil {
			err = e
		}
		records, e := a.service.ListConversationSearch(d.session)
		if e != nil {
			err = e
		}
		a.update(func() {
			d.busy = false
			d.conv = dconv
			d.availability = availability
			d.records = records
			if err != nil {
				d.err = err.Error()
			}
		})
	}()
}
func safeWebLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
func (a *Workspace) searchDialog(c *ui.Context) {
	d := a.search
	if d == nil || a.closeFrozen {
		return
	}
	if !d.open {
		if d.suggestions != nil {
			d.suggestions.Destroy()
			d.suggestions = nil
		}
		a.search = nil
		return
	}
	ui.Modal(c, &d.open, func() {
		ui.Text(c, "联网与参考来源").FontSize(18).Bold()
		ui.Scroll(c).Width(740).MaxHeight(510).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, d.availability.Reason).MaxWidth(720)
				on := d.conv.Options.WebSearch
				ui.Checkbox(c, &on, "对话后续任务启用联网").Disabled(d.busy || !d.availability.Available && !on).OnChange(func() {
					session, options := d.session, storage.ConversationOptions{WebSearch: on}
					d.busy = true
					go func() {
						conv, err := a.service.SetConversationOptions(session, options)
						a.update(func() {
							d.busy = false
							if err != nil {
								d.err = err.Error()
								return
							}
							d.conv = conv
							a.refreshConversations()
						})
					}()
				})
				for _, record := range d.records {
					ui.Column(c.Key(record.ID)).FillWidth().Gap(4).Children(func() {
						ui.Text(c, record.Provider+" · "+record.CreatedAt+" · "+record.State).Bold()
						ui.Text(c, record.Report.Text).MaxWidth(720)
						if record.Error != "" {
							ui.Text(c, record.Error).MaxWidth(720)
						}
						for _, ref := range record.Report.Citations {
							if safeWebLink(ref.URL) {
								ui.Button(c, ref.Title+" · "+ref.URL).FillWidth().OnClick(func() { c.OpenURL(ref.URL) })
							}
						}
					})
				}
				suggestions := []string{}
				for _, r := range d.records {
					suggestions = append(suggestions, r.Report.Suggestions...)
				}
				if len(suggestions) > 0 {
					ui.Text(c, "供应商提供的搜索建议（只读正文）").Bold()
					ui.Button(c, "显示搜索建议").Disabled(d.suggestions != nil || d.busy).OnClick(func() {
						view, err := a.readOnlyView()
						if err != nil {
							d.err = err.Error()
							return
						}
						d.suggestions = view
						go func() {
							markup := ""
							for _, s := range suggestions {
								markup += "<iframe sandbox='allow-popups' referrerpolicy='no-referrer' srcdoc=\"" + html.EscapeString("<meta http-equiv='Content-Security-Policy' content=\"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'\">"+s) + "\"></iframe>"
							}
							if err := setReadOnlyBody(view, markup); err != nil {
								a.update(func() { d.err = err.Error() })
							}
						}()
					})
					if d.suggestions != nil {
						ui.WebView(c, d.suggestions).Width(710).Height(220)
					}
				}
			})
		})
		if d.err != "" {
			ui.Text(c, d.err).MaxWidth(720).TextColor(c.Theme().Warning)
		}
		ui.Button(c, "返回对话").Disabled(d.busy).OnClick(func() { d.open = false })
	})
}
func (a *Workspace) openAttachment(message string, index int) {
	if a.attachment != nil && a.attachment.busy {
		a.attachment.open = true
		return
	}
	if a.closeFrozen {
		return
	}
	d := &nativeAttachment{open: true, busy: true}
	a.attachment = d
	go func() {
		f, err := a.service.MessageAttachment(message, index)
		a.update(func() {
			d.busy = false
			if err != nil {
				d.err = err.Error()
				return
			}
			d.file = f
			if f.MediaType != "image/gif" && strings.HasPrefix(f.MediaType, "image/") {
				raw, e := base64.StdEncoding.DecodeString(f.Data)
				if e == nil {
					d.image, e = ui.DecodeImage(raw)
				}
				if e != nil {
					d.err = e.Error()
				}
			}
		})
	}()
}
func (a *Workspace) attachmentDialog(c *ui.Context) {
	d := a.attachment
	if d == nil || a.closeFrozen {
		return
	}
	if !d.open {
		if d.media != nil {
			d.media.Destroy()
			d.media = nil
		}
		a.attachment = nil
		return
	}
	ui.Modal(c, &d.open, func() {
		ui.Text(c, "附件 · "+d.file.Name).FontSize(18).Bold()
		ui.Scroll(c).Width(740).MaxHeight(500).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				if d.file.Text != "" {
					ui.Text(c, d.file.Text).MaxWidth(720)
				}
				if d.image != nil {
					ui.Image(c, d.image).Width(700).Height(400)
				}
				if d.file.Data != "" && d.image == nil {
					ui.Button(c, "显示本地附件内容").Disabled(d.busy || d.media != nil).OnClick(func() {
						view, err := a.readOnlyView()
						if err != nil {
							d.err = err.Error()
							return
						}
						d.media = view
						f := d.file
						go func() {
							src := "data:" + f.MediaType + ";base64," + f.Data
							markup := ""
							if strings.HasPrefix(f.MediaType, "image/") {
								markup = "<img alt='本地附件' src='" + src + "'>"
							} else if f.MediaType == "application/pdf" {
								markup = "<object type='application/pdf' data='" + src + "'><p>当前 WebView 不支持 PDF 预览，请保存后查看。</p></object>"
							}
							if err := setReadOnlyBody(view, markup); err != nil {
								a.update(func() { d.err = err.Error() })
							}
						}()
					})
					if d.media != nil {
						ui.WebView(c, d.media).Width(710).Height(380)
					}
				}
			})
		})
		if d.err != "" {
			ui.Text(c, d.err).MaxWidth(720).TextColor(c.Theme().Warning)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "保存附件").Disabled(d.busy || d.file.Name == "").OnClick(func() {
				f := d.file
				d.busy = true
				go func() {
					name := f.Name
					if f.MediaType == "text/plain" && strings.HasSuffix(strings.ToLower(name), ".docx") {
						name += ".txt"
					}
					path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "保存附件", DefaultPath: name})
					if err == nil && path != "" {
						b := []byte(f.Text)
						if f.Data != "" {
							b, err = base64.StdEncoding.DecodeString(f.Data)
						}
						if err == nil {
							err = os.WriteFile(path, b, 0600)
						}
					}
					a.update(func() {
						d.busy = false
						if err != nil {
							d.err = err.Error()
						}
					})
				}()
			})
			ui.Button(c, "返回对话").Disabled(d.busy).OnClick(func() { d.open = false })
		})
	})
}
func setReadOnlyBody(view *mygo.WebView, markup string) error {
	// Eval only installs trusted host-generated markup. Untrusted suggestion HTML
	// is inside a sandboxed iframe with scripts/network/storage disabled.
	return retryReadOnly(view, fmt.Sprintf("document.querySelector('main').innerHTML=%q", markup))
}

func retryReadOnly(view *mygo.WebView, script string) error {
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		value, err := view.Page().EvalContext(ctx, "document.querySelector('main') ? ("+script+",true) : false")
		cancel()
		if err == nil && value == true {
			return nil
		}
		if view.IsDestroyed() {
			return fmt.Errorf("只读正文已关闭")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("只读正文未完成加载")
}
