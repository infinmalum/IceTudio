package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func (a *Workspace) writable() bool {
	if a.edits == nil || a.editBusy || a.closeFrozen || a.fileBusy || a.arrangementUI.gesture != nil || a.arrangementUI.marquee != nil || a.arrangementUI.patternDrag != nil || a.arrangementUI.materialDrag != nil || a.arrangementUI.audioDrag != nil {
		return false
	}
	s := a.edits.State()
	return s.Ready && !s.Busy && s.Pending == nil && a.active.ID != ""
}
func (a *Workspace) mutate(kind string, ops []project.Op) {
	a.mutateAt(EditTarget{ProjectID: a.active.ID, Revision: a.active.Revision}, kind, ops)
}
func (a *Workspace) mutateAt(target EditTarget, kind string, ops []project.Op) {
	a.submitEdit(target, kind, ops, nil)
}
func (a *Workspace) submitEdit(target EditTarget, kind string, ops []project.Op, confirmed func()) {
	if !a.writable() || target.ProjectID != a.active.ID {
		return
	}
	a.editBusy = true
	go func() {
		_, err := a.edits.Execute(target, kind, ops)
		a.update(func() {
			a.editBusy = false
			a.message = a.edits.State().Message
			a.notifyPiano()
			if err != nil {
				a.message += "：" + err.Error()
				if a.mapForm != nil && a.mapForm.applying {
					a.mapForm.applying = false
					a.mapForm.err = err.Error()
				}
				if a.form != nil && a.form.applying {
					a.form.applying = false
					a.form.err = err.Error()
				}
			}
			if err == nil && confirmed != nil {
				confirmed()
			}
			if a.active.ID == target.ProjectID {
				a.open(target.ProjectID)
			}
		})
	}()
}
func (a *Workspace) checkEdits() {
	if a.edits == nil || a.editBusy || a.closeFrozen {
		return
	}
	a.editBusy = true
	go func() {
		err := a.edits.Check()
		a.update(func() {
			a.editBusy = false
			a.message = a.edits.State().Message
			a.notifyPiano()
			if err != nil {
				a.message += "：" + err.Error()
			}
		})
	}()
}
func (a *Workspace) queryEdit() {
	if a.edits == nil || a.editBusy || a.closeFrozen {
		return
	}
	a.editBusy = true
	go func() {
		_, _, err := a.edits.Query()
		a.update(func() {
			a.editBusy = false
			a.message = a.edits.State().Message
			a.notifyPiano()
			if err != nil {
				a.message += "：" + err.Error()
			}
			if a.active.ID != "" {
				a.open(a.active.ID)
			}
		})
	}()
}
func (a *Workspace) editToolbar(c *ui.Context) {
	ui.Row(c).Gap(6).Children(func() {
		ui.Button(c, "撤销").Disabled(!a.writable() || a.form != nil || a.mapForm != nil).OnClick(func() { a.mutate("undo", nil) })
		ui.Button(c, "重做").Disabled(!a.writable() || a.form != nil || a.mapForm != nil).OnClick(func() { a.mutate("redo", nil) })
		if a.edits != nil {
			s := a.edits.State()
			if !s.Ready {
				ui.Button(c, "重试恢复检查").Disabled(a.editBusy || a.closeFrozen).OnClick(a.checkEdits)
			}
			if s.Pending != nil {
				ui.Button(c, "查询保存结果").Disabled(a.editBusy || a.closeFrozen).OnClick(a.queryEdit)
			}
		}
	})
}
func (a *Workspace) muteSolo(c *ui.Context, t project.Track) {
	for _, kind := range []string{"mute", "solo"} {
		value := !t.Muted
		label := "M"
		name := "静音"
		patch := project.TrackPatch{Muted: &value}
		if kind == "solo" {
			value = !t.Solo
			label = "S"
			name = "独奏"
			patch = project.TrackPatch{Solo: &value}
		}
		button := ui.Button(c.Key(fmt.Sprint(t.ID, kind)), label).Label(name + " " + t.Name).Width(26).Disabled(!a.writable() || a.form != nil || a.mapForm != nil)
		if (kind == "mute" && t.Muted) || (kind == "solo" && t.Solo) {
			button.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
		}
		button.OnClick(func() {
			a.mutate("edit", []project.Op{{Kind: "track.update", TrackID: t.ID, TrackPatch: &patch}})
		})
	}
}

func (a *Workspace) notifyPiano() {
	if a.piano != nil {
		pianoChanged.EmitPage(a.piano.Page(), a.preview.Context().Generation)
	}
}
