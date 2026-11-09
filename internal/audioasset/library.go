package audioasset

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"icetudio/internal/project"
)

const maxLibraryEntries = 4000

type Material struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Tags         string `json:"tags,omitempty"`
	BPM          int    `json:"bpm,omitempty"`
	Key          string `json:"key,omitempty"`
	EstimatedBPM bool   `json:"estimatedBpm,omitempty"`
	EstimatedKey bool   `json:"estimatedKey,omitempty"`
	Favorite     bool   `json:"favorite,omitempty"`
}

type Library struct {
	dir     string
	mu      sync.Mutex
	preview context.CancelFunc
}

func NewLibrary(dir string) *Library { return &Library{dir: dir} }
func (l *Library) path() string      { return filepath.Join(l.dir, "materials", "index.json") }

func (l *Library) list() ([]Material, error) {
	if l.dir == "" {
		return nil, fmt.Errorf("未配置素材数据目录")
	}
	var out []Material
	if err := readAudioJSON(l.path(), 24<<20, &out); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(out) > maxLibraryEntries {
		return nil, fmt.Errorf("素材索引超过上限")
	}
	ids := map[string]bool{}
	for _, m := range out {
		if !project.ValidID(m.ID) || !filepath.IsAbs(m.Path) || len(m.Path) > 4096 || ids[m.ID] {
			return nil, fmt.Errorf("素材索引条目无效")
		}
		ids[m.ID] = true
	}
	return out, nil
}
func (l *Library) List() ([]Material, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.list()
}
func (l *Library) Get(id string) (Material, error) {
	all, err := l.List()
	if err != nil {
		return Material{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return Material{}, fmt.Errorf("素材已从索引移除")
}

func (l *Library) Scan(ctx context.Context, root string) (int, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("请选择素材目录")
	}
	paths := []string{}
	visited := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e != nil {
			return e
		}
		visited++
		if visited > 100000 {
			return fmt.Errorf("目录超过 10 万条扫描上限，请选用较小的目录")
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if (Format(p) == "wav" || Format(p) == "flac") && len(p) <= 4096 {
			if len(paths) >= maxLibraryEntries {
				return fmt.Errorf("素材目录超过 %d 条上限", maxLibraryEntries)
			}
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	all, err := l.list()
	if err != nil {
		return 0, err
	}
	known := map[string]bool{}
	for _, m := range all {
		known[m.Path] = true
	}
	n := 0
	for _, p := range paths {
		if !known[p] {
			all = append(all, Material{ID: project.NewID("sample"), Path: p})
			n++
		}
	}
	if len(all) > maxLibraryEntries {
		return 0, fmt.Errorf("索引最多保存 %d 条素材", maxLibraryEntries)
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeAudioJSON(l.path(), all)
}

func (l *Library) Update(m Material) error {
	if m.BPM < 0 || m.BPM > 400 || len([]rune(m.Key)) > 32 || len([]rune(m.Tags)) > 200 {
		return fmt.Errorf("BPM 须为 0–400，标签及调性须在长度范围内")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	all, err := l.list()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(all, func(old Material) bool { return old.ID == m.ID })
	if i < 0 || all[i].Path != m.Path {
		return fmt.Errorf("素材来源已变化，请刷新")
	}
	all[i] = m
	return writeAudioJSON(l.path(), all)
}

func (l *Library) Analyze(ctx context.Context, id string) (Analysis, error) {
	m, err := l.Get(id)
	if err != nil {
		return Analysis{}, err
	}
	stage, err := decodePCM(ctx, l.dir, m.Path)
	if err != nil {
		return Analysis{}, err
	}
	defer os.Remove(stage)
	f, err := os.Open(stage)
	if err != nil {
		return Analysis{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, AnalysisFrames*FrameBytes))
	if err != nil {
		return Analysis{}, err
	}
	return Analyze(pcmSamples(raw)), ctx.Err()
}

func (l *Library) Prepare(ctx context.Context, id string, linked bool) (project.AudioAsset, error) {
	m, err := l.Get(id)
	if err != nil {
		return project.AudioAsset{}, err
	}
	if linked {
		return Reference(ctx, l.dir, m.Path, nil)
	}
	return Import(ctx, l.dir, m.Path)
}

func (l *Library) StopPreview() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.preview != nil {
		l.preview()
		l.preview = nil
	}
}

func (l *Library) play(ctx context.Context, volume float64, source func(context.Context) (io.ReadCloser, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	l.mu.Lock()
	if err := ctx.Err(); err != nil {
		l.mu.Unlock()
		return err
	}
	if l.preview != nil {
		l.preview()
	}
	l.preview = cancel
	l.mu.Unlock()
	pcm, err := source(ctx)
	if err != nil {
		return err
	}
	defer pcm.Close()
	if err = ctx.Err(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "pw-play", "--raw", "--rate", "48000", "--channels", "2", "--format", "s16", "--volume", strconv.FormatFloat(max(0, min(1, volume)), 'f', 3, 64), "-")
	cmd.Stdin = io.LimitReader(pcm, 20*project.AudioRate*FrameBytes)
	log := &limitedLog{}
	cmd.Stderr = log
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("本地预听失败：%w %s", err, strings.TrimSpace(log.String()))
	}
	return nil
}
func (l *Library) Preview(ctx context.Context, id string, volume float64) error {
	return l.play(ctx, volume, func(ctx context.Context) (io.ReadCloser, error) {
		m, err := l.Get(id)
		if err != nil {
			return nil, err
		}
		stage, err := decodePCM(ctx, l.dir, m.Path)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(stage)
		if err != nil {
			os.Remove(stage)
			return nil, err
		}
		return tempReader{f}, nil
	})
}
func (l *Library) PreviewAsset(ctx context.Context, asset project.AudioAsset, start, frames int64, volume float64) error {
	if start < 0 || frames < 1 || start > asset.Frames || frames > asset.Frames-start {
		return fmt.Errorf("预听源窗口无效")
	}
	return l.play(ctx, volume, func(ctx context.Context) (io.ReadCloser, error) {
		f, err := openVerified(ctx, l.dir, asset)
		if err != nil {
			return nil, err
		}
		if _, err = io.CopyN(io.Discard, f, start*FrameBytes); err != nil {
			f.Close()
			return nil, err
		}
		return previewRange{Reader: io.LimitReader(f, frames*FrameBytes), Closer: f}, nil
	})
}

type previewRange struct {
	io.Reader
	io.Closer
}

func (l *Library) PreviewPCM(ctx context.Context, volume float64, prepare func(context.Context) ([]byte, error)) error {
	return l.play(ctx, volume, func(ctx context.Context) (io.ReadCloser, error) {
		pcm, err := prepare(ctx)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(pcm)), nil
	})
}
