package app

import (
	"fmt"
	"icetudio/internal/project"
)

// PrepareNoteTransform is a read-only GUI compiler. The caller must submit
// the resulting ops through the normal recovery/transaction path using the
// same base revision. It never widens selection to unselected notes.
func (s *Service) PrepareNoteTransform(projectID string, baseRevision int64, clipID string, noteIDs []string, spec project.TransformSpec) ([]project.Op, error) {
	if len(noteIDs) == 0 || len(noteIDs) > project.MaxNotes {
		return nil, fmt.Errorf("请先选择要变换的音符")
	}
	item, err := s.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	if item.Revision != baseRevision {
		return nil, fmt.Errorf("工程已修改，请重新选择音符后再变换")
	}
	ids := make(map[string]bool, len(noteIDs))
	for _, id := range noteIDs {
		if ids[id] {
			return nil, fmt.Errorf("音符选择重复")
		}
		ids[id] = true
	}
	for _, track := range item.State.Tracks {
		for _, clip := range track.Clips {
			if clip.ID == clipID {
				chosen := []project.Note{}
				for _, n := range clip.Notes {
					if ids[n.ID] {
						chosen = append(chosen, n)
					}
				}
				if len(chosen) != len(ids) {
					return nil, fmt.Errorf("选中的音符已不存在，请重新选择")
				}
				clip.Notes = chosen
				track.Clips = []project.Clip{clip}
				item.State.Tracks = []project.Track{track}
				ops, stats, err := project.CompileTransform(&item.State, project.Selection{ClipIDs: []string{clipID}}, spec)
				if err != nil {
					return nil, err
				}
				// GUI selection is exact: reject a partial transpose rather than silently
				// skipping notes outside the supported MIDI pitch range.
				if stats.SkippedPitch > 0 || stats.SkippedTime > 0 {
					return nil, fmt.Errorf("部分音符变换后越界，整组选中音符未修改")
				}
				return ops, nil
			}
		}
	}
	return nil, fmt.Errorf("目标片段已不存在")
}
