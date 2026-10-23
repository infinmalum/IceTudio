package app

import (
	"context"
	"os"
	"testing"
	"time"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func TestPlaybackUnavailableDoesNotBlockEditing(t *testing.T) {
	svc, _ := newTestService(t, nil, nil)
	if _, err := svc.CreateProject("offline", 480); err != nil {
		t.Fatal(err)
	}
	if svc.PlaybackStatus().Available {
		t.Fatal("test service unexpectedly enabled audio")
	}
	if err := svc.PlayProject(playback.Request{}); err == nil {
		t.Fatal("missing playback must explain unavailable")
	}
}
func TestLiveProjectPlaybackUpdates(t *testing.T) {
	if os.Getenv("ICETUDIO_LIVE_AUDIO") != "1" {
		t.Skip("opt-in actual audio driver test")
	}
	svc, store := newTestService(t, nil, nil)
	manager := playback.New(t.TempDir(), func(id string) (playback.Snapshot, error) {
		p, err := store.GetProject(context.Background(), id)
		return playback.Snapshot{ID: p.ID, Revision: p.Revision, State: p.State}, err
	})
	svc.playback = manager
	t.Cleanup(manager.Close)
	p, err := svc.CreateProject("live project", 480)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SubmitProjectOps(p.ID, "setup", "gui", 0, []project.Op{{Kind: "track.add", Track: &project.TrackSpec{ID: "t_live"}}, {Kind: "clip.add", Clip: &project.ClipSpec{ID: "c_live", TrackID: "t_live", Length: 3840}}, {Kind: "note.add", ClipID: "c_live", Notes: []project.NewNote{{Duration: 1920, Pitch: 69, Velocity: 80}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.PlayProject(playback.Request{ProjectID: p.ID, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	wait := func(revision int64) playback.Status {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			s := svc.PlaybackStatus()
			if s.Revision == revision && s.Callbacks > 2 {
				return s
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("audio update timeout: %+v", svc.PlaybackStatus())
		return playback.Status{}
	}
	wait(1)
	stored, err := store.GetProject(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	pitch := 72
	if _, err = svc.SubmitProjectOps(p.ID, "edit", "cli", 1, []project.Op{{Kind: "note.update", ClipID: "c_live", NoteDeltas: []project.NoteDelta{{ID: stored.State.Tracks[0].Clips[0].Notes[0].ID, Pitch: &pitch}}}}); err != nil {
		t.Fatal(err)
	}
	s := wait(2)
	if !s.Playing {
		t.Fatal("edit stopped audio")
	}
	if _, err = svc.UndoProject(p.ID, "undo", "gui", 2); err != nil {
		t.Fatal(err)
	}
	wait(3)
	if err = svc.StopPlayback(); err != nil {
		t.Fatal(err)
	}
	if svc.PlaybackStatus().Playing {
		t.Fatal("stop failed")
	}
	if err = svc.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if svc.PlaybackStatus().ProjectID != "" {
		t.Fatal("deleted project retained audio target")
	}
	t.Logf("native playback/app commits/undo verified; callbacks=%d misses=%d", s.Callbacks, s.MissedBlocks)
}
