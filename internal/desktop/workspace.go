// Package desktop implements the MyGo Native workspace and embedded Piano Roll.
package desktop

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	pianoweb "icetudio/frontend"
	"icetudio/internal/app"
	"icetudio/internal/playback"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

var pianoChanged = mygo.NewEvent[uint64]("icetudio:piano-context")

// Workspace owns only UI projections. Project reads go through app.Service.
type Workspace struct {
	materialShelf      materialShelf
	pluginShelf        pluginShelf
	locateAudio        *locateAudioForm
	mixTrack           string
	mixSplit           float32
	recording          *recordingForm
	audioExport        *audioExportForm
	browserDrawer      bool
	agentDrawer        bool
	dispatch           func(func())
	pendingURL         string
	stateDir           string
	preferences        nativePreferences
	prefsVersion       uint64
	prefsSaving        bool
	prefsError         string
	browserRecovery    *browserRecovery
	search             *nativeSearch
	attachment         *nativeAttachment
	settings           *nativeSettings
	skillPanel         *nativeSkills
	approval           *nativeApproval
	composer           agentComposer
	agent              agentHistory
	listSequence       uint64
	draftSequence      uint64
	mapSequence        uint64
	mapForm            *timeMapForm
	savedMap           *app.TimeMapDraftRecord
	mapReady           bool
	mapError           string
	transportOptions   playback.Request
	win                *mygo.Window
	piano              *mygo.WebView
	service            *app.Service
	preview            PianoPreview
	projects           []storage.ProjectSummary
	active             storage.Project
	selected           string
	message            string
	sequence           uint64
	split              float32
	showAgent          bool
	md                 markdown.Options
	smokeResult        chan error
	edits              *Edits
	editBusy           bool
	closeFrozen        bool
	closeApproved      bool
	closeVersion       uint64
	closeDialogOpen    bool
	closeInspectBusy   bool
	closeToken         string
	closeReport        pianoInspection
	closeError         string
	audio              playback.Status
	audioBusy          bool
	audioPending       int
	audioSerial        uint64
	smokeEdit          bool
	smokeAgentHistory  bool
	smokeAgentComposer bool
	smokePanels        bool
	smokeAgentEdit     bool
	arrangementUI      arrangementView
	closeNativeVersion string
	closeHadGesture    bool
	drafts             *musicDraftStore
	draftQueue         chan func()
	savedInputs        []musicDraft
	draftError         string
	form               *propertyForm
	fileBusy           bool
	showDrafts         bool
	quantizeUnit       string
	showAudio          bool
	fonts              playback.FontList
	playPosition       int64
	showKeyHelp        bool
	followPaused       bool
	positionInput      string
	positionError      string
}

func workspaceTheme() *ui.Theme {
	t := *ui.DarkTheme()
	t.Background = ui.Hex("#1a1e24")
	t.Surface = ui.Hex("#272c34")
	t.SurfaceHover = ui.Hex("#343c48")
	t.SurfacePressed = ui.Hex("#424e5e")
	t.Border = ui.Hex("#444d5b")
	t.Text = ui.Hex("#edf1f7")
	t.TextMuted = ui.Hex("#b2bdca")
	t.Accent = ui.Hex("#83c0ed")
	t.AccentHover = ui.Hex("#9bcff4")
	t.AccentPressed = ui.Hex("#6caddd")
	t.AccentText = ui.Hex("#102338")
	t.Selection = ui.RGBA(131, 192, 237, 0.3)
	t.Focus = t.Accent
	t.Radius = 7
	t.FontSize = 13
	return &t
}

