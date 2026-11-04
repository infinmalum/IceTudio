package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var pianoCommand = mygo.NewEvent[PianoCommand]("icetudio:piano-command")

func (a *Workspace) pianoTools(c *ui.Context) {
	ui.ScrollHorizontal(c).Height(30).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			actions := []struct {
				label, name string
				value       int
				edit        bool
			}{{"缩小", "zoom", -16, false}, {"放大", "zoom", 16, false}, {"上一页", "previous", 0, false}, {"下一页", "next", 0, false}, {"全选", "selectAll", 0, false}, {"清除选择", "clear", 0, false}, {"复制", "copy", 0, true}, {"删除音符", "delete", 0, true}, {"量化", "quantize", 0, true}, {"−12 半音", "transpose", -12, true}, {"+12 半音", "transpose", 12, true}, {"−1 半音", "transpose", -1, true}, {"+1 半音", "transpose", 1, true}}
			if a.quantizeUnit == "" {
				a.quantizeUnit = "1/16"
			}
			ui.Text(c, "量化网格").FontSize(11)
			ui.Select(c, &a.quantizeUnit, []string{"1/4", "1/8", "1/16", "1/32", "1/8T", "1/16T"}).Width(70).Disabled(a.closeFrozen).Label("量化网格")
			ui.Button(c, "音符力度").FontSize(11).Disabled(!a.writable() || !a.preview.toolsReady() || a.preview.selectionCount() == 0).OnClick(a.beginVelocity)
			for _, action := range actions {
				count := a.preview.selectionCount()
				disabled := a.piano == nil || a.selected == "" || a.closeFrozen || (action.edit && (!a.writable() || !a.preview.toolsReady() || count == 0))
				ui.Button(c.Key(action.name+action.label), action.label).FontSize(11).Disabled(disabled).OnClick(func() { a.sendPianoCommand(action.name, action.value) })
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
	pianoCommand.EmitPage(a.piano.Page(), command)
}
