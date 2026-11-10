package playback

import (
	"context"
	"fmt"
	"slices"

	"icetudio/internal/project"
)

// ExportTrackFreeze renders the track before its fader and outgoing routes.
// Original clips, instrument and effects remain in the authoritative project.
func (m *Manager) ExportTrackFreeze(ctx context.Context, id string, revision int64, trackID, path string) error {
	item, err := m.get(id)
	if err != nil {
		return err
	}
	if item.Revision != revision {
		return fmt.Errorf("工程修订已变化")
	}
	i := slices.IndexFunc(item.State.Tracks, func(t project.Track) bool { return t.ID == trackID })
	if i < 0 || item.State.Mixer == nil {
		return fmt.Errorf("请选择带插件的轨道")
	}
	mix := item.State.Mixer
	ch := mix.Channel(trackID)
	if ch.FrozenAssetID != "" || ch.Instrument == nil && !slices.ContainsFunc(ch.Effects, func(f project.MixEffect) bool { return f.Plugin != nil && !f.Bypass }) {
		return fmt.Errorf("此轨道没有可冻结的插件链，或已被冻结")
	}
	for _, route := range mix.Routes {
		if route.To == trackID {
			return fmt.Errorf("冻结暂不支持有外部侧链输入的轨道")
		}
	}
	track := item.State.Tracks[i]
	if len(track.Clips) == 0 && len(track.Audio) == 0 {
		return fmt.Errorf("轨道没有可渲染的片段")
	}
	track.Muted, track.Solo = false, false
	item.State.Tracks = []project.Track{track}
	ch.Gain, ch.Pan = 1, 0
	newMix := project.Mixer{Channels: []project.MixChannel{ch}}
	for _, lane := range mix.Lanes {
		if (ch.Instrument != nil && lane.Target == ch.Instrument.ID) ||
			slices.ContainsFunc(ch.Effects, func(f project.MixEffect) bool { return f.ID == lane.Target }) {
			newMix.Lanes = append(newMix.Lanes, lane)
		}
	}
	item.State.Mixer = &newMix
	return m.exportSnapshot(ctx, ExportRequest{Bits: 16, Rate: SampleRate, TailMS: 1000}, path, item)
}