func workspaceLightTheme() *ui.Theme {
	t := ui.LightTheme()
	t.Background = ui.Hex("#f8fafc")
	t.Surface = ui.Hex("#eef2f7")
	t.SurfaceHover = ui.Hex("#e4ebf4")
	t.SurfacePressed = ui.Hex("#d6e2ee")
	t.Border = ui.Hex("#cbd5e2")
	t.Text = ui.Hex("#172434")
	t.TextMuted = ui.Hex("#4e5f73")
	t.Accent = ui.Hex("#215f94")
	t.AccentHover = ui.Hex("#194f81")
	t.AccentPressed = ui.Hex("#16446d")
	t.Selection = ui.RGBA(33, 95, 148, 0.2)
	t.Focus = t.Accent
	t.Radius = 7
	t.FontSize = 13
	return t
}

func (a *Workspace) view(c *ui.Context) {
	t := workspaceTheme()
	if a.preferences.Theme == "light" {
		t = workspaceLightTheme()
	}
	c.SetTheme(t)
	zoom := a.preferences.Scale
	if zoom == 0 {
		zoom = 1
	}
	c.SetZoom(zoom)
	if a.pendingURL != "" {
		link := a.pendingURL
		a.pendingURL = ""
		if safeWebLink(link) {
			c.OpenURL(link)
		}
	}
	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		ui.ScrollHorizontal(c).Height(48).Background(t.Surface).Children(func() {
			ui.Row(c).Height(48).Padding(8, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "IceTudio").FontSize(17).Bold().SingleLine()
				if a.service == nil {
					ui.Text(c, "只读预览").FontSize(12).TextColor(t.Accent)
				}
				ui.Box(c).Grow(1)
				a.editToolbar(c)
				ui.Button(c, "刷新工程").Disabled(a.service == nil || a.editBusy || a.closeFrozen || a.form != nil || a.mapForm != nil || a.fileBusy).OnClick(a.refresh)
				ui.Button(c, "设置").Disabled(a.service == nil || a.closeFrozen).OnClick(func() { a.openSettings("模型") })
				a.layoutTools(c)
			})
		})
		a.workspacePanes(c, t)

		ui.Row(c).Height(28).Padding(4, 16).Background(t.Surface).Children(func() {
			ui.Text(c, a.message).FontSize(12).TextColor(t.TextMuted)
		})
	})
	a.editorDrawers(c)
	a.keyboardHelp(c)
	a.browserRecoveryDialog(c)
	a.searchDialog(c)
	a.attachmentDialog(c)
	a.settingsDialog(c)
	a.skillDialog(c)
	a.approvalDialog(c)
	a.agentManagement(c)
	a.sendConfirmation(c)
	a.taskDetailsDialog(c)
	a.timeMapDialog(c)
	a.audioSettings(c)
	a.propertiesDialog(c)
	a.audioExportDialog(c)
	a.locateAudioDialog(c)
	a.recordingDialog(c)
	a.pluginCatalogDialog(c)
	a.draftsDialog(c)
	a.closeDialog(c)
}

func (a *Workspace) editorView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Padding(12).Gap(8).Children(func() {
		title := a.active.Title
		if title == "" {
			title = "选择一个工程"
		}
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, title).FontSize(18).Bold()
			ui.Box(c).Grow(1)
			ui.Textf(c, "%d PPQ · 修订 %d", a.active.PPQ, a.active.Revision).TextColor(t.TextMuted)
		})
		a.transport(c)
		a.navigationTools(c)
		view := a.preferences.View
		_, height := c.Size()
		if height < 640 && view != "arrangement" && view != "mixer" {
			view = "piano"
		}
		switch view {
		case "mixer":
			if a.mixSplit == 0 {
				a.mixSplit = 54
			}
			if height < 640 {
				a.mixerPane(c)
			} else {
				ui.SplitVerticalPercent(c, &a.mixSplit, func() { a.arrangement(c) }, func() { a.mixerPane(c) }).Grow(1).MinHeight(0)
			}
		case "arrangement":
			a.arrangement(c)
		case "piano":
			a.pianoPanel(c, t)
		default:
			ui.SplitVerticalPercent(c, &a.split, func() { a.arrangement(c) }, func() { a.pianoPanel(c, t) }).Grow(1).MinHeight(0).OnChange(a.savePreferences)
		}

	})
}

