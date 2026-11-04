package desktop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/project"
)

func TestNativePropertiesFrozenRevisionSharedUndoAndEventPreservation(t *testing.T) {
	e, svc, p := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	f := &propertyForm{kind: "clip", entry: newMusicDraft(EditTarget{p.ID, 0}, p.Title, "t_native", "c_native", "clip", ""), values: propertyValues{Name: "精确片段", Start: "123456789", Length: "1920", Color: "violet"}}
	ops, err := propertyOps(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(EditTarget{f.entry.Target.ID, f.entry.Target.Revision}, "edit", ops); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetProject(p.ID)
	clip := got.State.Tracks[0].Clips[0]
	old := p.State.Tracks[0].Clips[0]
	if clip.Start != 123456789 || clip.Name != "精确片段" || clip.Color != "violet" || !reflect.DeepEqual(clip.Notes, old.Notes) || !reflect.DeepEqual(clip.Controls, old.Controls) || !reflect.DeepEqual(clip.Raw, old.Raw) {
		t.Fatal("property update corrupted events")
	}
	if _, err = e.Execute(EditTarget{p.ID, 0}, "edit", ops); err == nil {
		t.Fatal("stale form rebased")
	}
	if _, err = e.Execute(EditTarget{p.ID, 1}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetProject(p.ID)
	if !reflect.DeepEqual(got.State.Tracks, p.State.Tracks) {
		t.Fatal("shared undo failed")
	}
	f.values.Start = "0"
	f.values.Length = "100"
	ops, _ = propertyOps(f)
	if _, err = e.Execute(EditTarget{p.ID, 2}, "edit", ops); err == nil {
		t.Fatal("form truncated events")
	}
}
func TestNativePropertiesValidateAndRetainRawValues(t *testing.T) {
	for _, v := range []propertyValues{{Start: "0.5", Length: "10"}, {Start: "-1", Length: "10"}, {Start: fmt.Sprint(int64(project.MaxTick) + 1), Length: "10"}, {Start: "0", Length: "0"}, {Start: "0", Length: "9223372036854775808"}, {Start: "0", Length: "10", Color: "pink"}} {
		f := &propertyForm{kind: "clip", values: v}
		if _, err := propertyOps(f); err == nil {
			t.Fatalf("invalid input accepted: %+v", v)
		}
		if f.values != v {
			t.Fatal("invalid input replaced")
		}
	}
	for _, v := range []propertyValues{{Channel: "0", Program: "1"}, {Channel: "17", Program: "1"}, {Channel: "1", Program: "129"}, {Channel: "1.5", Program: "1"}} {
		if _, err := propertyOps(&propertyForm{kind: "track", values: v}); err == nil {
			t.Fatal("invalid routing")
		}
	}
	ops, err := propertyOps(&propertyForm{kind: "track", values: propertyValues{Channel: "10", Program: "128", Color: "mint"}})
	if err != nil || *ops[0].TrackPatch.Channel != 9 || *ops[0].TrackPatch.Program != 127 {
		t.Fatal("GM program conversion")
	}
}
func TestNativeColorPickerKeyboardPersistsChoice(t *testing.T) {
	color := ""
	changes := 0
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); colorPicker(c, &color, func() { changes++ }) }, 500, 400)
	// Same native select keyboard path used in actual property forms.
	v.Key(0, ui.KeyTab)
	v.Key(0, ui.KeySpace)
	v.Key(0, ui.KeyEnd)
	v.Key(0, ui.KeyEnter)
	if color != "moss" || changes != 1 {
		t.Fatalf("color=%s changes=%d texts=%v", color, changes, v.Texts())
	}
}
func TestNativeProjectEmptyStateCanCreate(t *testing.T) {
	e, svc, _ := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	a := Workspace{service: svc, edits: e}
	if !a.projectAvailable() {
		t.Fatal("empty workspace cannot create")
	}
	a.beginProperties("create", project.Track{}, project.Clip{})
	if a.form == nil || a.form.kind != "create" || a.form.entry.Target.ID != "new_project" {
		t.Fatal("new project form unreachable without active project")
	}
}

func TestOrdinaryMusicDraftTakeoverKeepsRawTargetAndPartition(t *testing.T) {
	e, svc, p := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	draft := newMusicDraft(EditTarget{p.ID, 0}, p.Title, "t_native", "c_native", "clip", "")
	draft.Label = "片段起点（tick）"
	draft.Raw = "12345"
	// Real current-format entries use the label in their canonical identity.
	key, _ := json.Marshal([]string{p.ID, "t_native", "c_native", draft.Label})
	draft.Key = string(key)
	b, _ := json.Marshal(musicDraftState{Version: 1, Entries: []musicDraft{draft}})
	if err := svc.SaveMusicInputs("", "ordinary_input", string(b)); err != nil {
		t.Fatal(err)
	}
	d := &musicDraftStore{backend: svc}
	projected, err := d.pianoRead()
	if err != nil {
		t.Fatal(err)
	}
	state, _ := decodeMusicDrafts(projected)
	if len(state.Entries) != 0 {
		t.Fatal("ordinary fields left in WebView ownership")
	}
	kind, v, err := restoreMusicDraft(draft)
	if err != nil || kind != "field" || v.Name != "12345" {
		t.Fatal("draft input changed")
	}
	ops, err := propertyOps(&propertyForm{kind: kind, entry: draft, values: v})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(EditTarget{draft.Target.ID, draft.Target.Revision}, "edit", ops); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetProject(p.ID)
	if got.State.Tracks[0].Clips[0].Start != 12345 {
		t.Fatal("restored input did not reach native service")
	}
	if err := d.put(draft, true); err != nil {
		t.Fatal(err)
	}
	entries, _ := d.list()
	if len(entries) != 0 {
		t.Fatal("applied ordinary field not cleared")
	}
}
func TestNativeVelocityFreezesNoteIDsAndPreservesOtherEvents(t *testing.T) {
	e, svc, p := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	entry := newMusicDraft(EditTarget{p.ID, 0}, p.Title, "t_native", "c_native", "velocity", "")
	entry.NoteIDs = []string{"n_native"}
	f := &propertyForm{kind: "velocity", entry: entry, values: propertyValues{Name: "110"}}
	ops, err := propertyOps(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops[0].NoteDeltas) != 1 || ops[0].NoteDeltas[0].ID != "n_native" {
		t.Fatal("velocity lost frozen selection")
	}
	if _, err = e.Execute(EditTarget{p.ID, 0}, "edit", ops); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetProject(p.ID)
	clip := got.State.Tracks[0].Clips[0]
	old := p.State.Tracks[0].Clips[0]
	if clip.Notes[0].Velocity != 110 || !reflect.DeepEqual(clip.Controls, old.Controls) || !reflect.DeepEqual(clip.Raw, old.Raw) {
		t.Fatal("velocity changed other events")
	}
	f.values.Name = "128"
	if _, err := propertyOps(f); err == nil {
		t.Fatal("out of range velocity")
	}
}
