package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var pianoCommand = mygo.NewEvent[PianoCommand]("icetudio:piano-command")

func (a *Workspace) pianoTools(c *ui.Context) {
	ui.Row(c).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
		type pianoAction struct {
			label, name string
			value       int
		}
		viewActions := []pianoAction{{"缩小", "zoom", -16}, {"放大", "zoom", 16}, {"片段开头", "locate", 0}, {"全选", "selectAll", 0}, {"清除选择", "clear", 0}}
		editActions := []pianoAction{{"复制", "copy", 0}, {"粘贴", "paste", 0}, {"紧邻复制", "duplicate", 0}, {"删除音符", "delete", 0}, {"量化", "quantize", 0}, {"−12 半音", "transpose", -12}, {"+12 半音", "transpose", 12}, {"−1 半音", "transpose", -1}, {"+1 半音", "transpose", 1}}
		if a.quantizeUnit == "" {
			a.quantizeUnit = "1/16"
		}
		ui.Text(c, "量化").FontSize(12)
		ui.Select(c, &a.quantizeUnit, []string{"1/4", "1/8", "1/16", "1/32", "1/8T", "1/16T"}).Width(70).Disabled(a.closeFrozen).Label("量化网格")
		ui.Button(c, "音符力度").FontSize(12).Disabled(!a.writable() || !a.preview.toolsReady() || a.preview.selectionCount() == 0).OnClick(a.beginVelocity)
		count := a.preview.selectionCount()
		unavailable := a.piano == nil || a.selected == "" || a.closeFrozen
		ui.PopoverMenuButton(c, "视图与选择", func(m *ui.Menu) {
			for _, action := range viewActions {
				if m.Item(action.label).Disabled(unavailable).Chosen() {
					a.sendPianoCommand(action.name, action.value)
				}
			}
		})
		ui.PopoverMenuButton(c, "音符编辑", func(m *ui.Menu) {
			for _, action := range editActions {
				disabled := unavailable || !a.writable() || !a.preview.toolsReady() || count == 0 && action.name != "paste"
				if m.Item(action.label).Disabled(disabled).Chosen() {
					a.sendPianoCommand(action.name, action.value)
				}
			}
		})
	})
}

func (a *Workspace) sendPianoCommand(name string, value int) {
	if a.piano == nil || a.closeFrozen {
		return
	}
	command := a.preview.command(name, value)
	command.Unit = a.quantizeUnit
	command.Theme = a.preferences.Theme
	command.QuantizeKey = a.preferences.QuantizeKey
	command.DuplicateKey = a.preferences.DuplicateKey
	if name == "snap" {
		command.Step = editorSnap(a.arrangementUI.snap, a.arrangementUI.zoom, a.active.PPQ)
	}
	pianoCommand.EmitPage(a.piano.Page(), command)
}