func (a *Workspace) browser(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(12).Gap(8).Background(t.Surface).Children(func() {
		ui.Text(c, "工程浏览器").FontSize(12).Bold()
		ui.Text(c, "工程").FontSize(12).TextColor(t.TextMuted)
		a.projectTools(c)
		a.patternBrowser(c)
		a.materialBrowser(c)
		ui.Button(c, "音频插件库").FillWidth().Disabled(a.service == nil || a.closeFrozen || a.form != nil).OnClick(a.openPluginCatalog)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for _, p := range a.projects {
					entry := ui.Button(c.Key(p.ID), p.Title).FillWidth().Label("打开工程 " + p.Title)
					if p.ID == a.active.ID {
						entry.Background(t.Accent).TextColor(t.AccentText)
					}
					entry.Disabled(a.editBusy || a.closeFrozen || a.form != nil || a.mapForm != nil || a.fileBusy).OnClick(func() { a.open(p.ID) })
				}
				if len(a.projects) == 0 {
					ui.Text(c, "尚无工程\n点击新建或导入 MIDI。").TextColor(t.TextMuted)
				}
			})
		})
		ui.Text(c, "无需连接模型即可编辑工程。\n修改与助手操作共享撤销历史。").FontSize(12).TextColor(t.TextMuted)
	})
}

func (a *Workspace) arrangement(c *ui.Context) {
	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.objectTools(c)
		a.arrangementUI.draw(c, a.active, a.selected, a.arrangementActions(), func(c *ui.Context, t project.Track) {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if a.preferences.View == "mixer" {
					b := ui.Button(c, t.Name).MinWidth(0).Grow(1).Label("选中混音轨道 " + t.Name)
					if a.mixerSelection() == t.ID {
						b.Background(c.Theme().SurfaceHover)
					}
					b.OnClick(func() { a.focusMixerTrack(t) })
				} else {
					ui.Text(c, t.Name).Bold().Grow(1)
				}
				a.muteSolo(c, t)
			})
			ui.Textf(c, "CH %d · %s", t.Channel+1, gmNames[t.Program]).FontSize(12).TextColor(c.Theme().TextMuted)
			if m := a.active.State.Mixer; m != nil {
				for _, lane := range trackAutomation(m, t.ID) {
					ui.Text(c.Key(lane.ID), "◆ "+automationLabel(lane, m.Channel(t.ID))).FontSize(10).SingleLine().TextColor(c.Theme().TextMuted)
				}
			}
		})
	})
}
func (a *Workspace) arrangementActions() arrangementActions {
	return arrangementActions{Writable: func() bool { return a.writable() && a.form == nil && a.mapForm == nil }, Select: a.selectClip, AudioOpen: a.openAudioClip, AutomationOpen: func(id string, lane project.Automation, tick int64) {
		a.openMixer(id, "自动化")
		if f := a.form; f != nil && f.kind == "mixer" {
			f.mixer.target, f.mixer.parameter = lane.Target, lane.Parameter
			f.mixer.position = musicTime(f.meter, tick)
		}
	},
		Open: func() { a.preferences.View = "split"; a.split = 30; a.savePreferences() },
		SnapChanged: func() {
			a.sendPianoCommand("snap", int(editorSnap(a.arrangementUI.snap, a.arrangementUI.zoom, a.active.PPQ)*4000000))
		},
		Freeze: func(value bool) {
			a.preview.freeze(value || a.closeFrozen || a.form != nil || a.mapForm != nil)
			a.notifyPiano()
		},
		Commit: func(target EditTarget, ops []project.Op) { a.mutateAt(target, "edit", ops) },
		Error:  func(message string) { a.message = message },
		Loop: func(start, end int64) {
			a.transportOptions.Loop = true
			a.transportOptions.LoopStart = start
			a.transportOptions.LoopEnd = end
			if a.audio.Playing {
				a.seekPosition(start)
			}
		},
		Seek: a.seekPosition, Keyboard: a.editorKey, Navigate: func() { a.followPaused = true },
	}
}
func (a *Workspace) cancelArrangement() { a.arrangementUI.cancel(a.arrangementActions()) }

