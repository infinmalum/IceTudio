package audioasset

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"time"

	"icetudio/internal/project"
)

type Polish struct {
	Percent int // output length relative to source
	Cents   int
	Correct bool // estimate a stable monophonic pitch
	Target  int  // MIDI note or -1 for the nearest semitone
}

func Window(ctx context.Context, data string, asset project.AudioAsset, clip project.AudioClip) ([]byte, error) {
	if err := project.ValidateAudioClip(clip, asset); err != nil {
		return nil, err
	}
	f, err := openVerified(ctx, data, asset)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err = io.CopyN(io.Discard, f, clip.Source*FrameBytes); err != nil {
		return nil, err
	}
	raw := make([]byte, clip.Frames*FrameBytes)
	if _, err = io.ReadFull(f, raw); err != nil {
		return nil, err
	}
	if clip.Reverse {
		for i, j := 0, len(raw)-FrameBytes; i < j; i, j = i+FrameBytes, j-FrameBytes {
			for k := 0; k < FrameBytes; k++ {
				raw[i+k], raw[j+k] = raw[j+k], raw[i+k]
			}
		}
	}
	return raw, ctx.Err()
}

func PreparePolish(ctx context.Context, data string, asset project.AudioAsset, clip project.AudioClip, spec Polish) (project.AudioAsset, error) {
	fail := func(e error) (project.AudioAsset, error) { return project.AudioAsset{}, e }
	if spec.Percent < 50 || spec.Percent > 200 || spec.Cents < -1200 || spec.Cents > 1200 || spec.Target < -1 || spec.Target > 127 {
		return fail(fmt.Errorf("拉伸须为 50–200%%，移调须为 ±1200 音分，目标音为 MIDI 0–127 或自动"))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	raw, err := Window(ctx, data, asset, clip)
	if err != nil {
		return fail(err)
	}
	if spec.Correct {
		result := Analyze(pcmSamples(raw[:min(len(raw), AnalysisFrames*FrameBytes)]))
		if !result.Pitched {
			return fail(fmt.Errorf("未检测到稳定单声部音高，可手动输入移调音分"))
		}
		target := float64(spec.Target)
		if spec.Target < 0 {
			target = math.Round(result.MIDI)
		}
		spec.Cents = int(math.Round((target - result.MIDI) * 100))
		if spec.Cents < -1200 || spec.Cents > 1200 {
			return fail(fmt.Errorf("目标音超出 ±12 半音校正范围"))
		}
	}
	frames := (clip.Frames*int64(spec.Percent) + 50) / 100
	if frames < 1 || frames > project.MaxAudioFrames {
		return fail(fmt.Errorf("处理后时长超过音频上限"))
	}
	if clip.Frames < 4800 {
		return fail(fmt.Errorf("拉伸和音高校正需要至少 100 ms 音频"))
	}
	// Only bounded numeric arguments can enter the filter expression.
	filter := fmt.Sprintf("rubberband=tempo=%.9f:pitch=%.9f:formant=preserved,apad,atrim=end_sample=%d", 100/float64(spec.Percent), math.Exp2(float64(spec.Cents)/1200), frames)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-xerror", "-f", "s16le", "-ar", "48000", "-ac", "2", "-i", "pipe:0", "-af", filter, "-f", "s16le", "pipe:1")
	cmd.Stdin = bytes.NewReader(raw)
	log := &limitedLog{}
	cmd.Stderr = log
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	if err = cmd.Start(); err != nil {
		return fail(err)
	}
	stage, err := stagePCM(data, out)
	if err != nil {
		cancel()
	}
	wait := cmd.Wait()
	if stage != "" {
		defer os.Remove(stage)
	}
	if wait != nil {
		return fail(fmt.Errorf("音频精修失败，请检查 FFmpeg 的 Rubber Band 滤镜：%w %s", wait, log.String()))
	}
	if err != nil {
		return fail(err)
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	info, err := os.Stat(stage)
	if err != nil || info.Size() != frames*FrameBytes {
		return fail(fmt.Errorf("派生音频帧数不符"))
	}
	result, err := publishStage(data, clip.Name+" · 精修", stage)
	if err == nil {
		result.Derivation = &project.AudioDerivation{SourceID: asset.ID, SourceDigest: asset.Digest, Source: clip.Source, Frames: clip.Frames, Reverse: clip.Reverse, Percent: spec.Percent, Cents: spec.Cents, Algorithm: "ffmpeg-rubberband"}
	}
	return result, err
}
