package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

// Side panels yield before the music canvas. Automatic collapse never changes
// the persisted preference, so returning to a wide window restores the layout.
func editorPanels(width float32, p nativePreferences) (bool, bool) {
	browser, agent := !p.HideBrowser, !p.HideAgent
	if width < 1200 && (agent || width < 820) {
		browser = false
	}
	if width < 960 {
		agent = false
	}
	return browser, agent
}
func (a *Workspace) layoutTools(c *ui.Context) {
	width, _ := c.Size()
	toggleBrowser := func() {
		if width < 1200 && (!a.preferences.HideAgent || width < 820) {
			a.browserDrawer = true
			return
		}
		a.preferences.HideBrowser = !a.preferences.HideBrowser
		a.savePreferences()
	}
	toggleAgent := func() {
		if width < 960 {
			a.agentDrawer = true
			return
		}
		a.preferences.HideAgent = !a.preferences.HideAgent
		a.savePreferences()
	}
	if width < 700 {
		ui.PopoverMenuButton(c, "面板", func(m *ui.Menu) {
			if m.Item("浏览器").Chosen() {
				toggleBrowser()
			}
			if m.Item("助手面板").Chosen() {
				toggleAgent()
			}
		})
	} else {
		ui.Button(c, "浏览器").OnClick(toggleBrowser)
		ui.Button(c, "助手面板").OnClick(toggleAgent)
	}
	views := [...]string{"arrangement", "split", "piano", "mixer", "mix-split"}
	selected := 0
	for i, view := range views {
		if a.preferences.View == view {
			selected = i
			break
		}
	}
	labels := []string{"编排", "编排 + 卷帘", "钢琴卷帘", "混音", "编排 + 混音"}
	setView := func(index int) {
		a.cancelArrangement()
		a.preferences.View = views[index]
		a.savePreferences()
	}
	if width < 800 {
		ui.PopoverMenuButton(c, labels[selected], func(m *ui.Menu) {
			for i, label := range labels {
				if m.Item(label).Radio(i == selected).Chosen() {
					setView(i)
				}
			}
		}).Label("编辑器布局")
		return
	}
	ui.Segmented(c, &selected, labels...).Label("编辑器布局").OnChange(func() { setView(selected) })
}
func (a *Workspace) workspacePanes(c *ui.Context, t *ui.Theme) {
	width, _ := c.Size()
	logical := width
	browser, agent := editorPanels(logical, a.preferences)
	browserWidth := max(float32(180), min(320, a.preferences.BrowserWidth))
	mainWidth := logical
	if browser {
		mainWidth -= browserWidth
	}
	agentMax := min(float32(420), max(float32(280), mainWidth-600))
	editorShare := max(100-agentMax/mainWidth*100, min(100-280/mainWidth*100, a.preferences.EditorShare))
	body := func() {
		if agent {
			ui.SplitPercent(c, &editorShare, func() { a.editorView(c, t) }, func() { a.agentPanel(c) }).Fill().OnChange(func() { a.preferences.EditorShare = editorShare; a.savePreferences() })
		} else {
			a.editorView(c, t)
		}
	}
	if browser {
		ui.Split(c, &browserWidth, func() { a.browser(c) }, body).Grow(1).MinHeight(0).OnChange(func() { a.preferences.BrowserWidth = browserWidth; a.savePreferences() })
	} else {
		ui.Box(c).Grow(1).MinHeight(0).Children(body)
	}
}
func (a *Workspace) pianoPanel(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).FillWidth().Padding(4, 8).Gap(3).Background(t.Surface).Children(func() {
			title := "钢琴卷帘"
			if clip := a.preview.Context().Title; clip != "" {
				title += "  /  " + clip
			}
			ui.Text(c, title).FontSize(12).Bold().SingleLine().MinWidth(0).Tooltip(title)
			a.pianoTools(c)
		})
		if a.piano != nil {
			ui.WebView(c, a.piano).Grow(1).MinHeight(0).Border(1, t.Border).Radius(5).DrawOver(a.syncPianoViewport)
		}
	})
}
func (a *Workspace) keyboardHelp(c *ui.Context) {
	if !a.showKeyHelp {
		return
	}
	ui.Modal(c, &a.showKeyHelp, func() {
		ui.Text(c, "编辑器快捷键").FontSize(18).Bold()
		ui.Textf(c, "当前工程：%s · 所选片段：%s；手工操作和助手编辑共享撤销。", a.active.Title, a.preview.Context().Title).MaxWidth(520)
		ui.Text(c, "音乐画布获得焦点时生效；输入框和输入法组合中保留文字操作。").MaxWidth(520)
		for _, line := range []string{"Space：播放 / 暂停；Home：回到开头", "Ctrl / Cmd Z：撤销；Shift Ctrl / Cmd Z：重做", "Ctrl / Cmd A：全选；Ctrl / Cmd D：清除选择", "Ctrl / Cmd C：复制；Ctrl / Cmd V：在当前插入位置粘贴", "Ctrl / Cmd B：紧邻复制；Delete / Backspace：删除音符", "方向键：移动音符；Shift 框选：相交选中", "Escape：取消当前手势；Alt：本次手势关闭吸附", "Shift 滚轮 / 触控板横向滚动：连续导航；Ctrl 滚轮：锚点缩放", "手动导航暂停播放跟随；点击恢复跟随继续"} {
			ui.Text(c, line).FontSize(12)
		}
		ui.Button(c, "关闭快捷键").OnClick(func() { a.showKeyHelp = false })
	})
}

