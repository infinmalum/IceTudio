package desktop

import "github.com/egoist/mygo/ui"

// Side panels yield before the music canvas. Automatic collapse never changes
// the persisted preference, so returning to a wide window restores the layout.
func editorPanels(width float32, p nativePreferences) (bool, bool) {
	browser, agent := !p.HideBrowser, !p.HideAgent
	if width < 1200 {
		browser = false
	}
	if width < 960 {
		agent = false
	}
	return browser, agent
}
func (a *Workspace) layoutTools(c *ui.Context) {
	ui.Button(c, "浏览器").OnClick(func() {
		width, _ := c.Size()
		if width < 1200 {
			a.browserDrawer = true
			return
		}
		a.preferences.HideBrowser = !a.preferences.HideBrowser
		a.savePreferences()
	})
	ui.Button(c, "助手面板").OnClick(func() {
		width, _ := c.Size()
		if width < 960 {
			a.agentDrawer = true
			return
		}
		a.preferences.HideAgent = !a.preferences.HideAgent
		a.showAgent = !a.preferences.HideAgent
		a.savePreferences()
	})
	views := [...]string{"arrangement", "split", "piano", "mixer"}
	selected := 1
	for i, view := range views {
		if a.preferences.View == view {
			selected = i
			break
		}
	}
	ui.Segmented(c, &selected, "编排", "编排 + 卷帘", "钢琴卷帘", "混音").Label("编辑器布局").OnChange(func() {
		a.cancelArrangement()
		a.preferences.View = views[selected]
		a.savePreferences()
	})
	ui.Button(c, "快捷键").OnClick(func() { a.showKeyHelp = true })
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
	editorShare := max(100-420/mainWidth*100, min(100-280/mainWidth*100, a.preferences.EditorShare))
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
		ui.Text(c, "钢琴卷帘").Height(24).FontSize(12).Bold()
		a.pianoTools(c)
		if a.piano != nil {
			ui.WebView(c, a.piano).Grow(1).MinHeight(0).Border(1, t.Border).Radius(5)
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
	if a.browserDrawer {
		ui.Modal(c, &a.browserDrawer, func() {
			ui.Column(c).Size(320, 480).Children(func() { a.browser(c) })
			ui.Button(c, "关闭浏览器").OnClick(func() { a.browserDrawer = false })
		})
	}
	if a.agentDrawer {
		ui.Modal(c, &a.agentDrawer, func() {
			ui.Column(c).Size(360, 500).Children(func() { a.agentPanel(c) })
			ui.Button(c, "关闭助手").OnClick(func() { a.agentDrawer = false })
		})
	}
}
