package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/audioasset"
)

func TestNativeMaterialDragFreezesRevisionAndSnaps(t *testing.T) {
	edits, svc, p := testEdits(t)
	if err := edits.Check(); err != nil {
		t.Fatal(err)
	}
	a := &Workspace{active: p, edits: edits, service: svc}
	a.preferences.View = "arrangement"
	a.arrangementUI.zoom = 64
	a.arrangementUI.snap = "1/4"
	item := audioasset.Material{ID: "sample_one", Path: "/audio/test.wav"}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).FillWidth().Children(func() {
			ui.Column(c).Width(180).Children(func() { a.materialRow(c, item) })
			ui.Box(c).Width(600).Height(180).Bind(a.arrangementUI.trackDrop("t_native")).Label("drop region")
		})
	}, 1200, 700)
	origin, ok := tester.Find("选择素材 test.wav")
	if !ok {
		t.Fatal("material row not found")
	}
	drop, ok := tester.Find("drop region")
	if !ok {
		t.Fatal("track drop not found")
	}
	tester.Press(origin.X+12, origin.Y+10)
	if a.arrangementUI.materialDrag == nil || !a.preview.frozen {
		t.Fatal("gesture was not frozen")
	}
	tester.Move(drop.X+70, drop.Y+10)
	drag := a.arrangementUI.materialDrag
	if drag == nil || drag.track != "t_native" || drag.tick != 480 || drag.target != (EditTarget{p.ID, p.Revision}) {
		t.Fatalf("invalid drag preview: %+v", drag)
	}
	tester.Move(origin.X+12, origin.Y+10)
	tester.Release(origin.X+12, origin.Y+10)
	if a.arrangementUI.materialDrag != nil || a.preview.frozen || a.fileBusy {
		t.Fatal("canceled drag retained a gesture or queued a write")
	}
}