func (a *Workspace) agentPanel(c *ui.Context) {
	if a.service != nil {
		a.nativeAgentPanel(c)
		return
	}
	t := c.Theme()
	ui.Column(c).Width(276).Padding(16).Gap(12).Background(t.Surface).Children(func() {
		ui.Text(c, "创作助手").FontSize(16).Bold()
		ui.Text(c, "Native Markdown 样例，未连接模型").FontSize(12).TextColor(t.TextMuted)
		ui.Scroll(c).Grow(1).Children(func() {
			markdown.View(c, "### 工作区迁移\n\n音乐工程仍由 **Go 核心**管理。\n\n| 区域 | 绘制 |\n| --- | --- |\n| 编排 | Native |\n| 卷帘 | WebView |\n\n```go\n// 同一工程事务\nservice.GetProject(id)\n```\n\n- [x] Native 布局\n- [ ] 完整迁移验收\n\n此处是渲染验证样例，不是 Agent 回复。", &a.md)
		})
	})
}

func (a *Workspace) selectClip(track project.Track, clip project.Clip) {
	a.mixTrack = track.ID
	if a.selected != clip.ID {
		a.cancelArrangement()
	}
	a.selected = clip.ID
	a.arrangementUI.locate = clip.ID
	notes := make([]project.Note, 0, 512)
	for _, n := range clip.Notes {
		if len(notes) == 512 {
			break
		}
		if n.Start < int64(a.active.PPQ)*16 && n.Start+n.Duration > 0 {
			notes = append(notes, n)
		}
	}
	a.preview.set(PianoContext{Sampled: track.Sampler != nil, Pattern: project.PatternMembers(&a.active.State, clip.ID), ProjectID: a.active.ID, TrackID: track.ID, ClipID: clip.ID, Revision: a.active.Revision, PPQ: a.active.PPQ, Title: clip.Name, Snap: editorSnap(a.arrangementUI.snap, a.arrangementUI.zoom, a.active.PPQ), Notes: notes, TotalNotes: len(clip.Notes), Clip: clip, Track: project.Track{ID: track.ID, Name: track.Name, Channel: track.Channel, Program: track.Program, Color: track.Color}, Tempo: a.active.State.Tempo, Meter: a.active.State.Meter})
	if a.piano != nil {
		pianoChanged.EmitPage(a.piano.Page(), a.preview.Context().Generation)
	}
}

func (a *Workspace) install(p storage.Project) {
	a.sequence++
	if p.ID != "" && p.ID != a.preferences.LastProject {
		a.preferences.LastProject = p.ID
		a.savePreferences()
	}
	oldSelected := a.selected
	if a.active.ID != p.ID {
		a.stopMaterialPreview()
		a.mixTrack = ""
		if old := a.active.ID; old != "" && a.service != nil && a.win != nil {
			a.audioPending++
			a.audioBusy = true
			go func() {
				err := a.service.StopProjectPlayback(old)
				a.update(func() {
					a.audioPending--
					a.audioBusy = a.audioPending > 0
					if err != nil {
						a.message = "停止原工程播放失败：" + err.Error()
					}
				})
			}()
		}
		a.cancelArrangement()
		a.arrangementUI.rulerDown = false
		a.arrangementUI.start = 0
		a.arrangementUI.handles = nil
		a.arrangementUI.trackDrops = nil
		a.arrangementUI.patternSources = nil
		a.arrangementUI.marked = nil
		oldSelected = ""
	}
	if a.active.ID != p.ID {
		a.playPosition = 0
		a.transportOptions = playback.Request{}
	}
	a.active = p
	if f := a.form; f != nil && f.kind == "transport" && f.entry.Target.ID == p.ID && f.entry.Target.Revision == p.Revision {
		f.meter, _ = newMeterProjection(p.PPQ, p.State.Meter)
	}
	a.selected = oldSelected
	a.preview.set(PianoContext{ProjectID: p.ID, Revision: p.Revision, PPQ: p.PPQ, Title: p.Title})
	for _, track := range p.State.Tracks {
		for _, clip := range track.Clips {
			if clip.ID == oldSelected {
				a.selectClip(track, clip)
				return
			}
		}
	}
	for _, track := range p.State.Tracks {
		if len(track.Clips) > 0 {
			a.selectClip(track, track.Clips[0])
			return
		}
	}
	a.cancelArrangement()
	a.selected = ""
	if a.piano != nil {
		pianoChanged.EmitPage(a.piano.Page(), a.preview.Context().Generation)
	}
}

