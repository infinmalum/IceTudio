package desktop

import (
	"fmt"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func TestPreviewCopiesNotesAndSerializesGenerations(t *testing.T) {
	var preview PianoPreview
	notes := []project.Note{{ID: "original", Pitch: 60}}
	preview.set(PianoContext{Notes: notes})
	notes[0].Pitch = 80
	first := preview.Context()
	first.Notes[0].Pitch = 90
	if got := preview.Context(); got.Notes[0].Pitch != 60 || got.Generation != 1 {
		t.Fatalf("projection aliased source or caller: %+v", got)
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); preview.set(PianoContext{Notes: notes}) }()
	}
	wg.Wait()
	if got := preview.Context().Generation; got != 25 {
		t.Fatalf("generation %d", got)
	}
}

func TestNativeClipSelectionUpdatesOnlyPreview(t *testing.T) {
	a := &Workspace{preferences: defaultPreferences(), split: 48}
	a.preferences.HideAgent = false
	p := fixture()
	a.install(p)
	// The same clip name occurs in several tracks; target an unambiguous label
	// in this fixture rather than deriving identity from the user's title.
	p.State.Tracks[0].Clips[1].Name = "选择另一个片段"
	a.active = p
	view := ui.NewTester(a.view, 1440, 900)
	if err := view.Click("选择另一个片段"); err != nil {
		t.Fatal(err)
	}
	ctx := a.preview.Context()
	if ctx.ClipID != p.State.Tracks[0].Clips[1].ID || a.active.Revision != p.Revision {
		t.Fatalf("selection wrong or changed music: %+v", ctx)
	}
	if !view.HasText("创作助手") {
		t.Fatalf("native agent sample absent: %v", view.Texts())
	}
	if err := view.Click("助手面板"); err != nil {
		t.Fatal(err)
	}
	if !a.preferences.HideAgent {
		t.Fatal("native panel toggle failed")
	}
	for _, size := range [][2]int{{1280, 800}, {960, 720}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) { ui.NewTester(a.view, size[0], size[1]) })
	}
}

func TestPianoPreviewBoundsAndContextReplacement(t *testing.T) {
	a := &Workspace{}
	p := fixture()
	clip := p.State.Tracks[0].Clips[0]
	clip.Notes = nil
	for i := 0; i < 2000; i++ {
		clip.Notes = append(clip.Notes, project.Note{ID: fmt.Sprint("dense-", i), Start: 0, Duration: 120, Pitch: 60, Velocity: 90})
	}
	p.State.Tracks[0].Clips[0] = clip
	a.active = p
	a.selectClip(p.State.Tracks[0], clip)
	first := a.preview.Context()
	if len(first.Notes) != 512 || first.TotalNotes != 2000 {
		t.Fatalf("unbounded read-only projection: %d / %d", len(first.Notes), first.TotalNotes)
	}
	a.selectClip(p.State.Tracks[1], p.State.Tracks[1].Clips[1])
	next := a.preview.Context()
	if next.Generation != first.Generation+1 || next.ClipID == first.ClipID || len(next.Notes) != 10 {
		t.Fatalf("old context retained: %+v", next)
	}
}
