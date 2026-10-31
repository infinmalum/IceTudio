package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"icetudio/internal/project"
)

func TestTrackRemovalUndoRedoPreservesOrderAfterReopen(t *testing.T) {
	for _, removed := range [][]string{{"t_first"}, {"t_middle"}, {"t_first", "t_middle"}, {"t_middle", "t_first"}} {
		t.Run(fmt.Sprint(removed), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "order.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			p, err := s.CreateProject(ctx, "轨道顺序", 480)
			if err != nil {
				t.Fatal(err)
			}
			var ops []project.Op
			for _, id := range []string{"t_first", "t_middle", "t_last"} {
				ops = append(ops, project.Op{Kind: "track.add", Track: &project.TrackSpec{ID: id, Name: id}})
			}
			if _, err = s.SubmitProjectOps(ctx, p.ID, "seed", "gui", 0, ops); err != nil {
				t.Fatal(err)
			}
			before, _ := s.GetProject(ctx, p.ID)
			ops = nil
			for _, id := range removed {
				ops = append(ops, project.Op{Kind: "track.remove", TrackID: id})
			}
			if _, err = s.SubmitProjectOps(ctx, p.ID, "remove", "agent", before.Revision, ops); err != nil {
				t.Fatal(err)
			}
			deleted, _ := s.GetProject(ctx, p.ID)
			for cycle := 0; cycle < 3; cycle++ {
				for _, undo := range []bool{true, false} {
					if err = s.Close(); err != nil {
						t.Fatal(err)
					}
					s, err = Open(path)
					if err != nil {
						t.Fatal(err)
					}
					current, _ := s.GetProject(ctx, p.ID)
					opID := fmt.Sprintf("order-%d-%t", cycle, undo)
					if undo {
						_, err = s.UndoProject(ctx, p.ID, opID, "gui", current.Revision)
					} else {
						_, err = s.RedoProject(ctx, p.ID, opID, "gui", current.Revision)
					}
					if err != nil {
						t.Fatal(err)
					}
					actual, _ := s.GetProject(ctx, p.ID)
					want := deleted.State
					if undo {
						want = before.State
					}
					if !reflect.DeepEqual(actual.State, want) {
						t.Fatalf("cycle %d undo=%t changed track order: %+v", cycle, undo, actual.State.Tracks)
					}
				}
			}
		})
	}
}
