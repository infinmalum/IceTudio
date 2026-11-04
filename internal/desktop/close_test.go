package desktop

import "testing"

func TestNativeCloseConfirmationRejectsLaterSnapshots(t *testing.T) {
	if closeSnapshotChanged(1, 1, "same", "same", "same", "same", true) {
		t.Fatal("identical snapshot rejected")
	}
	for _, changed := range []bool{
		closeSnapshotChanged(1, 2, "same", "same", "same", "same", true),
		closeSnapshotChanged(1, 1, "old-draft", "new-draft", "same", "same", true),
		closeSnapshotChanged(1, 1, "same", "same", "old-page", "new-page", true),
	} {
		if !changed {
			t.Fatal("stale close confirmation accepted")
		}
	}
	if closeSnapshotChanged(1, 1, "same", "same", "old", "unknown", false) {
		t.Fatal("explicit unknown-page force exit was made impossible")
	}
}
func TestNativeCloseTracksLateSettingsAndPendingOperations(t *testing.T) {
	a := Workspace{settings: &nativeSettings{form: newNativeForm(new(propertyValues))}}
	a.settings.form.dirty = true
	before, blocked := a.panelCloseState()
	if !blocked {
		t.Fatal("unsaved settings bypassed close check")
	}
	a.settings.form.version++
	after, _ := a.panelCloseState()
	if before == after {
		t.Fatal("late input did not invalidate confirmation")
	}
	a.settings.form.dirty = false
	a.settings.busy = true
	if _, blocked := a.panelCloseState(); !blocked {
		t.Fatal("in-flight settings operation was clean")
	}
}
