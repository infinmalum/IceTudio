package audioasset

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"icetudio/internal/project"
)

const maxBundlePCM = 512 << 20

type bundleManifest struct {
	Version int           `json:"version"`
	Audio   string        `json:"audio"`
	State   project.State `json:"project"`
}

func bundleAssets(st *project.State) (map[string]project.AudioAsset, error) {
	var bytes int64
	out := map[string]project.AudioAsset{}
	for _, a := range st.AudioAssets {
		if err := project.ValidateAudioAsset(a); err != nil {
			return nil, err
		}
		if p, ok := out[a.Digest]; ok {
			if p.Frames != a.Frames {
				return nil, fmt.Errorf("相同哈希的音频帧数不同")
			}
			continue
		}
		bytes += a.Frames * FrameBytes
		if bytes > maxBundlePCM {
			return nil, fmt.Errorf("资源包音频超过 512 MiB")
		}
		out[a.Digest] = a
	}
	return out, nil
}
func ExportBundle(ctx context.Context, data, path string, st project.State) error {
	if err := project.ValidatePortable(&st); err != nil {
		return err
	}
	assets, err := bundleAssets(&st)
	if err != nil {
		return err
	}
	return Publish(ctx, path, func(dest *os.File) error {
		w := zip.NewWriter(dest)
		manifest, err := w.Create("manifest.json")
		if err != nil {
			return err
		}
		if err = json.NewEncoder(manifest).Encode(bundleManifest{1, "s16le/48000/stereo", st}); err != nil {
			return err
		}
		for hash, a := range assets {
			if err = ctx.Err(); err != nil {
				return err
			}
			f, e := OpenContext(ctx, data, a)
			if e != nil {
				return e
			}
			entry, e := w.Create("pcm/" + hash + ".pcm")
			if e != nil {
				f.Close()
				return e
			}
			digest := sha256.New()
			n, e := io.Copy(io.MultiWriter(entry, digest), io.LimitReader(f, a.Frames*FrameBytes+1))
			f.Close()
			if e != nil {
				return e
			}
			if n != a.Frames*FrameBytes || fmt.Sprintf("%x", digest.Sum(nil)) != hash {
				return fmt.Errorf("资源包音频校验失败")
			}
		}
		return w.Close()
	})
}
func ImportBundle(ctx context.Context, data, path string) (project.State, error) {
	fail := func(err error) (project.State, error) { return project.State{}, err }
	z, err := zip.OpenReader(path)
	if err != nil {
		return fail(err)
	}
	defer z.Close()
	if len(z.File) > project.MaxAudioAssets+1 {
		return fail(fmt.Errorf("资源包条目过多"))
	}
	files := map[string]*zip.File{}
	for _, entry := range z.File {
		if files[entry.Name] != nil || !entry.Mode().IsRegular() {
			return fail(fmt.Errorf("资源包含重复或特殊条目"))
		}
		files[entry.Name] = entry
	}
	entry := files["manifest.json"]
	if entry == nil || entry.UncompressedSize64 > project.MaxStateJSONBytes {
		return fail(fmt.Errorf("资源包清单不存在或过大"))
	}
	r, err := entry.Open()
	if err != nil {
		return fail(err)
	}
	d := json.NewDecoder(io.LimitReader(r, project.MaxStateJSONBytes+1))
	d.DisallowUnknownFields()
	var m bundleManifest
	err = d.Decode(&m)
	if err == nil && d.Decode(new(any)) != io.EOF {
		err = fmt.Errorf("资源包清单包含多余内容")
	}
	r.Close()
	if err != nil {
		return fail(err)
	}
	if m.Version != 1 || m.Audio != "s16le/48000/stereo" {
		return fail(fmt.Errorf("资源包版本不受支持"))
	}
	if err = project.ValidatePortable(&m.State); err != nil {
		return fail(err)
	}
	assets, err := bundleAssets(&m.State)
	if err != nil {
		return fail(err)
	}
	if len(files) != len(assets)+1 {
		return fail(fmt.Errorf("资源包含未声明条目"))
	}
	for hash, a := range assets {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		entry = files["pcm/"+hash+".pcm"]
		if entry == nil || entry.UncompressedSize64 != uint64(a.Frames*FrameBytes) {
			return fail(fmt.Errorf("资源包 PCM 缺失或帧数不符"))
		}
		src, e := entry.Open()
		if e != nil {
			return fail(e)
		}
		digest := sha256.New()
		stage, e := stagePCM(data, io.TeeReader(src, digest))
		src.Close()
		if e != nil {
			return fail(e)
		}
		if fmt.Sprintf("%x", digest.Sum(nil)) != hash {
			os.Remove(stage)
			return fail(fmt.Errorf("资源包 PCM 哈希不符"))
		}
		_, e = PublishPCM(data, a.Name, stage)
		os.Remove(stage)
		if e != nil {
			return fail(e)
		}
	}
	return m.State, nil
}
