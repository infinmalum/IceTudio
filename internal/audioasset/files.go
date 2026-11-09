// Package audioasset manages local immutable stereo PCM assets and atomic exports.
package audioasset

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"icetudio/internal/project"
)

const FrameBytes = 4 // 48 kHz, interleaved stereo signed little-endian PCM16
const MaxBytes = project.MaxAudioFrames * FrameBytes

func Root(data string) string { return filepath.Join(data, "audio-assets") }

func decodeKind(header []byte) string {
	if len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE" {
		return "wav"
	}
	if len(header) >= 4 && string(header[:4]) == "fLaC" {
		return "flac"
	}
	return ""
}

func Import(ctx context.Context, data, path string) (project.AudioAsset, error) {
	stage, err := decodePCM(ctx, data, path)
	if err != nil {
		return project.AudioAsset{}, err
	}
	defer os.Remove(stage)
	return publishStage(data, filepath.Base(path), stage)
}

func decodePCM(ctx context.Context, data, path string) (string, error) {
	if data == "" {
		return "", fmt.Errorf("未配置音频数据目录")
	}
	pre, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !pre.Mode().IsRegular() {
		return "", fmt.Errorf("音频源不是普通文件")
	}
	f, err := openAudioSource(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 512<<20 || fi.Size() < 12 {
		return "", fmt.Errorf("音频源须为不超过 512 MiB 的普通 WAV/FLAC 文件")
	}
	source := bufio.NewReader(f)
	header, err := source.Peek(12)
	if err != nil {
		return "", err
	}
	format := decodeKind(header)
	if format == "" {
		return "", fmt.Errorf("仅支持 WAV 和 FLAC")
	}
	if format == "wav" && int64(binary.LittleEndian.Uint32(header[4:8]))+8 > fi.Size() {
		return "", fmt.Errorf("WAV 容器已截断")
	}
	var expected int64
	if format == "flac" {
		h, e := source.Peek(42)
		if e != nil || h[4]&127 != 0 || h[5] != 0 || h[6] != 0 || h[7] != 34 {
			return "", fmt.Errorf("FLAC STREAMINFO 缺失或截断")
		}
		meta := binary.BigEndian.Uint64(h[18:26])
		rate, samples := int64(meta>>44), int64(meta&((1<<36)-1))
		if rate < 1 || samples > 0 && samples*48000/rate > project.MaxAudioFrames {
			return "", fmt.Errorf("FLAC 采样率或时长无效")
		}
		expected = samples * 48000 / rate
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-xerror", "-err_detect", "explode", "-protocol_whitelist", "pipe",
		"-f", format, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn", "-ar", "48000", "-ac", "2", "-f", "s16le", "pipe:1")
	cmd.Stdin = source
	log := &limitedLog{}
	cmd.Stderr = log
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", fmt.Errorf("启动本机 FFmpeg 解码：%w", err)
	}
	staging, err := stagePCM(data, out)
	if err != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return "", err
	}
	if waitErr != nil {
		os.Remove(staging)
		return "", fmt.Errorf("音频解码失败：%w %s", waitErr, log.String())
	}
	if expected > 0 {
		stat, e := os.Stat(staging)
		if e != nil {
			os.Remove(staging)
			return "", e
		}
		frames := stat.Size() / FrameBytes
		if frames < expected-1 || frames > expected+1 {
			os.Remove(staging)
			return "", fmt.Errorf("FLAC 声明的采样数与解码不符")
		}
	}
	return staging, nil
}

type limitedLog struct{ bytes.Buffer }

func (l *limitedLog) Write(p []byte) (int, error) {
	left := max(0, 2048-l.Len())
	_, _ = l.Buffer.Write(p[:min(len(p), left)])
	return len(p), nil
}

func stagePCM(data string, r io.Reader) (string, error) {
	dir := Root(data)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".pcm-stage-*")
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, MaxBytes+1))
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n == 0 || n > MaxBytes || n%FrameBytes != 0 {
		os.Remove(f.Name())
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return "", fmt.Errorf("音频为空、已截断或超过十分钟上限")
	}
	return f.Name(), nil
}

