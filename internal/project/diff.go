package project

// NoteChange contains read-only before/after projections. IDs allocated in a
// preview of note.add are temporary; only a committed Result owns stable IDs.
type NoteChange struct {
	TrackID   string `json:"trackId"`
	TrackName string `json:"trackName"`
	ClipID    string `json:"clipId"`
	ClipName  string `json:"clipName"`
	ClipStart int64  `json:"clipStart"`
	Before    *Note  `json:"before,omitempty"`
	After     *Note  `json:"after,omitempty"`
}

type NoteDiff struct {
	Added   int          `json:"added"`
	Updated int          `json:"updated"`
	Removed int          `json:"removed"`
	Total   int          `json:"total"`
	Changes []NoteChange `json:"changes"`
}

// DiffNotes counts the whole scoped change while bounding transported rows.
// Traverse musical order, never map order, to keep truncated views stable.
func DiffNotes(before, after *State, limit int) NoteDiff {
	out := NoteDiff{Changes: []NoteChange{}}
	old := map[string]Note{}
	for _, t := range before.Tracks {
		for _, c := range t.Clips {
			for _, n := range c.Notes {
				old[n.ID] = n
			}
		}
	}
	add := func(t Track, c Clip, b, a *Note) {
		out.Total++
		if len(out.Changes) < limit {
			out.Changes = append(out.Changes, NoteChange{t.ID, t.Name, c.ID, c.Name, c.Start, b, a})
		}
	}
	for _, t := range after.Tracks {
		for _, c := range t.Clips {
			for _, n := range c.Notes {
				b, found := old[n.ID]
				delete(old, n.ID)
				if !found {
					out.Added++
					add(t, c, nil, &n)
				} else if b != n {
					out.Updated++
					add(t, c, &b, &n)
				}
			}
		}
	}
	for _, t := range before.Tracks {
		for _, c := range t.Clips {
			for _, n := range c.Notes {
				if _, found := old[n.ID]; found {
					out.Removed++
					add(t, c, &n, nil)
				}
			}
		}
	}
	return out
}