func (a *Workspace) open(id string) {
	if a.service == nil {
		return
	}
	a.sequence++
	request := a.sequence
	go func() {
		p, err := a.service.GetProject(id)
		a.update(func() {
			if request != a.sequence {
				return
			}
			if err != nil {
				a.message = err.Error()
				return
			}
			a.install(p)
			if a.edits == nil {
				a.message = "只读工程快照"
			}
		})
	}()
}
func (a *Workspace) refresh() {
	if a.service == nil {
		return
	}
	a.sequence++
	a.listSequence++
	listRequest := a.listSequence
	request := a.sequence
	go func() {
		items, err := a.service.ListProjects()
		a.update(func() {
			if request != a.sequence || listRequest != a.listSequence {
				return
			}
			if err != nil {
				a.message = err.Error()
				return
			}
			a.projects = items
			if len(items) > 0 {
				id := items[0].ID
				for _, p := range items {
					if a.active.ID == "" && p.ID == a.preferences.LastProject {
						id = p.ID
					}
				}
				for _, p := range items {
					if p.ID == a.active.ID {
						id = p.ID
					}
				}
				a.open(id)
			} else {
				a.install(storage.Project{})
				a.message = "尚无工程"
			}
		})
	}()
}

// Run starts the incremental MyGo workspace. A nil service uses explicitly
// labelled visual fixtures and never starts a host, model or MCP process.
type Options struct {
	StateDir  string
	SmokeDir  string
	SmokeEdit bool
}

