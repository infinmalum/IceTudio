package app

import (
	"context"
	"fmt"
	"slices"

	"icetudio/internal/audioasset"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

type CompPart struct {
	TakeID               string `json:"takeId"`
	Source, Frames, Tick int64
}

type compSource struct {
	status   audioasset.TakeStatus
	archived *project.AudioAsset
}

func (s *Service) compSources(id string, st *project.State) ([]compSource, error) {
	list, err := s.recordings.List()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var sources []compSource
	for _, t := range list {
		if t.ProjectID != id || t.Kind != "audio" || t.Active || t.Frames < 1 {
			continue
		}
		seen[t.ID] = true
		sources = append(sources, compSource{status: t})
	}
	for _, a := range st.AudioAssets {
		if a.TakeID == "" || seen[a.TakeID] {
			continue
		}
		seen[a.TakeID] = true
		sources = append(sources, compSource{status: audioasset.TakeStatus{Take: audioasset.Take{
			ID: a.TakeID, ProjectID: id, Kind: "audio", LoopFrames: a.TakeLoopFrames}, Frames: a.Frames}, archived: &a})
	}
	return sources, nil
}

func (s *Service) AudioTakes(id string) ([]audioasset.TakeStatus, error) {
	p, err := s.GetProject(id)
	if err != nil {
		return nil, err
	}
	sources, err := s.compSources(id, &p.State)
	if err != nil {
		return nil, err
	}
	out := make([]audioasset.TakeStatus, 0, len(sources))
	for _, src := range sources {
		out = append(out, src.status)
	}
	return out, nil
}

func (s *Service) PreviewTake(ctx context.Context, id, takeID string, source, frames int64, volume float64) error {
	p, err := s.GetProject(id)
	if err != nil {
		return err
	}
	list, err := s.compSources(id, &p.State)
	if err != nil {
		return err
	}
	for _, t := range list {
		if t.status.ID != takeID {
			continue
		}
		if t.archived != nil {
			return s.PreviewAudioAsset(ctx, *t.archived, source, frames, volume)
		}
		return s.library.PreviewPCM(ctx, volume, func(ctx context.Context) ([]byte, error) {
			return s.recordings.ReadTakeWindow(takeID, source, min(frames, 20*project.AudioRate))
		})
	}
	return fmt.Errorf("找不到可用的工程 take")
}

// PrepareComp only stages source content. The user submits the entire batch
// with the original target revision through the normal recovery coordinator.
func (s *Service) PrepareComp(ctx context.Context, id string, rev int64, track string, parts []CompPart) ([]project.Op, error) {
	if len(parts) < 1 || len(parts) > 32 {
		return nil, fmt.Errorf("Comp 须选择 1–32 个选段")
	}
	p, err := s.GetProject(id)
	if err != nil {
		return nil, err
	}
	if p.Revision != rev {
		return nil, fmt.Errorf("工程修订已变化")
	}
	if !slices.ContainsFunc(p.State.Tracks, func(t project.Track) bool { return t.ID == track }) {
		return nil, fmt.Errorf("Comp 目标轨不存在")
	}
	timeline, err := playback.NewTimeline(&p.State)
	if err != nil {
		return nil, err
	}
	list, err := s.compSources(id, &p.State)
	if err != nil {
		return nil, err
	}
	takes := map[string]compSource{}
	for _, t := range list {
		takes[t.status.ID] = t
	}
	sorted := slices.Clone(parts)
	slices.SortFunc(sorted, func(a, b CompPart) int {
		if a.Tick < b.Tick {
			return -1
		}
		if a.Tick > b.Tick {
			return 1
		}
		return 0
	})
	end := int64(-1)
	for _, part := range sorted {
		t, ok := takes[part.TakeID]
		if !ok || part.Tick < 0 || part.Tick > project.MaxTick || part.Source < 0 || part.Source >= t.status.Frames || part.Frames < 1 || part.Frames > t.status.Frames-part.Source {
			return nil, fmt.Errorf("Comp 的 take、源帧或位置无效")
		}
		start := timeline.Frame(float64(part.Tick))
		if start < end || start > playback.MaxFrames-part.Frames {
			return nil, fmt.Errorf("Comp 选段重叠或超出播放范围")
		}
		end = start + part.Frames
	}
	assets := map[string]project.AudioAsset{}
	var ops []project.Op
	for i, part := range sorted {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		asset, ok := assets[part.TakeID]
		if archived := takes[part.TakeID].archived; archived != nil {
			asset, ok = *archived, true
		}
		if !ok {
			_, asset, err = s.recordings.Recover(part.TakeID)
			if err != nil {
				return nil, err
			}
			j := slices.IndexFunc(p.State.AudioAssets, func(a project.AudioAsset) bool {
				return a.TakeID == part.TakeID && a.Digest == asset.Digest && a.Frames == asset.Frames
			})
			if j >= 0 {
				asset = p.State.AudioAssets[j]
			} else {
				ops = append(ops, project.Op{Kind: "audio.asset", AudioAsset: &asset})
			}
			assets[part.TakeID] = asset
		}
		if part.Frames > asset.Frames-part.Source {
			return nil, fmt.Errorf("录音源长度已变化")
		}
		fade := min(int64(96), part.Frames/4)
		clip := &project.AudioClip{ID: project.NewID("ac"), AssetID: asset.ID, Name: fmt.Sprintf("Comp · %02d", i+1), Start: part.Tick,
			Source: part.Source, Frames: part.Frames, Gain: 100, FadeIn: fade, FadeOut: fade}
		ops = append(ops, project.Op{Kind: "audio.add", TrackID: track, AudioClip: clip})
	}
	return ops, ctx.Err()
}
