package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func TestProjectPreviewAllEditKindsAndMixedCommitUndo(t *testing.T) {
	cases := []string{"project.rename", "tempo.set", "meter.set", "track.add", "track.update", "track.remove", "clip.add", "clip.update", "clip.remove", "note.add", "note.update", "note.remove", "control.add", "control.remove", "mixed"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			ctx, ids, tools := scopeFixture(t, nil)
			s := tools.svc
			p, _ := s.GetProject(ids.Project)
			if _, err := s.SubmitProjectOps(ids.Project, "preview-control-seed", "gui", p.Revision, []project.Op{{Kind: "control.add", ClipID: ids.C1, Events: []project.EventSpec{{ID: "seed_control", Kind: project.KindCC, Controller: 64, Value: 127}}}}); err != nil {
				t.Fatal(err)
			}
			before, _ := s.GetProject(ids.Project)
			noteID := before.State.Tracks[0].Clips[0].Notes[0].ID
			name, muted, program, start, pitch := "改名", true, 40, int64(480), 64
			ops := map[string][]project.Op{
				"project.rename": {{Kind: "project.rename", Title: "新标题"}},
				"tempo.set":      {{Kind: "tempo.set", Tempo: []project.TempoPoint{{Tick: 0, USPerQuarter: 750000}}}},
				"meter.set":      {{Kind: "meter.set", Meter: []project.MeterPoint{{Tick: 0, Numerator: 3, Denominator: 4}}}},
				"track.add":      {{Kind: "track.add", Track: &project.TrackSpec{ID: "preview_track", Name: "新轨"}}},
				"track.update":   {{Kind: "track.update", TrackID: ids.T1, TrackPatch: &project.TrackPatch{Name: &name, Muted: &muted, Program: &program}}},
				"track.remove":   {{Kind: "track.remove", TrackID: ids.T1}},
				"clip.add":       {{Kind: "clip.add", Clip: &project.ClipSpec{ID: "preview_clip", TrackID: ids.T1, Start: 1920, Length: 960}}},
				"clip.update":    {{Kind: "clip.update", ClipID: ids.C1, ClipPatch: &project.ClipPatch{Start: &start, Name: &name}}},
				"clip.remove":    {{Kind: "clip.remove", ClipID: ids.C1}},
				"note.add":       {{Kind: "note.add", ClipID: ids.C1, Notes: []project.NewNote{{ID: "preview_note", Start: 500, Duration: 100, Pitch: 72, Velocity: 80}}}},
				"note.update":    {{Kind: "note.update", ClipID: ids.C1, NoteDeltas: []project.NoteDelta{{ID: noteID, Pitch: &pitch}}}},
				"note.remove":    {{Kind: "note.remove", ClipID: ids.C1, NoteIDs: []string{noteID}}},
				"control.add":    {{Kind: "control.add", ClipID: ids.C1, Events: []project.EventSpec{{ID: "preview_event", Kind: project.KindProgram, Program: 40}}}},
				"control.remove": {{Kind: "control.remove", ClipID: ids.C1, EventIDs: []string{"seed_control"}}},
			}
			ops["mixed"] = append(append(append(append([]project.Op{}, ops["tempo.set"]...), ops["track.update"]...), ops["clip.update"]...), ops["note.update"]...)
			ops["mixed"] = append(ops["mixed"], ops["control.remove"]...)
			ops["mixed"] = append(ops["mixed"], project.Op{Kind: "track.remove", TrackID: ids.T2})
			payload, err := json.Marshal(projectEditArgs{ProjectID: ids.Project, OpID: "preview-all", BaseRevision: &before.Revision, Ops: ops[kind]})
			if err != nil {
				t.Fatal(err)
			}
			run, snapshot, err := s.store.CreateRunWithOptions(ctx, tools.sessionID, storage.CreateRunOptions{})
			if err != nil {
				t.Fatal(err)
			}
			tools.runID, tools.profile = run.ID, snapshot
			_, calls, err := s.store.AddAssistantMessage(ctx, tools.sessionID, run.ID, "", []storage.MessageToolCall{{ID: "candidate-all", Name: "project__edit", Arguments: string(payload)}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			history, _ := s.ProjectHistory(ids.Project)
			prepared, err := s.prepareProjectPreview(calls[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			view := prepared.view
			if view.Diff.Total+view.Objects.Total == 0 {
				t.Fatal("empty changes")
			}
			if kind != "note.add" && kind != "note.update" && kind != "note.remove" {
				if view.NotesOnly || view.Objects.Total == 0 || view.StartTick != 0 || view.OriginalEndTick != 960 || view.CandidateEndTick != previewScoreEnd(&prepared.after) {
					t.Fatalf("structural candidate window: %+v", view)
				}
			}
			afterPreview, _ := s.GetProject(ids.Project)
			historyAfter, _ := s.ProjectHistory(ids.Project)
			if !reflect.DeepEqual(before, afterPreview) || !reflect.DeepEqual(history, historyAfter) {
				t.Fatal("preview wrote state/history")
			}
			if _, err = s.ApproveProjectEditPreview(calls[0].ID, view.ArgumentsHash); err != nil {
				t.Fatal(err)
			}
			if _, err = tools.Call(context.Background(), "project__edit", string(payload)); err != nil {
				t.Fatal(err)
			}
			committed, _ := s.GetProject(ids.Project)
			if !reflect.DeepEqual(prepared.after, committed.State) {
				t.Fatal("preview disagreed with actual commit")
			}
			if _, err = s.UndoProject(ids.Project, "preview-all-undo", "gui", committed.Revision); err != nil {
				t.Fatal(err)
			}
			restored, _ := s.GetProject(ids.Project)
			if !reflect.DeepEqual(restored.State, before.State) {
				t.Fatal("shared undo lost music or metadata")
			}
		})
	}
}

func TestProjectPreviewMixedScopeAndNoOpRefused(t *testing.T) {
	for _, which := range []string{"scope", "noop"} {
		t.Run(which, func(t *testing.T) {
			ids, tools, call := previewFixture(t, "write", func(ids scopeIDs, rev int64) (string, string) {
				op := fmt.Sprintf(`{"kind":"clip.update","clipId":%q,"clipPatch":{"start":0}}`, ids.C1)
				if which == "scope" {
					op = `{"kind":"meter.set","meter":[{"tick":0,"numerator":3,"denominator":4}]}`
				}
				return "project__edit", editPayload(ids.Project, rev, "preview-noop", op)
			})
			before, _ := tools.svc.GetProject(ids.Project)
			if _, err := tools.svc.PreviewProjectEdit(call.ID); err == nil {
				t.Fatal("invalid candidate accepted")
			}
			after, _ := tools.svc.GetProject(ids.Project)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("invalid candidate wrote music")
			}
		})
	}
}

func TestProjectPreviewDeletingScopedClipDoesNotInventTrackDeletion(t *testing.T) {
	ids, tools, call := previewFixture(t, "write", func(ids scopeIDs, rev int64) (string, string) {
		return "project__edit", editPayload(ids.Project, rev, "preview-scoped-remove", fmt.Sprintf(`{"kind":"clip.remove","clipId":%q}`, ids.C1))
	})
	view, err := tools.svc.PreviewProjectEdit(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Objects.Total != 1 || view.Objects.Changes[0].Kind != "clip" || view.Objects.Changes[0].ObjectID != ids.C1 || view.Diff.Removed != 1 {
		t.Fatalf("invented parent deletion or leaked other scope: %+v", view)
	}
	before, _ := tools.svc.GetProject(ids.Project)
	if before.State.Tracks[0].ID != ids.T1 || len(before.State.Tracks[0].Clips) != 1 {
		t.Fatal("preview modified parent")
	}
}
