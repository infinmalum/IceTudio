package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"icetudio/internal/audioasset"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (s *Service) Materials() ([]audioasset.Material, error) { return s.library.List() }
func (s *Service) ScanMaterials(ctx context.Context, path string) (int, error) {
	return s.library.Scan(ctx, path)
}
func (s *Service) UpdateMaterial(m audioasset.Material) error { return s.library.Update(m) }
func (s *Service) AnalyzeMaterial(ctx context.Context, id string) (audioasset.Analysis, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.library.Analyze(ctx, id)
}
func (s *Service) PreviewMaterial(ctx context.Context, id string, volume float64) error {
	return s.library.Preview(ctx, id, volume)
}
func (s *Service) PreviewAudioAsset(ctx context.Context, asset project.AudioAsset, start, frames int64, volume float64) error {
	return s.library.PreviewAsset(ctx, asset, start, frames, volume)
}
func (s *Service) StopMaterialPreview() {
	if s.library != nil {
		s.library.StopPreview()
	}
}
func (s *Service) PrepareMaterialAudio(ctx context.Context, id string, reference bool) (project.AudioAsset, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.library.Prepare(ctx, id, reference)
}

func (s *Service) RelinkAudioAsset(ctx context.Context, id string, revision int64, assetID, path string) error {
	p, err := s.GetProject(id)
	if err != nil {
		return err
	}
	if p.Revision != revision {
		return fmt.Errorf("工程已变化，请重新选择引用")
	}
	asset, ok := project.FindAudioAsset(&p.State, assetID)
	if !ok {
		return fmt.Errorf("找不到音频资产")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = audioasset.Reference(ctx, s.dataDir, path, &asset)
	return err
}

func (s *Service) projectAudio(id string, revision int64, track, clip string) (*project.State, project.AudioClip, project.AudioAsset, error) {
	p, err := s.GetProject(id)
	if err != nil {
		return nil, project.AudioClip{}, project.AudioAsset{}, err
	}
	if p.Revision != revision {
		return nil, project.AudioClip{}, project.AudioAsset{}, fmt.Errorf("工程修订已变化")
	}
	for _, t := range p.State.Tracks {
		if t.ID != track {
			continue
		}
		for _, c := range t.Audio {
			if c.ID != clip {
				continue
			}
			if a, ok := project.FindAudioAsset(&p.State, c.AssetID); ok {
				return &p.State, c, a, nil
			}
		}
	}
	return nil, project.AudioClip{}, project.AudioAsset{}, fmt.Errorf("原片段或资产不存在")
}

func (s *Service) PrepareAudioPolish(ctx context.Context, id string, rev int64, track, clip string, spec audioasset.Polish) ([]project.Op, project.AudioAsset, error) {
	_, c, a, err := s.projectAudio(id, rev, track, clip)
	if err != nil {
		return nil, a, err
	}
	result, err := audioasset.PreparePolish(ctx, s.dataDir, a, c, spec)
	if err != nil {
		return nil, result, err
	}
	originalFrames := c.Frames
	c.FadeIn = min(result.Frames, c.FadeIn*result.Frames/originalFrames)
	c.FadeOut = min(result.Frames-c.FadeIn, c.FadeOut*result.Frames/originalFrames)
	c.AssetID, c.Source, c.Frames, c.Reverse = result.ID, 0, result.Frames, false
	return []project.Op{{Kind: "audio.asset", AudioAsset: &result}, {Kind: "audio.update", TrackID: track, AudioClip: &c}}, result, nil
}

func (s *Service) PrepareAudioSlices(ctx context.Context, id string, rev int64, track, clip string) ([]project.Op, error) {
	st, c, a, err := s.projectAudio(id, rev, track, clip)
	if err != nil {
		return nil, err
	}
	if c.Frames > 60*project.AudioRate {
		return nil, fmt.Errorf("自动切片请先将源窗口裁到一分钟以内")
	}
	raw, err := audioasset.Window(ctx, s.dataDir, a, c)
	if err != nil {
		return nil, err
	}
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(uint16(raw[i*2]) | uint16(raw[i*2+1])<<8)
	}
	hits := audioasset.Onsets(samples)
	if len(hits) > 129 {
		return nil, fmt.Errorf("检测到过多瞬态，请裁短片段")
	}
	timeline, err := playback.NewTimeline(st)
	if err != nil {
		return nil, err
	}
	base := timeline.Frame(float64(c.Start))
	cuts := []int64{0}
	ticks := []int64{c.Start}
	for _, hit := range hits {
		tick := int64(math.Round(timeline.Tick(base + hit)))
		frame := timeline.Frame(float64(tick)) - base
		if tick > ticks[len(ticks)-1] && tick <= project.MaxTick && frame > cuts[len(cuts)-1] && frame < c.Frames {
			cuts = append(cuts, frame)
			ticks = append(ticks, tick)
		}
	}
	if len(cuts) < 2 {
		return nil, fmt.Errorf("未发现可分割的瞬态")
	}
	cuts = append(cuts, c.Frames)
	ops := make([]project.Op, 0, len(ticks))
	for i, tick := range ticks {
		part := c
		part.Start, part.Source, part.Frames = tick, c.Source+cuts[i], cuts[i+1]-cuts[i]
		if c.Reverse {
			part.Source = c.Source + c.Frames - cuts[i+1]
		}
		part.FadeIn, part.FadeOut = 0, 0
		if i == 0 {
			part.FadeIn = min(part.Frames, c.FadeIn)
		}
		if i+1 == len(ticks) {
			part.FadeOut = min(part.Frames, c.FadeOut)
		}
		kind := "audio.update"
		if i > 0 {
			kind = "audio.add"
			part.ID = project.NewID("ac")
		}
		ops = append(ops, project.Op{Kind: kind, TrackID: track, AudioClip: &part})
	}
	return ops, ctx.Err()
}

func (s *Service) AuditionProjectSampler(id string, revision int64, trackID string, req playback.AuditionRequest) error {
	if s.playback == nil {
		return fmt.Errorf("此宿主未启用采样试听")
	}
	err := s.library.PreviewPCM(context.Background(), .72, func(ctx context.Context) ([]byte, error) {
		p, err := s.GetProject(id)
		if err != nil {
			return nil, err
		}
		if p.Revision != revision {
			return nil, fmt.Errorf("采样器修订已变化")
		}
		for _, t := range p.State.Tracks {
			if t.ID == trackID {
				return s.playback.RenderSamplerPreview(ctx, p.State, t, req)
			}
		}
		return nil, fmt.Errorf("采样器轨道不存在")
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
