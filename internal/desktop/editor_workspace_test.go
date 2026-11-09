package desktop

import (
	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
	"math"
	"testing"
)

func TestEditorMusicPositionsRoundTripAndRejectTruncatedBars(t *testing.T) {
	for _, ppq := range []int{1, 7, 480, 32768} {
		meter := []project.MeterPoint{{Tick: 0, Numerator: 4, Denominator: 4}, {Tick: int64(ppq)*5 + 1, Numerator: 3, Denominator: 8}, {Tick: int64(ppq)*7 + 1, Numerator: 7, Denominator: 128}}
		m, err := newMeterProjection(ppq, meter)
		if err != nil {
			t.Fatal(err)
		}
		for _, tick := range []int64{0, 1, int64(ppq) * 5, int64(ppq)*5 + 1, int64(ppq) * 6, project.MaxTick, project.MaxTick * 2} {
			s := musicTime(m, tick)
			got, err := parseMusicTime(m, s)
			if err != nil || got != tick {
				t.Fatalf("ppq %d tick %d %s: %d %v", ppq, tick, s, got, err)
			}
		}
	}
	m, _ := newMeterProjection(480, []project.MeterPoint{{Tick: 0, Numerator: 4, Denominator: 4}, {Tick: 500, Numerator: 3, Denominator: 8}})
	for _, s := range []string{"1:4:0", "0:1:0", "2:4:0", "NaN", "2:1:0.1", "2:1:NaN", "999999999999999999:1:0"} {
		if _, e := parseMusicTime(m, s); e == nil {
			t.Fatal("invalid position accepted", s)
		}
	}
}
func TestEditorSnapAndPreferences(t *testing.T) {
	if math.Abs(editorSnap("1/8T", 64, 480)*480-160) > 1e-8 || editorSnap("关闭", 64, 7) != 1.0/7 || editorSnap("自适应", 16, 480) != 2 {
		t.Fatal("snap unit mismatch")
	}
	p := defaultPreferences()
	p.View = "piano"
	p.HideAgent = true
	p.QuantizeKey = "Q"
	p.DuplicateKey = "B"
	p.Follow = "连续跟随"
	dir := t.TempDir()
	if err := writePreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	got, e := readPreferences(dir)
	if e != nil || got != p {
		t.Fatal("layout did not persist", got, e)
	}
	p.DuplicateKey = "Q"
	if validPreferences(p) {
		t.Fatal("conflicting keyboard map accepted")
	}
	if b, a := editorPanels(900, defaultPreferences()); b || a {
		t.Fatal("narrow editor lost its canvas")
	}
	if b, a := editorPanels(1440, defaultPreferences()); !b || !a {
		t.Fatal("wide editor lost panels")
	}
}
func TestNativeContinuousArrangementWheelZoomAndFarPosition(t *testing.T) {
	h, _ := arrangementForTest(t)
	h.v.defaults()
	h.v.width = 800
	if !h.v.navigation(ui.InputEvent{Kind: ui.InputScroll, DX: 32}, h.p, h.actions) || h.v.start != 240 {
		t.Fatal("continuous pan failed", h.v.start)
	}
	before := float64(h.v.start) + 200/h.v.zoom*float64(h.p.PPQ)
	h.v.navigation(ui.InputEvent{Kind: ui.InputZoom, Scale: 1.5, X: 200}, h.p, h.actions)
	after := float64(h.v.start) + 200/h.v.zoom*float64(h.p.PPQ)
	if math.Abs(after-before) > 1 {
		t.Fatal("zoom moved musical anchor", before, after)
	}
	h.v.locateClip(h.p, "c_native")
	if h.v.start != 0 {
		t.Fatal("locate did not use exact clip start")
	}
	if h.calls != 0 || h.p.Revision != 0 {
		t.Fatal("navigation wrote music")
	}
}
func TestNativeEdgeScrollRetainsGestureRevision(t *testing.T) {
	h, _ := arrangementForTest(t)
	r := h.clipRect(t)
	h.view.Press(r.X+20, r.Y+15)
	g := h.v.gesture
	if g == nil {
		t.Fatal("no gesture")
	}
	original := g.Target
	h.v.width = 100
	h.v.moveGesture(200, h.p)
	if h.v.start == 0 || g.Target != original || g.ViewStart != 0 || h.calls != 0 {
		t.Fatal("edge scroll rebased or submitted", g)
	}
	h.view.Key(0, ui.KeyEscape)
	if h.calls != 0 {
		t.Fatal("cancel published edge gesture")
	}
}
func TestPianoNavigationRejectsStaleAndUnlistedCommands(t *testing.T) {
	p := PianoPreview{}
	p.set(PianoContext{ProjectID: "p", Revision: 2})
	ctx := p.Attach()
	called := 0
	p.navigation = func(PianoContext, string, int64, int64) { called++ }
	if p.Navigate(ctx.SessionID, ctx.Generation, "seek", 480, 0) != nil || called != 1 {
		t.Fatal("current seek rejected")
	}
	for _, name := range []string{"edit", "rawSQL"} {
		if p.Navigate(ctx.SessionID, ctx.Generation, name, 0, 0) == nil {
			t.Fatal("unknown command accepted")
		}
	}
	if p.Navigate(ctx.SessionID, ctx.Generation+1, "seek", 0, 0) == nil || p.Navigate(ctx.SessionID, ctx.Generation, "seek", -1, 0) == nil || called != 1 {
		t.Fatal("invalid transport escaped boundary")
	}
}

func TestNativeLayoutUsesMyGoZoomedSizeOnce(t *testing.T) {
	a := &Workspace{preferences: defaultPreferences()}
	a.install(fixture())
	a.preferences.Scale = 1.25
	v := ui.NewTester(a.view, 1440, 900)
	if !v.HasText("ARRANGEMENT") {
		t.Fatal("125% zoom unnecessarily hid arrangement")
	}
	if err := v.Click("浏览器"); err != nil {
		t.Fatal(err)
	}
	if !a.browserDrawer {
		t.Fatal("collapsed browser has no drawer entry")
	}
}

func TestNativeLayoutSegmentSwitchesByMouseAndKeyboard(t *testing.T) {
	a := &Workspace{preferences: defaultPreferences()}
	v := ui.NewTester(a.layoutTools, 900, 80)
	if err := v.Click("钢琴卷帘"); err != nil {
		t.Fatal(err)
	}
	if a.preferences.View != "piano" || a.prefsVersion != 1 {
		t.Fatalf("click selected %q with %d preference updates", a.preferences.View, a.prefsVersion)
	}
	v.Key(0, ui.KeyRight)
	if a.preferences.View != "mixer" || a.prefsVersion != 2 {
		t.Fatalf("arrow selected %q with %d preference updates", a.preferences.View, a.prefsVersion)
	}
}
