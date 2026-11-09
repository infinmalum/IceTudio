package audioasset

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"icetudio/internal/project"
)

type reference struct {
	Path string `json:"path"`
}

func refFile(data, digest string) string { return filepath.Join(Root(data), digest+".ref.json") }

// References are local to this data directory. The project and bundle carry
// only canonical content metadata and never authorize a machine path.
func Reference(ctx context.Context, data, path string, want *project.AudioAsset) (project.AudioAsset, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return project.AudioAsset{}, err
	}
	stage, err := decodePCM(ctx, data, path)
	if err != nil {
		return project.AudioAsset{}, err
	}
	defer os.Remove(stage)
	asset, err := describePCM(filepath.Base(path), stage)
	if err != nil {
		return asset, err
	}
	if want != nil && (asset.Digest != want.Digest || asset.Frames != want.Frames) {
		return asset, fmt.Errorf("所选文件的音频内容与原引用不同")
	}
	if err = ctx.Err(); err != nil {
		return asset, err
	}
	if err = writeAudioJSON(refFile(data, asset.Digest), reference{Path: path}); err != nil {
		return asset, err
	}
	return asset, nil
}

type tempReader struct{ *os.File }

func (t tempReader) Close() error {
	return errors.Join(t.File.Close(), os.Remove(t.Name()))
}

func OpenContext(ctx context.Context, data string, asset project.AudioAsset) (io.ReadCloser, error) {
	f, err := openStored(data, asset)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var ref reference
	if e := readAudioJSON(refFile(data, asset.Digest), 8192, &ref); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("无法读取素材引用 %s：%w", asset.Name, e)
	}
	if !filepath.IsAbs(ref.Path) {
		return nil, fmt.Errorf("无效的本地素材引用")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stage, err := decodePCM(ctx, data, ref.Path)
	if err != nil {
		return nil, fmt.Errorf("无法读取引用 %s，请在素材库中定位原文件：%w", asset.Name, err)
	}
	actual, err := describePCM(asset.Name, stage)
	if err != nil || actual.Digest != asset.Digest || actual.Frames != asset.Frames {
		os.Remove(stage)
		return nil, fmt.Errorf("引用 %s 内容已变化，请定位原文件", asset.Name)
	}
	f, err = os.Open(stage)
	if err != nil {
		os.Remove(stage)
		return nil, err
	}
	return tempReader{f}, nil
}

func writeAudioJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".audio-json-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(v); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return SyncDir(dir)
}

func readAudioJSON(path string, limit int64, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() > limit {
		return fmt.Errorf("素材索引或引用格式无效")
	}
	d := json.NewDecoder(io.LimitReader(f, limit+1))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("素材数据有多余内容")
	}
	return nil
}

func openVerified(ctx context.Context, data string, asset project.AudioAsset) (io.ReadCloser, error) {
	f, err := OpenContext(ctx, data, asset)
	if err != nil {
		return nil, err
	}
	if _, ok := f.(tempReader); ok {
		return f, nil
	} // decoded reference already hashed
	file := f.(*os.File)
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(file, asset.Frames*FrameBytes+1)); err == nil {
		if fmt.Sprintf("%x", h.Sum(nil)) != asset.Digest {
			err = fmt.Errorf("音频资产 %s 校验失败", asset.Name)
		}
	}
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
