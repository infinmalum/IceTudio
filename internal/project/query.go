package project

// ScopeRegion narrows a state to a frozen run scope's allowlists
// (docs/p3-agent-scope.md §4). Empty allowlists mean the whole project. A
// track allowlist keeps those tracks with all their clips; a clips allowlist
// keeps only the listed clips and drops tracks left without any.
func ScopeRegion(st *State, tracks, clips []string) *State {
	out := *st
	if len(tracks) == 0 && len(clips) == 0 {
		return &out
	}
	trackSet, clipSet := map[string]bool{}, map[string]bool{}
	for _, id := range tracks {
		trackSet[id] = true
	}
	for _, id := range clips {
		clipSet[id] = true
	}
	out.Tracks = nil
	for _, tr := range st.Tracks {
		if len(trackSet) > 0 && !trackSet[tr.ID] {
			continue
		}
		kept := tr
		kept.Clips = nil
		for _, c := range tr.Clips {
			if len(clipSet) > 0 && !clipSet[c.ID] {
				continue
			}
			kept.Clips = append(kept.Clips, c)
		}
		if len(clipSet) > 0 && len(kept.Clips) == 0 {
			continue
		}
		out.Tracks = append(out.Tracks, kept)
	}
	return &out
}

// ClipTrack returns the owning track of a clip.
func ClipTrack(st *State, clipID string) (string, bool) {
	for _, tr := range st.Tracks {
		for _, c := range tr.Clips {
			if c.ID == clipID {
				return tr.ID, true
			}
		}
	}
	return "", false
}

// Region returns a filtered copy of the state: only the requested track
// (all tracks when trackID is empty), only the requested clip (or every
// clip when clipID is empty), and only the events inside the half-open
// tick window [start, end) when bounds are given. Notes count as in-window
// when they overlap it; control and raw events count by their tick. The
// tempo and meter maps are global context and pass through unchanged.
func Region(st *State, trackID, clipID string, start, end *int64) *State {
	out := *st
	scoped := trackID != "" || clipID != "" || start != nil || end != nil
	if !scoped {
		return &out
	}
	out.Tracks = nil
	for _, tr := range st.Tracks {
		if trackID != "" && tr.ID != trackID {
			continue
		}
		kept := tr
		kept.Clips = nil
		for _, c := range tr.Clips {
			if clipID != "" && c.ID != clipID {
				continue
			}
			kept.Clips = append(kept.Clips, clipWindow(c, start, end))
		}
		out.Tracks = append(out.Tracks, kept)
	}
	return &out
}

func clipWindow(c Clip, start, end *int64) Clip {
	if start == nil && end == nil {
		return c
	}
	lo, hi := int64(0), int64(1<<62)
	if start != nil {
		lo = *start
	}
	if end != nil {
		hi = *end
	}
	out := c
	out.Notes = nil
	out.Controls = nil
	out.Raw = nil
	for _, n := range c.Notes {
		if n.Start < hi && n.Start+n.Duration > lo {
			out.Notes = append(out.Notes, n)
		}
	}
	for _, e := range c.Controls {
		if e.Tick >= lo && e.Tick < hi {
			out.Controls = append(out.Controls, e)
		}
	}
	for _, e := range c.Raw {
		if e.Tick >= lo && e.Tick < hi {
			out.Raw = append(out.Raw, e)
		}
	}
	return out
}
