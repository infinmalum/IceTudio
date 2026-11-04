package desktop

import (
	"testing"

	"icetudio/internal/project"
)

func bridgeForTest(t *testing.T) (*PianoPreview, *Edits, EditTarget) {
	t.Helper()
	e, svc, item := testEdits(t)
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	a := &Workspace{service: svc, edits: e}
	a.preview.edits = e
	a.preview.service = svc
	a.install(item)
	ctx := a.preview.Attach()
	if err := a.preview.ContextReady(ctx.SessionID, ctx.Generation); err != nil {
		t.Fatal(err)
	}
	return &a.preview, e, EditTarget{item.ID, item.Revision}
}
func requestFor(p *PianoPreview) PianoEdit {
	ctx := p.Context()
	pitch := 67
	return PianoEdit{SessionID: ctx.SessionID, Generation: ctx.Generation, ProjectID: ctx.ProjectID, Revision: ctx.Revision, TrackID: ctx.TrackID, ClipID: ctx.ClipID, OpID: project.NewID("op"), Kind: "edit", Ops: []project.Op{{Kind: "note.update", TrackID: ctx.TrackID, ClipID: ctx.ClipID, NoteDeltas: []project.NoteDelta{{ID: "n_native", Pitch: &pitch}}}}}
}
func TestPianoBridgeCommitRequiresAcknowledgementAndSharesHistory(t *testing.T) {
	p, e, target := bridgeForTest(t)
	req := requestFor(p)
	receipt, err := p.Submit(req)
	if err != nil || receipt.Revision != 1 {
		t.Fatalf("submit %+v %v", receipt, err)
	}
	if e.State().Pending == nil || e.State().Pending.OpID != req.OpID || p.Context().Writable {
		t.Fatal("cleared journal before page received result")
	}
	got, _ := p.service.GetProject(target.ProjectID)
	if got.State.Tracks[0].Clips[0].Notes[0].Pitch != 67 || len(got.State.Tracks[0].Clips[0].Controls) != 1 {
		t.Fatal("edit or non-note preservation failed")
	}
	if err = p.Acknowledge("old-session", target.ProjectID, req.OpID); err == nil || e.State().Pending == nil {
		t.Fatal("stale page cleared journal")
	}
	p.freeze(true) // Closing must not prevent an already committed call's ack.
	if err = p.Acknowledge(req.SessionID, target.ProjectID, req.OpID); err != nil || e.State().Pending != nil {
		t.Fatalf("ack: %v", err)
	}
	if _, err = e.Execute(EditTarget{target.ProjectID, 1}, "undo", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = p.service.GetProject(target.ProjectID)
	if got.State.Tracks[0].Clips[0].Notes[0].Pitch != 60 {
		t.Fatal("Native undo did not reverse WebView edit")
	}
}
func TestPianoBridgeRejectsStaleFrozenAndOutOfScopeRequests(t *testing.T) {
	tests := []struct {
		name   string
		change func(*PianoPreview, *PianoEdit)
	}{
		{"wrong-session", func(p *PianoPreview, r *PianoEdit) { r.SessionID = "other" }},
		{"wrong-generation", func(p *PianoPreview, r *PianoEdit) { r.Generation++ }},
		{"wrong-revision", func(p *PianoPreview, r *PianoEdit) { r.Revision++ }},
		{"wrong-target", func(p *PianoPreview, r *PianoEdit) { r.Ops[0].TrackID = "t_other" }},
		{"container-edit", func(p *PianoPreview, r *PianoEdit) { r.Ops[0].Kind = "track.remove" }},
		{"extra-payload", func(p *PianoPreview, r *PianoEdit) { r.Ops[0].Title = "injected" }},
		{"clip-move", func(p *PianoPreview, r *PianoEdit) {
			n := int64(100)
			r.Ops = []project.Op{{Kind: "clip.update", TrackID: r.TrackID, ClipID: r.ClipID, ClipPatch: &project.ClipPatch{Start: &n}}}
		}},
		{"close-frozen", func(p *PianoPreview, r *PianoEdit) { p.freeze(true) }},
		{"page-not-ready", func(p *PianoPreview, r *PianoEdit) { p.mu.Lock(); p.readyGeneration = 0; p.mu.Unlock() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, e, target := bridgeForTest(t)
			req := requestFor(p)
			test.change(p, &req)
			if _, err := p.Submit(req); err == nil {
				t.Fatal("accepted invalid request")
			}
			got, _ := p.service.GetProject(target.ProjectID)
			if got.Revision != 0 || e.State().Pending != nil {
				t.Fatal("invalid request changed project or journal")
			}
		})
	}
}
func TestPianoReloadKeepsUnknownOutcomeAndRejectsOldAck(t *testing.T) {
	p, e, target := bridgeForTest(t)
	req := requestFor(p)
	if _, err := p.Submit(req); err != nil {
		t.Fatal(err)
	}
	newPage := p.Attach()
	if newPage.SessionID == req.SessionID || newPage.Writable {
		t.Fatal("reload reused old page grant or bypassed recovery")
	}
	if err := p.Acknowledge(req.SessionID, target.ProjectID, req.OpID); err == nil || e.State().Pending == nil {
		t.Fatal("old page erased recovery after reload")
	}
	restarted := NewEdits(p.service)
	if err := restarted.Check(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := restarted.Query(); err != nil || !found || restarted.State().Pending != nil {
		t.Fatalf("recovery: %v %v", found, err)
	}
	got, _ := p.service.GetProject(target.ProjectID)
	if got.Revision != 1 {
		t.Fatal("reload recovery replayed music")
	}
}
func TestPianoSelectionCannotRetargetNativeTool(t *testing.T) {
	p, _, _ := bridgeForTest(t)
	ctx := p.Context()
	if err := p.Selection(ctx.SessionID, ctx.Generation, 1, []string{"n_native"}, true); err != nil {
		t.Fatal(err)
	}
	frozen := p.command("delete", 0)
	if err := p.Selection(ctx.SessionID, ctx.Generation, 2, nil, true); err != nil {
		t.Fatal(err)
	}
	current := p.command("delete", 0)
	if frozen.SelectionVersion == current.SelectionVersion || len(frozen.NoteIDs) != 1 || len(current.NoteIDs) != 0 {
		t.Fatal("command did not freeze selection")
	}
	if err := p.Selection(ctx.SessionID, ctx.Generation, 1, []string{"n_native"}, true); err == nil {
		t.Fatal("late selection report overwrote newer one")
	}
	if err := p.Selection(ctx.SessionID, ctx.Generation, 3, []string{"n_other"}, true); err == nil {
		t.Fatal("accepted foreign note")
	}
}

func TestPianoCompiledTransformUsesVerifiedBridgeScope(t *testing.T) {
	p, e, _ := bridgeForTest(t)
	req := requestFor(p)
	var spec project.TransformSpec
	spec.Transpose = &struct {
		Semitones int `json:"semitones"`
	}{1}
	ops, err := p.PrepareNoteTransform(req.ProjectID, req.Revision, req.ClipID, []string{"n_native"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].TrackID != req.TrackID || ops[0].ClipID != req.ClipID {
		t.Fatalf("unscoped transform %+v", ops)
	}
	req.Ops = ops
	if _, err = p.Submit(req); err != nil {
		t.Fatal(err)
	}
	if e.State().Pending == nil {
		t.Fatal("missing acknowledgement journal")
	}
}
