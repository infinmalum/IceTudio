package playback

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"icetudio/internal/audioasset"
)

type ExportRequest struct {
	Rate              int
	ProjectID         string
	Revision          int64
	TrackID           string
	Start, End        int64 // absolute ticks; End=0 means project end
	TailMS            int
	Bits              int
	Normalize, Dither bool
}

func (m *Manager) Export(ctx context.Context, req ExportRequest, path string) error {
	format := audioasset.Format(path)
	if req.Rate == 0 {
		req.Rate = SampleRate
	}
	if req.Rate != 44100 && req.Rate != 48000 && req.Rate != 96000 {
		return fmt.Errorf("采样率须为 44100、48000 或 96000")
	}
	if format != "wav" && format != "flac" {
		return fmt.Errorf("请选择 WAV 或 FLAC 导出文件")
	}
	if req.Bits != 16 && req.Bits != 24 || req.TailMS < 0 || req.TailMS > 10000 || req.Start < 0 || req.End < 0 {
		return fmt.Errorf("导出参数无效")
	}
	item, err := m.get(req.ProjectID)
	if err != nil {
		return err
	}
	if item.Revision != req.Revision {
		return fmt.Errorf("工程修订已变化，未导出")
	}
	if req.TrackID != "" {
		found := false
		for i := range item.State.Tracks {
			t := &item.State.Tracks[i]
			t.Solo = t.ID == req.TrackID
			if t.Solo {
				t.Muted = false
				found = true
			}
		}
		if !found {
			return fmt.Errorf("导出轨道不存在")
		}
	}
	score, err := m.compileContext(ctx, item, false)
	if err != nil {
		return err
	}
	end := req.End
	if end == 0 {
		end = score.EndTick
	}
	if end <= req.Start || end > score.EndTick {
		return fmt.Errorf("导出区间不在工程范围")
	}
	tail := int64(req.TailMS) * SampleRate / 1000
	limit := score.EndFrame
	if req.End != 0 {
		limit = min(limit, score.Timeline.Frame(float64(end)))
	}
	frames := limit - score.Timeline.Frame(float64(req.Start)) + tail
	if frames < 1 || frames > 10*60*SampleRate {
		return fmt.Errorf("导出范围须为 1 帧至 10 分钟")
	}
	settings := m.cloneSettings()
	font := m.resolve(settings).path
	if font == "" {
		font = filepath.Join(m.dir, "audio", "basic-v1.sf2")
		if err = writeAtomic(font, BasicFont()); err != nil {
			return err
		}
	}
	engine, err := openNative(font, settings.Driver, true)
	if err != nil {
		return err
	}
	defer engine.close()
	// Use the same chase, synth and PCM mixer as live playback. After the
	// range endpoint the renderer emits only samples from the requested tail.
	if req.End != 0 {
		events := score.Events[:0]
		for _, e := range score.Events {
			if e.Frame < limit {
				events = append(events, e)
			}
		}
		score.Events = events
		score.audioLimit = limit
		for _, ch := range score.Channels {
			score.Events = append(score.Events, Event{Frame: limit, Kind: CC, Channel: ch, A: 64}, Event{Frame: limit, Kind: CC, Channel: ch, A: 66}, Event{Frame: limit, Kind: CC, Channel: ch, A: 123})
		}
	}
	score.EndFrame = limit + tail
	if err = engine.install(score, req.Start, 0, 0, false, true); err != nil {
		return err
	}
	if score.mix != nil && score.mix.latency > 0 {
		engine.render(score.mix.latency)
	}
	writeWAV := func(f *os.File) error {
		return writeRenderedWAV(ctx, f, engine, frames, req)
	}
	if format == "wav" && req.Rate == SampleRate {
		return audioasset.Publish(ctx, path, writeWAV)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".icetudio-flac-source-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = writeWAV(tmp)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return audioasset.Publish(ctx, path, func(f *os.File) error {
		in, e := os.Open(tmp.Name())
		if e != nil {
			return e
		}
		defer in.Close()
		args := []string{"-nostdin", "-v", "error", "-xerror", "-protocol_whitelist", "pipe,file", "-f", "wav", "-i", "pipe:0", "-ar", strconv.Itoa(req.Rate)}
		if format == "wav" {
			codec := "pcm_s16le"
			if req.Bits == 24 {
				codec = "pcm_s24le"
			}
			absolute, err := filepath.Abs(f.Name())
			if err != nil {
				return err
			}
			args = append(args, "-c:a", codec, "-f", "wav", "-y", absolute)
		} else {
			args = append(args, "-c:a", "flac", "-f", "flac", "pipe:1")
		}
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		cmd.Stdin, cmd.Stdout = in, f
		if e = cmd.Run(); e != nil {
			return fmt.Errorf("本机 FFmpeg 音频编码：%w", e)
		}
		return nil
	})
}

func writeRenderedWAV(ctx context.Context, dest *os.File, engine *native, frames int64, req ExportRequest) error {
	width := req.Bits / 8
	size := uint32(frames) * uint32(2*width)
	var header [44]byte
	copy(header[:4], "RIFF")
	copy(header[8:16], "WAVEfmt ")
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[4:], size+36)
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 2)
	binary.LittleEndian.PutUint32(header[24:], SampleRate)
	binary.LittleEndian.PutUint32(header[28:], uint32(SampleRate*2*width))
	binary.LittleEndian.PutUint16(header[32:], uint16(2*width))
	binary.LittleEndian.PutUint16(header[34:], uint16(req.Bits))
	binary.LittleEndian.PutUint32(header[40:], size)
	if _, err := dest.Write(header[:]); err != nil {
		return err
	}
	var scratch *os.File
	if req.Normalize {
		var err error
		scratch, err = os.CreateTemp(filepath.Dir(dest.Name()), ".icetudio-render-*")
		if err != nil {
			return err
		}
		defer os.Remove(scratch.Name())
		defer scratch.Close()
	}
	peak := float64(0)
	gain := float64(1)
	seed := uint32(0x1637fa1)
	noise := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed>>8) / 16777216
	}
	encode := func(samples []float32) error {
		buf := make([]byte, len(samples)*width)
		scale := float64(int64(1) << uint(req.Bits-1))
		for i, v := range samples {
			scaled := float64(v) * gain * scale
			if req.Dither {
				scaled += noise() - noise()
			}
			integer := int32(math.Round(max(-scale, min(scale-1, scaled))))
			for b := 0; b < width; b++ {
				buf[i*width+b] = byte(integer >> uint(8*b))
			}
		}
		_, err := dest.Write(buf)
		return err
	}
	for remaining := frames; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := int(min(remaining, 2048))
		l, r := engine.render(n)
		samples := make([]float32, n*2)
		for i := range l {
			samples[i*2], samples[i*2+1] = l[i], r[i]
		}
		if scratch == nil {
			if err := encode(samples); err != nil {
				return err
			}
		} else {
			raw := make([]byte, len(samples)*4)
			for i, v := range samples {
				peak = max(peak, math.Abs(float64(v)))
				binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
			}
			if _, err := scratch.Write(raw); err != nil {
				return err
			}
		}
		remaining -= int64(n)
	}
	if scratch == nil {
		return nil
	}
	if peak > 0 {
		gain = .99 / peak
	}
	if _, err := scratch.Seek(0, io.SeekStart); err != nil {
		return err
	}
	raw := make([]byte, 2048*8)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, e := io.ReadFull(scratch, raw)
		samples := make([]float32, n/4)
		for i := range samples {
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		}
		if err := encode(samples); err != nil {
			return err
		}
		if e == io.EOF || e == io.ErrUnexpectedEOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}