func validEditorKeys(p nativePreferences) bool {
	valid := func(s string) bool {
		return s == "" || s == "Q" || s == "W" || s == "E" || s == "R" || s == "T" || s == "B" || s == "L"
	}
	return valid(p.QuantizeKey) && valid(p.DuplicateKey) && (p.QuantizeKey == "" || p.DuplicateKey == "" || p.QuantizeKey != p.DuplicateKey)
}

func (a *Workspace) editorDrawers(c *ui.Context) {
	width, height := c.Size()
	if a.browserDrawer {
		ui.Modal(c, &a.browserDrawer, func() {
			ui.Column(c).Size(min(320, width-64), min(520, height-92)).Gap(8).Children(func() {
				ui.Box(c).FillWidth().Grow(1).MinHeight(0).Children(func() { a.browser(c) })
				ui.Button(c, "关闭浏览器").FillWidth().OnClick(func() { a.browserDrawer = false })
			})
		})
	}
	if a.agentDrawer {
		ui.Modal(c, &a.agentDrawer, func() {
			ui.Column(c).Size(min(360, width-64), min(560, height-92)).Gap(8).Children(func() {
				ui.Box(c).FillWidth().Grow(1).MinHeight(0).Children(func() { a.agentPanel(c) })
				ui.Button(c, "关闭助手").FillWidth().OnClick(func() { a.agentDrawer = false })
			})
		})
	}
}

// The global desk owns navigation and transport. Editor panes start at their
// own tool row and can be swapped without moving the transport.
func (a *Workspace) workspaceHeader(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	title := a.active.Title
	if title == "" {
		title = "未选择工程"
	}
	ui.Column(c).FillWidth().Background(t.Surface).Children(func() {
		ui.Row(c).FillWidth().Padding(4, 8).Wrap().AlignItems(ui.Center).Gap(5).Children(func() {
			ui.Text(c, "IceTudio").FontSize(14).Bold()
			ui.PopoverMenuButton(c, "工作区", func(m *ui.Menu) {
				if m.Item("刷新工程").Disabled(a.service == nil || a.editBusy || a.closeFrozen || a.form != nil || a.mapForm != nil || a.fileBusy).Chosen() {
					a.refresh()
				}
				if m.Item("设置").Disabled(a.service == nil || a.closeFrozen).Chosen() {
					a.openSettings("模型")
				}
				if m.Item("编辑器快捷键").Chosen() {
					a.showKeyHelp = true
				}
			}).Height(27)
			a.layoutTools(c)
			if width >= 900 {
				ui.Text(c, title).MinWidth(80).Grow(1).SingleLine().TextAlign(ui.End).FontSize(12).Bold().Tooltip(title)
			}
		})
		ui.Box(c).Height(1).FillWidth().Background(t.Border)
		ui.Row(c).FillWidth().Wrap().Padding(4, 8).Gap(6).AlignItems(ui.Center).Children(func() {
			a.transport(c)
		})
		ui.Box(c).Height(1).FillWidth().Background(t.Border)
	})
}

func (a *Workspace) workspaceStatus(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).FillWidth().Height(24).Padding(3, 10).Gap(10).Background(t.Surface).Children(func() {
		if a.active.ID != "" {
			detail := fmt.Sprintf("%s · %d PPQ · 修订 %d", a.active.Title, a.active.PPQ, a.active.Revision)
			ui.Text(c, detail).MaxWidth(270).Shrink(1).SingleLine().FontSize(11).TextColor(t.TextMuted).Tooltip(detail)
		}
		ui.Text(c, a.message).FontSize(11).TextColor(t.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(a.message)
	})
}

func (a *Workspace) editorMode(height float32) string {
	switch a.preferences.View {
	case "split":
		if height < 640 {
			return "piano"
		}
	case "mix-split":
		if height < 720 {
			return "mixer"
		}
	case "":
		return "arrangement"
	}
	return a.preferences.View
}

func (a *Workspace) arrangementVisible(height float32) bool {
	view := a.editorMode(height)
	return view == "arrangement" || view == "split" || view == "mix-split"
}
