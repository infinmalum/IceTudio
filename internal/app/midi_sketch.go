package app

import (
	"fmt"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

// PlayMIDISketch auditions generated notes over the target tempo map without
// publishing an edit. It uses the same stale revision and cancellation path
// as other immutable previews.
func (s *Service) PlayMIDISketch(id string, revision int64, trackID, clipID string, notes []project.NewNote) error {
	if s.playback == nil {
		return fmt.Errorf("原生试听不可用")
	}
	if len(notes) == 0 || len(notes) > 8192 {
		return fmt.Errorf("试听音符数量超出范围")
	}
	item, err := s.GetProject(id)
	if err != nil {
		return err
	}
	if item.Revision != revision {
		return fmt.Errorf("试听目标修订已变化")
	}
	for _, t := range item.State.Tracks {
		if t.ID != trackID {
			continue
		}
		for _, c := range t.Clips {
			if c.ID != clipID {
				continue
			}
			c.PatternID = ""
			c.Notes, c.Controls, c.Raw = nil, nil, nil
			first, end := c.Length, int64(0)
			for _, n := range notes {
				if n.Start < 0 || n.Duration < 1 || n.Start >= c.Length || n.Duration > c.Length-n.Start {
					return fmt.Errorf("试听音符超出片段")
				}
				first, end = min(first, n.Start), max(end, n.Start+n.Duration)
				c.Notes = append(c.Notes, project.Note{Start: n.Start, Duration: n.Duration, Pitch: n.Pitch, Velocity: n.Velocity, Channel: n.Channel})
			}
			c.Length = end
			t.Clips = []project.Clip{c}
			t.Muted, t.Solo = false, false
			item.State.Tracks = []project.Track{t}
			return s.playback.PlayPreview(playback.Request{ProjectID: id, Revision: revision, Tick: c.Start + first},
				playback.Snapshot{ID: id, Revision: revision, State: item.State}, "midi-sketch", "sketch")
		}
	}
	return fmt.Errorf("原试听片段已不存在")
}

func (s *Service) StopMIDISketch() {
	if s.playback != nil {
		s.playback.StopPreview("midi-sketch")
	}
}