func Run(service *app.Service, options Options) error {
	smokeDir := options.SmokeDir
	prefs, prefsErr := readPreferences(options.StateDir)
	a := &Workspace{stateDir: options.StateDir, preferences: prefs, smokeEdit: options.SmokeEdit, smokeResult: make(chan error, 1), service: service, split: 48, showAgent: true, message: "正在检查未完成编辑"}
	a.split = prefs.Split
	if prefsErr != nil {
		a.prefsError = prefsErr.Error()
	}
	if service != nil {
		a.edits = NewEdits(service)
		a.drafts = &musicDraftStore{backend: service}
		a.draftQueue = make(chan func(), 64)
		go func() {
			for run := range a.draftQueue {
				run()
			}
		}()
	}
	a.preview.drafts = a.drafts
	a.preview.edits = a.edits
	a.preview.service = service
	a.preview.navigation = func(ctx PianoContext, name string, tick, end int64) {
		a.update(func() { a.handlePianoNavigation(ctx, name, tick, end) })
	}
	a.preview.notify = a.notifyPiano
	a.preview.ready = func() { a.update(func() { a.sendPianoCommand("keymap", 0); a.sendPianoCommand("appearance", 0) }) }
	a.preview.invalidate = func() {
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	a.preview.changed = func(id string) {
		if a.win != nil {
			a.update(func() {
				if a.active.ID == id {
					a.open(id)
				}
				a.message = a.edits.State().Message
			})
		}
	}
	mygo.SetFrontend(pianoweb.MyGoAssets)
	mygo.Bind(&a.preview)
	mygo.App.SetName("IceTudio")
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{Title: "IceTudio", Width: 1440, Height: 900, MinWidth: 900, MinHeight: 640, Content: ui.View(a.view)})
		a.win.OnBlur(a.cancelArrangement)
		a.win.OnResize(a.cancelArrangement)
		a.win.OnClose(func(e *mygo.CloseEvent) { a.requestClose(e.PreventDefault) })
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) { a.requestClose(e.PreventDefault) })
		var err error
		a.piano, err = a.win.NewWebView(mygo.WebViewOptions{URL: "/mygo-web/piano.html"})
		if err != nil {
			a.message = "卷帘不可用：" + err.Error()
			if smokeDir != "" {
				a.smokeResult <- err
				mygo.App.Quit()
				return
			}
		} else if smokeDir != "" {
			go a.smoke(smokeDir)
		}
		if service == nil {
			p := fixture()
			a.projects = []storage.ProjectSummary{{ID: p.ID, Title: p.Title, PPQ: p.PPQ}}
			a.install(p)
			a.message = "只读视觉样例 · 无宿主/数据库/模型连接"
		} else {
			a.checkEdits()
			a.reloadDrafts()
			a.checkTimeMap()
			a.refreshConversations()
			a.loadComposer()
			stopAudio := make(chan struct{})
			a.win.OnClosed(func() {
				close(stopAudio)
				a.cancelPluginScan()
			})
			go a.watchAudio(stopAudio)
			go a.watchAgent(stopAudio)
			a.refresh()
			stopEvents := service.Subscribe(func(evt app.Event) {
				if evt.SessionID != "" {
					a.update(func() {
						if a.agent.selected != "" {
							a.loadAgentHistory(false)
						}
					})
				}
				if evt.Type == "project.changed" || evt.Type == "project.deleted" {
					a.update(func() {
						a.invalidateCandidate(evt.ProjectID)
						a.reloadProjectList()
						if evt.ProjectID == a.active.ID {
							if evt.Type == "project.deleted" {
								a.install(storage.Project{})
								a.refresh()
							} else {
								a.open(evt.ProjectID)
							}
						}
					})
				}
			})
			a.win.OnClosed(stopEvents)
		}
		a.win.Invalidate()
	})
	err := mygo.App.Run()
	if err != nil {
		return err
	}
	if smokeDir != "" {
		select {
		case err = <-a.smokeResult:
			return err
		default:
			return fmt.Errorf("验证在结果生成前关闭")
		}
	}
	return nil
}

func fixture() storage.Project {
	p := storage.Project{ID: "preview", Title: "Glass Tides · 视觉样例", PPQ: 480}
	p.State, _ = project.Blank(p.Title, 480)
	for i, name := range []string{"Soft Keys", "Warm Bass", "Glass Bells", "Percussion"} {
		t := project.Track{ID: fmt.Sprintf("track-%d", i), Name: name, Channel: i, Program: i * 8}
		for j := 0; j < 3; j++ {
			c := project.Clip{ID: fmt.Sprintf("clip-%d-%d", i, j), Name: []string{"Verse", "Variation", "Outro"}[j], Start: int64(j*2880 + i*240), Length: 2400}
			for n := 0; n < 8+j*2; n++ {
				c.Notes = append(c.Notes, project.Note{ID: fmt.Sprintf("note-%d-%d-%d", i, j, n), Start: int64(n * 240), Duration: 180, Pitch: 60 + i*3 + n%4*2, Velocity: 90, Channel: i})
			}
			t.Clips = append(t.Clips, c)
		}
		p.State.Tracks = append(p.State.Tracks, t)
	}
	return p
}

// update marshals UI state to the window thread. Tests inject the same queue
// boundary so asynchronous service callbacks remain testable without a window.
func (a *Workspace) update(fn func()) {
	if a.dispatch != nil {
		a.dispatch(fn)
		return
	}
	a.win.Update(fn)
}
