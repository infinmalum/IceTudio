package project

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
