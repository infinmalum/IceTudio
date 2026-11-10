package playback

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"icetudio/internal/project"
)

const maxSamplerVoices = 16384

type sampleVoice struct {
	loop                         bool
	rate                         float64
	attack, decay, gate, release int64
	sustain, velocity            float64
}

func compileSampleNote(st *project.State, score *Score, track int, n project.Note, tick int64) error {
	tr := st.Tracks[track]
	start := score.Timeline.Frame(float64(tick + n.Start))
	gate := max(1, score.Timeline.Frame(float64(tick+n.Start+n.Duration))-start)
	for _, z := range tr.Sampler.Zones {
		if n.Pitch < z.Low || n.Pitch > z.High || n.Velocity < z.MinVelocity || n.Velocity > z.MaxVelocity {
			continue
		}
		a, ok := project.FindAudioAsset(st, z.AssetID)
		if !ok {
			return fmt.Errorf("采样器缺少资产")
		}
		rate := math.Exp2(float64(n.Pitch-z.Root) / 12)
		length := int64(math.Ceil(float64(z.Frames) / rate))
		attack, decay, release := int64(z.Attack)*48, int64(z.Decay)*48, int64(z.Release)*48
		hold := gate
		if z.OneShot {
			release = min(release, length)
			hold = length - release
		} else {
			length = min(length, gate+release)
		}
		if length < 1 || start < 0 || start+length > MaxFrames || len(score.Audio) >= maxSamplerVoices {
			return fmt.Errorf("采样声部数量或时长超限")
		}
		score.Audio = append(score.Audio, audioRegion{
			Track: track, Asset: a, Frame: start, Clip: project.AudioClip{Source: z.Source, Frames: z.Frames, Gain: z.Gain},
			Length: length, Voice: &sampleVoice{rate: rate, attack: attack, decay: decay, gate: hold, release: release, sustain: float64(z.Sustain) / 100, velocity: float64(n.Velocity) / 127},
		})
		score.EndFrame = max(score.EndFrame, start+length)
	}
	return nil
}

func validateSamplerPolyphony(regions []audioRegion) error {
	type boundary struct {
		frame int64
		delta int
	}
	var points []boundary
	for _, r := range regions {
		if r.Voice != nil {
			points = append(points, boundary{r.Frame, 1}, boundary{r.Frame + r.Length, -1})
		}
	}
	slices.SortFunc(points, func(a, b boundary) int {
		if a.frame < b.frame {
			return -1
		}
		if a.frame > b.frame {
			return 1
		}
		return a.delta - b.delta
	})
	voices := 0
	for _, p := range points {
		voices += p.delta
		if voices > 128 {
			return fmt.Errorf("采样器同时发声超过 128 个声部")
		}
	}
	return nil
}

func (m *Manager) RenderSamplerPreview(ctx context.Context, st project.State, track project.Track, req AuditionRequest) ([]byte, error) {
	if track.Sampler == nil || len(req.Keys) < 1 || len(req.Keys) > 8 || req.DurationMs < 50 || req.DurationMs > 5000 {
		return nil, fmt.Errorf("采样器试听参数无效")
	}
	duration := int64(req.DurationMs)
	track.Audio = nil
	track.Muted, track.Solo = false, false
	track.Clips = []project.Clip{{Length: duration}}
	clip := &track.Clips[0]
	velocity := req.Velocity
	if velocity == 0 {
		velocity = 100
	}
	for _, key := range req.Keys {
		clip.Notes = append(clip.Notes, project.Note{Pitch: key, Velocity: velocity, Duration: duration})
	}
	st.Tracks, st.Mixer = []project.Track{track}, nil
	st.PPQ = 1000
	st.Tempo = []project.TempoPoint{{USPerQuarter: 1000000}}
	score, err := m.compileContext(ctx, Snapshot{State: st}, false)
	if err != nil {
		return nil, err
	}
	if len(score.Audio) == 0 {
		return nil, fmt.Errorf("该音高与力度没有映射区域")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	font := filepath.Join(m.dir, "audio", "basic-v1.sf2")
	if _, err = os.Stat(font); os.IsNotExist(err) {
		if err = writeAtomic(font, BasicFont()); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	native, err := openNative(font, "", true)
	if err != nil {
		return nil, err
	}
	defer native.close()
	if err = native.install(score, 0, 0, 0, false, true); err != nil {
		return nil, err
	}
	frames := min(int(score.EndFrame), int(duration+350)*48)
	left, right := native.render(frames)
	buf := make([]byte, frames*4)
	for i := 0; i < frames; i++ {
		l, r := left[i], right[i]
		// Avoid a hard stop when audition ends before a long sample.
		gain := min(1, float32(frames-i)/96)
		binary.LittleEndian.PutUint16(buf[i*4:], uint16(int16(max(-32768, min(32767, l*gain*32768)))))
		binary.LittleEndian.PutUint16(buf[i*4+2:], uint16(int16(max(-32768, min(32767, r*gain*32768)))))
	}
	return buf, ctx.Err()
}
