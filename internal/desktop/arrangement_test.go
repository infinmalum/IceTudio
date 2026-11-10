package desktop

import (
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

type arrangementHarness struct {
	v        arrangementView
	p        storage.Project
	selected string
	frozen   bool
	calls    int
	err      error
	ops      []project.Op
	target   EditTarget
	actions  arrangementActions
	view     *ui.Tester
}

func arrangementForTest(t *testing.T) (*arrangementHarness, *Edits) {
	t.Helper()
	e, svc, p := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	h := &arrangementHarness{p: p, selected: "c_native"}
	h.actions = arrangementActions{
		Writable: func() bool { return !h.frozen }, Select: func(_ project.Track, c project.Clip) { h.selected = c.ID }, Open: func() {}, Freeze: func(b bool) { h.frozen = b }, Error: func(s string) { t.Log(s) },
		Commit: func(target EditTarget, ops []project.Op) {
			h.calls++
			h.target = target
			h.ops = ops
			_, h.err = e.Execute(target, "edit", ops)
			h.p, _ = svc.GetProject(p.ID)
		},
	}
	h.view = ui.NewTester(func(c *ui.Context) {
		c.SetTheme(workspaceTheme())
		h.v.draw(c, h.p, h.selected, h.actions, nil, func(c *ui.Context, t project.Track) { ui.Text(c, t.Name) })
	}, 1200, 600)
	return h, e
}
func (h *arrangementHarness) clipRect(t *testing.T) ui.Rect {
	t.Helper()
	r, ok := h.view.Find("片段")
	if !ok {
		t.Fatalf("clip not reachable: %v", h.view.Texts())
	}
	return r
}
func TestNativeArrangementPointerMoveResizeAndSharedUndo(t *testing.T) {
	h, e := arrangementForTest(t)
	before := h.p
	r := h.clipRect(t)
	h.view.Press(r.X+20, r.Y+15)
	if h.v.gesture == nil || !h.frozen {
		t.Fatal("pointer did not start frozen gesture")
	}
	h.view.Move(r.X+84, r.Y+15)
	if h.v.gesture.Start != 480 || h.calls != 0 {
		t.Fatal("preview wrote or wrong snap")
	}
	h.view.Wheel(r.X+84, r.Y+15, 200, 200)
	if h.v.gesture == nil || h.v.gesture.Start != 480 || h.v.zoom != 64 {
		t.Fatal("scroll changed gesture coordinate base")
	}
	h.view.Release(r.X+84, r.Y+15)
	if h.calls != 1 || h.err != nil || h.target.Revision != 0 || h.p.State.Tracks[0].Clips[0].Start != 480 {
		t.Fatalf("move not committed once: %+v %v", h.ops, h.err)
	}
	if h.frozen || h.v.gesture != nil {
		t.Fatal("gesture retained after release")
	}
	h.view.Advance(time.Second)
	r = h.clipRect(t)
	h.view.Press(r.X+r.W-3, r.Y+15)
	if h.v.gesture == nil || !h.v.gesture.Resize {
		t.Fatal("real end did not start resize")
	}
	h.view.Move(r.X+r.W+61, r.Y+15)
	h.view.Release(r.X+r.W+61, r.Y+15)
	if h.calls != 2 || h.err != nil || h.p.State.Tracks[0].Clips[0].Length != 2400 {
		t.Fatalf("resize failed: %v", h.err)
	}
	// Preserve note/control payloads and the one history shared with WebView.
	after := h.p.State.Tracks[0].Clips[0]
	old := before.State.Tracks[0].Clips[0]
	if !reflect.DeepEqual(after.Notes, old.Notes) || !reflect.DeepEqual(after.Controls, old.Controls) || !reflect.DeepEqual(after.Raw, old.Raw) {
		t.Fatal("gesture changed local events")
	}
	if _, err := e.Execute(EditTarget{h.p.ID, 2}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := e.backend.(*app.Service).GetProject(h.p.ID)
	if got.State.Tracks[0].Clips[0].Length != 1920 || got.State.Tracks[0].Clips[0].Start != 480 {
		t.Fatal("undo did not reverse only resize")
	}
}
func TestNativeArrangementCancelAndStaleRevision(t *testing.T) {
	for _, kind := range []string{"escape", "blur", "explicit"} {
		t.Run(kind, func(t *testing.T) {
			h, _ := arrangementForTest(t)
			r := h.clipRect(t)
			h.view.Press(r.X+20, r.Y+15)
			h.view.Move(r.X+84, r.Y+15)
			switch kind {
			case "escape":
				h.view.Key(0, ui.KeyEscape)
			case "blur":
				h.view.SetFocused(false)
			case "explicit":
				h.v.cancel(h.actions)
			}
			h.view.Release(r.X+84, r.Y+15)
			if h.calls != 0 || h.p.Revision != 0 || h.v.gesture != nil || h.frozen {
				t.Fatalf("%s did not cancel", kind)
			}
		})
	}
	h, e := arrangementForTest(t)
	r := h.clipRect(t)
	h.view.Press(r.X+20, r.Y+15)
	h.view.Move(r.X+84, r.Y+15)
	externalStart := int64(960)
	if _, err := e.Execute(EditTarget{h.p.ID, 0}, "edit", []project.Op{{Kind: "project.rename", Title: "external"}, {Kind: "clip.update", ClipID: "c_native", ClipPatch: &project.ClipPatch{Start: &externalStart}}}); err != nil {
		t.Fatal(err)
	}
	// Replace the current query snapshot while keeping the original gesture.
	h.p.Revision = 1
	h.p.State.Tracks[0].Clips[0].Start = 960
	h.view.Frame()
	h.view.Release(r.X+84, r.Y+15)
	if h.calls != 1 || h.err == nil || h.target.Revision != 0 || h.p.Title != "external" || h.p.State.Tracks[0].Clips[0].Start != 960 {
		t.Fatalf("silently rebased stale gesture: %v", h.err)
	}
}
func TestArrangementProjectionAndFarReachability(t *testing.T) {
	p := fixture()
	p.PPQ = 1
	p.State.Tracks[0].Clips = []project.Clip{{ID: "far", Start: project.MaxTick, Length: project.MaxTick}}

	v := arrangementView{}
	v.locateClip(p, "far")
	if v.start != int64(project.MaxTick) {
		t.Fatal("far location lost precision")
	}
	clips, lanes := arrangementClips([]project.Clip{{ID: "a", Start: 0, Length: 20}, {ID: "b", Start: 12, Length: 6}, {ID: "c", Start: 16, Length: 2}, {ID: "done", Start: 0, Length: 16}}, 16, 32, 1, 64)
	if len(clips) != 3 || lanes != 3 || clips[0].Left != 0 || clips[0].RealEnd != true {
		t.Fatalf("overlap/crossing projection: %+v %d", clips, lanes)
	}
	g := clipGesture{Clip: project.Clip{Start: 100, Length: 100}, PPQ: 7, Zoom: 64, Snap: .25}
	g.move(-8)
	if g.Start != 98 {
		t.Fatalf("negative half snap must round away from zero: %d", g.Start)
	}
	g.move(8)
	if g.Start != 102 {
		t.Fatalf("fractional PPQ rounding: %d", g.Start)
	}
}

func TestArrangementInvalidShrinkAndReleaseOutsideClip(t *testing.T) {
	h, _ := arrangementForTest(t)
	r := h.clipRect(t)
	h.view.Press(r.X+r.W-3, r.Y+15)
	// Length 480 would truncate CC/raw; core rejects without cutting events.
	h.view.Move(r.X+r.W-3-192, r.Y+15)
	h.view.Release(r.X+r.W-3-192, r.Y+15)
	if h.calls != 1 || h.err == nil || h.p.Revision != 0 || len(h.p.State.Tracks[0].Clips[0].Raw) != 1 {
		t.Fatal("destructive shrink changed music")
	}
	h.view.Advance(time.Second)
	r = h.clipRect(t)
	h.view.Press(r.X+20, r.Y+15)
	h.view.Move(r.X+340, r.Y-100)
	h.view.Release(r.X+340, r.Y-100)
	if h.calls != 2 || h.err != nil || h.p.State.Tracks[0].Clips[0].Start != 2400 {
		t.Fatalf("captured outside release: %v", h.err)
	}
}
func TestWorkspaceRefreshDoesNotRebaseOrCancelActiveClipGesture(t *testing.T) {
	p := fixture()
	a := &Workspace{}
	a.install(p)
	track := p.State.Tracks[0]
	clip := track.Clips[0]
	a.arrangementUI.gesture = &clipGesture{Target: EditTarget{p.ID, p.Revision}, Track: track, Clip: clip, Start: clip.Start, Length: clip.Length}
	p.Revision = 5
	p.State.Tracks[0].Clips[0].Start = 999
	a.install(p)
	g := a.arrangementUI.gesture
	if g == nil || g.Target.Revision != 0 || g.Clip.Start != clip.Start {
		t.Fatal("refresh canceled or rebased gesture")
	}
	p.State.Tracks[0].Clips = p.State.Tracks[0].Clips[1:]
	a.install(p)
	if a.arrangementUI.gesture != nil {
		t.Fatal("selection fallback retained deleted target gesture")
	}
}

func TestNativeArrangementNavigationAndFixedHeaders(t *testing.T) {
	h, _ := arrangementForTest(t)
	h.p.State.Tracks[0].Clips = append(h.p.State.Tracks[0].Clips, project.Clip{ID: "far_clip", Name: "Far end", Start: project.MaxTick, Length: project.MaxTick})
	h.view.Frame()
	if err := h.view.Click("全部编排片段"); err != nil {
		t.Fatal(err)
	}
	h.view.Key(0, ui.KeyEnd)
	h.view.Key(0, ui.KeyEnter)
	if h.selected != "far_clip" || h.v.start != int64(project.MaxTick) || h.p.Revision != 0 {
		t.Fatalf("picker lost far clip: %s page %d", h.selected, h.v.start)
	}
	h.v.start = 0
	h.v.zoom = 200
	h.view.Frame()
	before, ok := h.view.Find("Keys")
	if !ok {
		t.Fatal("missing header")
	}
	h.view.Wheel(900, 180, 200, 0)
	after, ok := h.view.Find("Keys")
	if !ok || after.X != before.X {
		t.Fatal("track header moved with horizontal scrolling")
	}
	if h.calls != 0 || h.p.Revision != 0 {
		t.Fatal("view navigation wrote music")
	}
}

func TestNativeArrangementNegativeStartAndClippedResizeBoundary(t *testing.T) {
	h, _ := arrangementForTest(t)
	r := h.clipRect(t)
	h.view.Press(r.X+20, r.Y+15)
	h.view.Move(r.X-44, r.Y+15)
	h.view.Release(r.X-44, r.Y+15)
	if h.calls != 0 || h.p.Revision != 0 {
		t.Fatal("negative start was submitted")
	}
	h.p.State.Tracks[0].Clips[0].Length = 15360
	h.view.Frame()
	h.view.Advance(time.Second)
	r = h.clipRect(t)
	h.view.Press(r.X+r.W-3, r.Y+15)
	if h.v.gesture == nil || h.v.gesture.Resize {
		t.Fatal("viewport crop edge treated as true clip end")
	}
	h.view.Key(0, ui.KeyEscape)
	h.v.start = 15360 - 480*8
	h.view.Frame()
	h.view.Advance(time.Second)
	r = h.clipRect(t)
	h.view.Press(r.X+r.W-3, r.Y+15)
	if h.v.gesture == nil || !h.v.gesture.Resize {
		t.Fatal("true clip end after continuous navigation not resizable")
	}
	h.view.Key(0, ui.KeyEscape)
	if h.calls != 0 {
		t.Fatal("boundary checks changed music")
	}
}

func TestNativeGroupedArrangementDragIsOneTransaction(t *testing.T) {
	h, e := arrangementForTest(t)
	_, err := e.Execute(EditTarget{h.p.ID, h.p.Revision}, "edit", []project.Op{
		{Kind: "clip.add", Clip: &project.ClipSpec{ID: "c_second", TrackID: "t_native", Name: "第二片段", Start: 1920, Length: 480}},
		{Kind: "clip.arrange", Arrangement: &project.ArrangementEdit{Action: "group", IDs: []string{"c_native", "c_second"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.p, _ = e.backend.(*app.Service).GetProject(h.p.ID)
	h.view.Frame()
	r, ok := h.view.Find("▣ 片段")
	if !ok {
		t.Fatal("分组片段未显示")
	}
	h.view.Press(r.X+20, r.Y+15)
	h.view.Move(r.X+84, r.Y+15)
	h.view.Release(r.X+84, r.Y+15)
	clips := h.p.State.Tracks[0].Clips
	if h.calls != 1 || h.err != nil || len(h.ops) != 1 || h.ops[0].Kind != "clip.arrange" ||
		clips[0].Start != 480 || clips[1].Start != 2400 || clips[0].GroupID != clips[1].GroupID {
		t.Fatalf("grouped drag: calls=%d ops=%+v clips=%+v err=%v", h.calls, h.ops, clips, h.err)
	}
}
