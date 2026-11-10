package playback

import (
	"context"
	"fmt"
	"math"

	"icetudio/internal/audioasset"
	"icetudio/internal/pluginhost"
	"icetudio/internal/project"
)

type audioRegion struct {
	Length  int64 // sampler output duration; ordinary PCM uses Clip.Frames
	Voice   *sampleVoice
	Track   int
	Asset   project.AudioAsset
	Clip    project.AudioClip
	Frame   int64
	Samples []int16
}

func compileAudio(st *project.State, score *Score, solo bool) error {
	count := 0
	for ti, t := range st.Tracks {
		if st.Mixer != nil {
			if id := st.Mixer.Channel(t.ID).FrozenAssetID; id != "" {
				asset, ok := project.FindAudioAsset(st, id)
				if !ok {
					return fmt.Errorf("冻结轨缺少音频资产")
				}
				score.EndFrame = max(score.EndFrame, asset.Frames)
				score.EndTick = max(score.EndTick, int64(math.Ceil(score.Timeline.Tick(asset.Frames))))
				if !t.Muted && (!solo || t.Solo) {
					score.Audio = append(score.Audio, audioRegion{Track: ti, Asset: asset,
						Clip: project.AudioClip{AssetID: id, Frames: asset.Frames, Gain: 100}})
				}
				continue
			}
		}
		for _, c := range t.Audio {
			count++
			asset, ok := project.FindAudioAsset(st, c.AssetID)
			if !ok {
				return fmt.Errorf("片段 %s 缺少音频资产", c.Name)
			}
			if err := project.ValidateAudioAsset(asset); err != nil {
				return err
			}
			if err := project.ValidateAudioClip(c, asset); err != nil {
				return err
			}
			start := score.Timeline.Frame(float64(c.Start))
			end := start + c.Frames
			if start < 0 || end < start || end > MaxFrames || count > project.MaxAudioClips {
				return fmt.Errorf("音频片段数量或位置超出播放范围")
			}
			score.EndFrame = max(score.EndFrame, end)
			score.EndTick = max(score.EndTick, int64(math.Ceil(score.Timeline.Tick(end))))
			if !t.Muted && (!solo || t.Solo) {
				score.Audio = append(score.Audio, audioRegion{Track: ti, Asset: asset, Clip: c, Frame: start})
			}
		}
	}
	return nil
}

func (m *Manager) compile(snapshot Snapshot, metronome bool) (*Score, error) {
	return m.compileContext(context.Background(), snapshot, metronome)
}
func (m *Manager) compileContext(ctx context.Context, snapshot Snapshot, metronome bool) (*Score, error) {
	score, err := compileWithMetronome(snapshot, metronome)
	if err != nil {
		return nil, err
	}
	score.catalog = pluginhost.New(m.dir)
	samples := map[string][]int16{}
	var bytes int64
	for i := range score.Audio {
		r := &score.Audio[i]
		if len(r.Samples) != 0 {
			continue
		}
		if cached, ok := samples[r.Asset.Digest]; ok {
			if int64(len(cached)) != r.Asset.Frames*2 {
				return nil, fmt.Errorf("相同音频哈希的帧数不一致")
			}
			r.Samples = cached
			continue
		}
		bytes += r.Asset.Frames * audioasset.FrameBytes
		if bytes > 256<<20 {
			return nil, fmt.Errorf("播放所需音频资产超过 256 MiB")
		}
		data, err := audioasset.LoadContext(ctx, m.dir, r.Asset)
		if err != nil {
			return nil, err
		}
		samples[r.Asset.Digest], r.Samples = data, data
	}
	return score, nil
}
