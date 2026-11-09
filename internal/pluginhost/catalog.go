// Package pluginhost owns the local plugin catalog, separate from project state.
package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	Ready       = "ready"
	Failed      = "failed"
	Interrupted = "interrupted" // persisted before invoking plugin code

	maxCatalog = 8 << 20
	maxModules = 1024
	maxBatch   = 128
	maxBinary  = 512 << 20
	maxClasses = 64
	protocol   = "ICE_PLUGIN_SCAN_1"
)

type Class struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Vendor  string `json:"vendor,omitempty"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind"`
}

type Module struct {
	Path    string  `json:"path"`
	Format  string  `json:"format"`
	SHA256  string  `json:"sha256"`
	Status  string  `json:"status"`
	Detail  string  `json:"detail,omitempty"`
	Classes []Class `json:"classes,omitempty"`
}

type Catalog struct {
	dir string
	mu  sync.Mutex
}

type index struct {
	Version int      `json:"version"`
	Modules []Module `json:"modules"`
}

func New(dataDir string) *Catalog { return &Catalog{dir: dataDir} }

func (c *Catalog) path() string { return filepath.Join(c.dir, "audio-plugins.json") }

func (c *Catalog) List() ([]Module, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read()
}

func (c *Catalog) Scan(ctx context.Context, folder string, progress func(int, int)) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("此平台暂未提供 CLAP/VST3 隔离扫描")
	}
	root, err := canonicalFolder(folder)
	if err != nil {
		return err
	}
	paths, err := discover(ctx, root)
	if err != nil {
		return err
	}
	return c.scan(ctx, paths, false, progress, prepareScanner)
}

func (c *Catalog) Retry(ctx context.Context, path string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("此平台暂未提供 CLAP/VST3 隔离扫描")
	}
	return c.scan(ctx, []string{path}, true, nil, prepareScanner)
}

func (c *Catalog) scan(ctx context.Context, paths []string, retry bool, progress func(int, int),
	setup func(context.Context) (scanModule, error)) error {
	if !c.mu.TryLock() {
		return fmt.Errorf("已有插件扫描正在进行")
	}
	defer c.mu.Unlock()
	items, err := c.read()
	if err != nil {
		return err
	}
	byPath := make(map[string]Module, len(items))
	for _, item := range items {
		byPath[item.Path] = item
	}
	if retry {
		if _, exists := byPath[paths[0]]; !exists {
			return fmt.Errorf("只能重试已登记的插件")
		}
	}
	var run scanModule
	for i, path := range paths {
		if _, ok := byPath[path]; !ok && len(byPath) >= maxModules {
			return fmt.Errorf("插件索引已达到 %d 个模块的上限", maxModules)
		}
		if progress != nil {
			progress(i, len(paths))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		old, exists := byPath[path]
		fingerprint, err := hashBinary(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// An incomplete bundle has no scannable binary. The path can
			// be tried again after its files are repaired.
			byPath[path] = Module{Path: path, Format: pluginFormat(path), Status: Failed, Detail: clean(err.Error())}
			if err := c.save(byPath); err != nil {
				return err
			}
			continue
		}
		if !retry && exists && old.SHA256 == fingerprint {
			continue
		}
		if run == nil {
			run, err = setup(ctx)
			if err != nil {
				return err
			}
		}

		module := Module{Path: path, Format: pluginFormat(path), SHA256: fingerprint, Status: Interrupted}
		byPath[path] = module
		if err := c.save(byPath); err != nil {
			return err
		}

		classes, problem, runErr := run(ctx, module)
		var current string
		var hashErr error
		if runErr == nil && ctx.Err() == nil {
			current, hashErr = hashBinary(ctx, path)
		}
		if runErr != nil || ctx.Err() != nil {
			if exists {
				byPath[path] = old
			} else {
				delete(byPath, path)
			}
			if err := c.save(byPath); err != nil {
				return errors.Join(runErr, ctx.Err(), err)
			}
			return errors.Join(runErr, ctx.Err())
		}
		if hashErr != nil || current != fingerprint {
			classes, problem = nil, "扫描期间插件文件发生变化，请重新扫描"
		}
		if problem == "" && !validClasses(classes) {
			problem = "插件返回了无效或不受支持的扫描结果"
		}
		if problem == "" {
			module.Status, module.Classes = Ready, classes
		} else {
			module.Status, module.Detail = Failed, clean(problem)
		}
		byPath[path] = module
		if err := c.save(byPath); err != nil {
			return err
		}
	}
	if progress != nil {
		progress(len(paths), len(paths))
	}
	return nil
}

// A worker returns a per-module diagnostic separately from infrastructure
// errors, which must never put all plugins on the failure list.
type scanModule func(context.Context, Module) ([]Class, string, error)

func canonicalFolder(folder string) (string, error) {
	if folder == "" {
		return "", fmt.Errorf("请选择插件目录")
	}
	folder, err := filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	folder, err = filepath.EvalSymlinks(folder)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(folder)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("请选择插件目录")
	}
	return folder, nil
}

func pluginFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".clap":
		return "clap"
	case ".vst3":
		return "vst3"
	}
	return ""
}

func discover(ctx context.Context, root string) ([]string, error) {
	var paths []string
	visited := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 12000 {
			return fmt.Errorf("目录过大，请选择更小的插件目录")
		}
		format := pluginFormat(path)
		if !(format == "vst3" && d.IsDir() || format == "clap" && d.Type().IsRegular()) {
			return nil
		}
		if len(paths) == maxBatch {
			return fmt.Errorf("一次最多扫描 %d 个模块", maxBatch)
		}
		if !validPath(path) {
			return fmt.Errorf("插件路径无效")
		}
		paths = append(paths, path)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err == nil && len(paths) == 0 {
		err = fmt.Errorf("所选目录中没有可扫描的 CLAP 或 VST3")
	}
	return paths, err
}

func hashBinary(ctx context.Context, module string) (string, error) {
	if !validPath(module) {
		return "", fmt.Errorf("插件路径无效")
	}
	real, err := filepath.EvalSymlinks(module)
	if err != nil {
		return "", err
	}
	if real != module {
		return "", fmt.Errorf("插件路径已变更为符号链接")
	}
	binary := module
	if pluginFormat(module) == "vst3" {
		arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
		if arch == "" {
			return "", fmt.Errorf("此架构尚不支持扫描 VST3")
		}
		name := strings.TrimSuffix(filepath.Base(module), filepath.Ext(module))
		binary = filepath.Join(module, "Contents", arch+"-linux", name+".so")
		if real, err := filepath.EvalSymlinks(binary); err != nil || real != binary {
			return "", fmt.Errorf("VST3 包缺少当前架构的常规模块")
		}
	}
	f, err := openRegular(binary)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, maxBinary+1)); err != nil {
		return "", err
	} else if n == 0 || n > maxBinary {
		return "", fmt.Errorf("插件文件大小超出限制")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type contextReader struct {
	ctx context.Context
	in  io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(b)
}

func validText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func clean(text string) string {
	text = strings.ToValidUTF8(text, "�")
	var result strings.Builder
	for _, r := range text {
		if result.Len()+utf8.RuneLen(r) > 512 {
			break
		}
		if !unicode.IsControl(r) {
			result.WriteRune(r)
		}
	}
	if result.Len() == 0 {
		return "插件扫描失败"
	}
	return result.String()
}

func validPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && pluginFormat(path) != "" && validText(path, 4096)
}

func validClasses(classes []Class) bool {
	if len(classes) == 0 || len(classes) > maxClasses {
		return false
	}
	seen := make(map[string]bool, len(classes))
	for _, p := range classes {
		if p.ID == "" || p.Name == "" || seen[p.ID] ||
			!validText(p.ID, 256) || !validText(p.Name, 256) || !validText(p.Vendor, 256) || !validText(p.Version, 256) {
			return false
		}
		if p.Kind != "instrument" && p.Kind != "effect" && p.Kind != "other" {
			return false
		}
		seen[p.ID] = true
	}
	return true
}

func (c *Catalog) read() ([]Module, error) {
	if c.dir == "" {
		return nil, fmt.Errorf("未配置插件索引目录")
	}
	f, err := openRegular(c.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCatalog+1))
	if err != nil {
		return nil, err
	}
	var idx index
	if len(data) > maxCatalog || json.Unmarshal(data, &idx) != nil || idx.Version != 1 || len(idx.Modules) > maxModules {
		return nil, fmt.Errorf("插件索引无效或版本不受支持")
	}
	seen := map[string]bool{}
	for _, m := range idx.Modules {
		digest, err := hex.DecodeString(m.SHA256)
		validHash := err == nil && len(digest) == sha256.Size && strings.ToLower(m.SHA256) == m.SHA256
		ok := m.Status == Ready && validHash && validClasses(m.Classes) && m.Detail == "" ||
			m.Status == Failed && (validHash || m.SHA256 == "") && len(m.Classes) == 0 && m.Detail != "" ||
			m.Status == Interrupted && validHash && len(m.Classes) == 0 && m.Detail == ""
		if !ok || !validPath(m.Path) || m.Format != pluginFormat(m.Path) || seen[m.Path] || !validText(m.Detail, 512) {
			return nil, fmt.Errorf("插件索引条目无效")
		}
		seen[m.Path] = true
	}
	return idx.Modules, nil
}

func (c *Catalog) save(items map[string]Module) error {
	modules := make([]Module, 0, len(items))
	for _, module := range items {
		modules = append(modules, module)
	}
	slices.SortFunc(modules, func(a, b Module) int { return strings.Compare(a.Path, b.Path) })
	data, err := json.Marshal(index{1, modules})
	if err != nil {
		return err
	}
	if len(data) > maxCatalog {
		return fmt.Errorf("插件索引超过存储上限")
	}
	f, err := os.CreateTemp(c.dir, ".audio-plugins-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, bytes.NewReader(data))
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), c.path()); err != nil {
		return err
	}
	dir, err := os.Open(c.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
