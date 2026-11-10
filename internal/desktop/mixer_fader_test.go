package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativeMixerFaderCancelsAndCommitsOnce(t *testing.T) {
	edits, svc, p := testEdits(t)
	if err := edits.Check(); err != nil {
		t.Fatal(err)
	}
	done := make(chan func(), 1)
	a := &Workspace{active: p, edits: edits, dispatch: func(fn func()) { done <- fn }}
	view := ui.NewTester(func(c *ui.Context) {
		a.mixerFader(c, mixChoice{"t_native", "Keys"}, 1, true)
	}, 300, 160)
	r, ok := view.Find("推子 Keys")
	if !ok {
		t.Fatal("mixer fader is unreachable")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	view.Press(x, y)
	view.Move(x, r.Y+7)
	if a.mixGesture == nil || a.mixGesture.value != 2 || a.editBusy {
		t.Fatal("drag did not preview the new gain without a write")
	}
	view.Key(0, ui.KeyEscape)
	view.Release(x, r.Y+7)
	if a.mixGesture != nil || a.editBusy {
		t.Fatal("Escape did not cancel gain adjustment")
	}

	view.Press(x, y)
	view.Move(x, r.Y+7)
	view.Release(x, r.Y+7)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("pointer release did not commit")
	}
	got, err := svc.GetProject(p.ID)
	if err != nil || got.Revision != p.Revision+1 || got.State.Mixer.Channel("t_native").Gain != 2 {
		t.Fatalf("unexpected fader commit: revision %d, %v", got.Revision, err)
	}
	if _, err = edits.Execute(EditTarget{got.ID, got.Revision}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetProject(p.ID)
	if err != nil || got.Revision != p.Revision+2 || got.State.Mixer != nil {
		t.Fatalf("shared undo did not restore gain: revision %d, %v", got.Revision, err)
	}
}
