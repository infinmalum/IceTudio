package desktop

import "fmt"

// UI-only state is frozen by the close modal. In-flight service operations and
// unsaved form text remain visible; force quit never pretends they were saved.
func (a *Workspace) panelCloseState() (string, bool) {
	version := ""
	blocked := false
	if d := a.settings; d != nil {
		dirty := d.form != nil && d.form.dirty
		if d.form != nil {
			version += fmt.Sprintf("form:%d/", d.form.version)
		}
		version += fmt.Sprintf("settings:%t/%t/%t/", d.busy, dirty, d.apiKey != "")
		blocked = blocked || d.busy || dirty || d.apiKey != ""
	}
	if d := a.skillPanel; d != nil {
		dirty := d.proposal != nil && d.proposal.dirty
		if d.proposal != nil {
			version += fmt.Sprintf("proposal:%d/", d.proposal.version)
		}
		version += fmt.Sprintf("skills:%t/%t/%t/%t/%t/", d.busy, dirty, d.note != "", d.request != "", d.evalNote != "")
		blocked = blocked || d.busy || dirty || d.note != "" || d.request != "" || d.evalNote != ""
	}
	if d := a.approval; d != nil {
		version += fmt.Sprintf("approval:%t/%s/%s/%d/", d.busy, d.note, d.result, d.generation)
		blocked = blocked || d.busy || d.note != "" || d.result != ""
	}
	if d := a.agent.details; d != nil {
		version += fmt.Sprintf("task:%t/%s/", d.actionBusy, d.input)
		blocked = blocked || d.actionBusy || d.input != ""
	}
	if d := a.search; d != nil {
		version += fmt.Sprintf("search:%t/", d.busy)
		blocked = blocked || d.busy
	}
	if d := a.attachment; d != nil {
		version += fmt.Sprintf("attachment:%t/", d.busy)
		blocked = blocked || d.busy
	}
	if d := a.browserRecovery; d != nil {
		version += fmt.Sprintf("browser:%t/", d.busy)
		blocked = blocked || d.busy
	}
	version += fmt.Sprintf("prefs:%t/%s/%d/", a.prefsSaving, a.prefsError, a.prefsVersion)
	blocked = blocked || a.prefsSaving || a.prefsError != ""
	return version, blocked
}