func PublishPCM(data, name, path string) (project.AudioAsset, error) {
	if data == "" {
		return project.AudioAsset{}, fmt.Errorf("未配置音频数据目录")
	}
	f, err := os.Open(path)
	if err != nil {
		return project.AudioAsset{}, err
	}
	stage, err := stagePCM(data, f)
	f.Close()
	if err != nil {
		return project.AudioAsset{}, err
	}
	defer os.Remove(stage)
	return publishStage(data, name, stage)
}

func describePCM(name, stage string) (project.AudioAsset, error) {
	f, err := os.Open(stage)
	if err != nil {
		return project.AudioAsset{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return project.AudioAsset{}, err
	}
	hash := sha256.New()
	frames := fi.Size() / FrameBytes
	bin := max(int64(1), (frames+511)/512)
	peaks := make([]int16, (frames+bin-1)/bin)
	buffer := make([]byte, 65536)
	var position int64
	for {
		n, err := io.ReadFull(f, buffer)
		if n%FrameBytes != 0 {
			return project.AudioAsset{}, fmt.Errorf("PCM 尾帧不完整")
		}
		hash.Write(buffer[:n])
		for i := 0; i < n; i += FrameBytes {
			a, b := int(int16(binary.LittleEndian.Uint16(buffer[i:]))), int(int16(binary.LittleEndian.Uint16(buffer[i+2:])))
			level := min(32767, max(max(a, -a), max(b, -b)))
			index := position / bin
			peaks[index] = max(peaks[index], int16(level))
			position++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return project.AudioAsset{}, err
		}
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	a := project.AudioAsset{ID: project.NewID("a"), Digest: digest, Name: string([]rune(name)[:min(len([]rune(name)), project.MaxTitleRunes)]), Frames: frames, Peaks: peaks}
	return a, nil
}

func publishStage(data, name, stage string) (project.AudioAsset, error) {
	a, err := describePCM(name, stage)
	if err != nil {
		return a, err
	}
	dest := filepath.Join(Root(data), a.Digest+".pcm")
	if err = os.Link(stage, dest); err != nil {
		if !os.IsExist(err) {
			return project.AudioAsset{}, err
		}
		if err = Verify(data, a); err != nil {
			return project.AudioAsset{}, err
		}
	}
	return a, SyncDir(Root(data))
}

func openStored(data string, asset project.AudioAsset) (*os.File, error) {
	if err := project.ValidateAudioAsset(asset); err != nil {
		return nil, err
	}
	if data == "" {
		return nil, fmt.Errorf("未配置音频数据目录")
	}
	f, err := openAudioSource(filepath.Join(Root(data), asset.Digest+".pcm"))
	if err != nil {
		return nil, fmt.Errorf("无法读取音频资产 %s：%w", asset.Name, err)
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != asset.Frames*FrameBytes {
		f.Close()
		return nil, fmt.Errorf("音频资产 %s 已截断或格式无效", asset.Name)
	}
	return f, nil
}

func Verify(data string, asset project.AudioAsset) error {
	f, err := openStored(data, asset)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != asset.Digest {
		return fmt.Errorf("音频资产 %s 校验失败", asset.Name)
	}
	return nil
}

func Load(data string, asset project.AudioAsset) ([]int16, error) {
	return LoadContext(context.Background(), data, asset)
}
func LoadContext(ctx context.Context, data string, asset project.AudioAsset) ([]int16, error) {
	f, err := OpenContext(ctx, data, asset)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, asset.Frames*FrameBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != asset.Frames*FrameBytes || fmt.Sprintf("%x", sha256.Sum256(raw)) != asset.Digest {
		return nil, fmt.Errorf("音频资产 %s 校验失败", asset.Name)
	}
	return pcmSamples(raw), nil
}

func SyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Publish writes beside the destination. An existing name is never replaced.
func Publish(ctx context.Context, path string, write func(*os.File) error) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".icetudio-output-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = write(f)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Link(f.Name(), path); err != nil {
		return fmt.Errorf("发布成品：%w", err)
	}
	return SyncDir(dir)
}

func Format(path string) string { return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) }
