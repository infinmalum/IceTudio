package desktop

import (
	"encoding/json"
	"testing"
)

func TestNativeAndPianoDraftPartitionsAndCAS(t *testing.T) {
	_, svc, p := testEdits(t)
	d := &musicDraftStore{backend: svc}
	native := newMusicDraft(EditTarget{p.ID, 0}, p.Title, "t_native", "c_native", "clip", `{"Name":"未提交"}`)
	if err := d.put(native, false); err != nil {
		t.Fatal(err)
	}
	projected, err := d.pianoRead()
	if err != nil {
		t.Fatal(err)
	}
	view, err := decodeMusicDrafts(projected)
	if err != nil || len(view.Entries) != 0 {
		t.Fatal("Native entries leaked into Piano ownership")
	}
	piano := native
	piano.Label = "卷帘本地输入"
	piano.Key = "piano-key"
	piano.Raw = "105"
	s := musicDraftState{Version: 1, Entries: []musicDraft{piano}}
	b, _ := json.Marshal(s)
	if err := d.pianoSave(projected.Token, "piano_write", string(b)); err != nil {
		t.Fatal(err)
	}
	entries, err := d.list()
	if err != nil || len(entries) != 2 {
		t.Fatalf("lost native input: %v", err)
	}
	// A stale bridge call cannot overwrite a newer native raw input.
	projected, _ = d.pianoRead()
	native.Raw = `{"Name":"后来的输入"}`
	if err := d.put(native, false); err != nil {
		t.Fatal(err)
	}
	if err := d.pianoSave(projected.Token, "stale_write", `{"version":1,"entries":[]}`); err == nil {
		t.Fatal("stale CAS accepted")
	}
	// A Piano retry rereads its own projection. Clearing that partition retains Native.
	projected, _ = d.pianoRead()
	if err := d.pianoSave(projected.Token, "new_write", `{"version":1,"entries":[]}`); err != nil {
		t.Fatal(err)
	}
	entries, _ = d.list()
	if len(entries) != 1 || entries[0].Raw != native.Raw {
		t.Fatal("retry deleted Native entry")
	}
	stale := native
	stale.Raw = "earlier"
	if err := d.put(stale, true); err == nil {
		t.Fatal("stale discard cleared newer input")
	}
	if err := d.put(native, true); err != nil {
		t.Fatal(err)
	}
	entries, _ = d.list()
	if len(entries) != 0 {
		t.Fatal("explicit Native discard failed")
	}
}
func TestNativeDraftOriginalRevisionAndRejectedBridgePreserved(t *testing.T) {
	_, svc, p := testEdits(t)
	d := &musicDraftStore{backend: svc}
	e := newMusicDraft(EditTarget{p.ID, 0}, p.Title, "t_native", "c_native", "clip", `{"Start":"bad"}`)
	if err := d.put(e, false); err != nil {
		t.Fatal(err)
	}
	// App delegates are still authoritative; reopening only restores input.
	reopened := &musicDraftStore{backend: svc}
	entries, err := reopened.list()
	if err != nil || len(entries) != 1 || entries[0].Target.Revision != 0 || entries[0].Raw != e.Raw {
		t.Fatal("input was rebased or dropped")
	}
	got, _ := svc.GetProject(p.ID)
	if got.Revision != 0 {
		t.Fatal("draft changed music")
	}
	// Refuse an invalid namespace from a bridge client, leaving original file.
	b, _ := json.Marshal(musicDraftState{Version: 1, Entries: []musicDraft{e}})
	r, _ := reopened.pianoRead()
	if err := reopened.pianoSave(r.Token, "foreign", string(b)); err == nil {
		t.Fatal("Piano overwrote Native namespace")
	}
	after, _ := svc.GetMusicInputs()
	before, _ := json.Marshal(e)
	var state musicDraftState
	_ = json.Unmarshal([]byte(after.Payload), &state)
	saved, _ := json.Marshal(state.Entries[0])
	if string(before) != string(saved) {
		t.Fatal("rejected request changed draft")
	}

}
