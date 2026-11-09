package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"icetudio/internal/project"
	"icetudio/internal/storage"
)

// Disposable opt-in smoke data only. Verifies offscreen note IDs remain in
// bulk selection/transforms; elapsed times are observations, not frame p95.
func (a *Workspace) verifyDensePiano(dir string) error {
	original := a.preview.Context()
	observations := map[string]any{}
	waitUI := func(read func() bool) error {
		return waitCondition(func() bool {
			value := false
			if err := a.onMain(func() { value = read() }); err != nil {
				return false
			}
			return value
		})
	}
	for _, count := range []int{1000, 10000} {
		for _, distribution := range []string{"dense", "sparse"} {
			p, err := a.service.CreateProject(fmt.Sprintf("Dense %d", count), 480)
			if err != nil {
				return err
			}
			result, err := a.service.SubmitProjectOps(p.ID, project.NewID("dense"), "gui", p.Revision, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{Name: "密集音轨"}}})
			if err != nil {
				return err
			}
			track := result.CreatedIDs[0]
			result, err = a.service.SubmitProjectOps(p.ID, project.NewID("dense"), "gui", result.Revision, []project.Op{{Kind: "clip.add", Clip: &project.ClipSpec{TrackID: track, Start: 0, Length: int64(count+1) * 480}}})
			if err != nil {
				return err
			}
			clip := result.CreatedIDs[0]
			notes := make([]project.NewNote, count)
			for i := range notes {
				notes[i] = project.NewNote{Start: int64(i%240) * 24, Duration: 24, Pitch: 48 + i%24, Velocity: 90}
				if distribution == "sparse" {
					notes[i].Start = int64(i) * 480
				}
			}
			result, err = a.service.SubmitProjectOps(p.ID, project.NewID("dense"), "gui", result.Revision, []project.Op{{Kind: "note.add", ClipID: clip, Notes: notes}})
			if err != nil {
				return err
			}
			before, err := a.service.GetProject(p.ID)
			if err != nil {
				return err
			}
			start := time.Now()
			if err := a.onMain(func() { a.install(before); a.selectClip(before.State.Tracks[0], before.State.Tracks[0].Clips[0]) }); err != nil {
				return err
			}
			if err := waitCondition(func() bool { return a.preview.toolsReady() }); err != nil {
				return fmt.Errorf("dense %d load: %w", count, err)
			}
			if err := a.onMain(func() { a.sendPianoCommand("selectAll", 0) }); err != nil {
				return err
			}
			if err := waitCondition(func() bool { return a.preview.selectionCount() == count }); err != nil {
				return fmt.Errorf("dense %d selection: %w", count, err)
			}
			perf, err := a.benchmarkEditorScroll()
			if err != nil {
				return fmt.Errorf("%d %s scroll benchmark: %w", count, distribution, err)
			}
			commitStart := time.Now()
			if err := a.onMain(func() { a.sendPianoCommand("transpose", 1) }); err != nil {
				return err
			}
			if err := waitUI(func() bool {
				return a.active.ID == p.ID && a.active.Revision == before.Revision+1 && a.edits.State().Pending == nil && a.preview.toolsReady()
			}); err != nil {
				return fmt.Errorf("%d %s transform ready: %w; %s", count, distribution, err, a.editDiagnostics())
			}
			commitElapsed := time.Since(commitStart).Milliseconds()
			after, err := a.service.GetProject(p.ID)
			if err != nil {
				return err
			}
			expected := map[string]int{}
			for _, n := range before.State.Tracks[0].Clips[0].Notes {
				expected[n.ID] = n.Pitch + 1
			}
			for _, n := range after.State.Tracks[0].Clips[0].Notes {
				if n.Pitch != expected[n.ID] {
					return fmt.Errorf("offscreen dense transform mismatch")
				}
			}
			if len(after.State.Tracks[0].Clips[0].Notes) != count {
				return fmt.Errorf("dense transform lost notes")
			}
			if err := a.onMain(func() { a.mutate("undo", nil) }); err != nil {
				return err
			}
			if err := waitUI(func() bool { return a.active.Revision == before.Revision+2 && !a.editBusy }); err != nil {
				return fmt.Errorf("%d %s undo ready: %w; %s", count, distribution, err, a.editDiagnostics())
			}
			observations[fmt.Sprintf("%d-%s", count, distribution)] = map[string]any{"selectionAndTransformMilliseconds": time.Since(start).Milliseconds(), "commitToReadyMilliseconds": commitElapsed, "scrollToTwoFrames": json.RawMessage(perf), "selected": count, "notesPreserved": true}
			if err := a.service.DeleteProject(p.ID); err != nil {
				return err
			}
		}
	}
	// Draw a real multi-track state without touching the original test project.
	st, _ := project.Blank("64 tracks", 480)
	for i := 0; i < 64; i++ {
		st.Tracks = append(st.Tracks, project.Track{ID: fmt.Sprintf("t_dense_%d", i), Name: fmt.Sprintf("音轨 %d", i+1), Clips: []project.Clip{{ID: fmt.Sprintf("c_dense_%d", i), Length: 1920, Notes: []project.Note{}}}})
	}
	if err := a.onMain(func() { a.install(storage.Project{ID: "p_dense_view", Title: st.Title, PPQ: st.PPQ, State: st}) }); err != nil {
		return err
	}
	image, err := a.win.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "native-64-tracks.png"), image, 0600); err != nil {
		return err
	}
	restored, err := a.service.GetProject(original.ProjectID)
	if err != nil {
		return err
	}
	if err := a.onMain(func() {
		a.install(restored)
		for _, t := range restored.State.Tracks {
			for _, c := range t.Clips {
				if c.ID == original.ClipID {
					a.selectClip(t, c)
				}
			}
		}
	}); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(observations, "", "  ")
	return os.WriteFile(filepath.Join(dir, "dense.json"), encoded, 0600)
}
