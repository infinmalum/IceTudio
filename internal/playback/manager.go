package playback

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// FontEntry 是用户添加的本地 SoundFont；列表顺序即回退优先级（先者优先）。
type FontEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Priority int    `json:"priority"`
	AddedAt  string `json:"addedAt"`
}
type Settings struct {
	Version     int         `json:"version"` // 2：音色列表 + 默认音色；v1（单一 soundFont 路径）已不支持
	Driver      string      `json:"driver"`
	Fonts       []FontEntry `json:"fonts"`
	DefaultFont string      `json:"defaultFont"` // 音色 ID；空 = 打包默认音色（缺失时自动回退）
}
// FontState 是单个音色来源的当前状态；文件级检查不打开引擎。
type FontState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Source   string `json:"source"` // user | bundled | basic
	Exists   bool   `json:"exists"`
	Size     int64  `json:"size"`
	Error    string `json:"error"`
	Active   bool   `json:"active"`
	IsDefault bool  `json:"isDefault"`
	Priority int    `json:"priority"`
}
type FontList struct {
	Bundled     FontState   `json:"bundled"`
	Basic       FontState   `json:"basic"`
	Fonts       []FontState `json:"fonts"`
	DefaultFont string      `json:"defaultFont"`
	Notice      string      `json:"notice"`
}
type Request struct {
	ProjectID string `json:"projectId"`
	Revision  int64  `json:"revision"`
	Tick      int64  `json:"tick"`
	Loop      bool   `json:"loop"`
	LoopStart int64  `json:"loopStart"`
	LoopEnd   int64  `json:"loopEnd"`
}
type Status struct {
	Available     bool     `json:"available"`
	Ready         bool     `json:"ready"`
	Playing       bool     `json:"playing"`
	ProjectID     string   `json:"projectId"`
	Revision      int64    `json:"revision"`
	Tick          float64  `json:"tick"`
	EndTick       int64    `json:"endTick"`
	Loop          bool     `json:"loop"`
	LoopStart     int64    `json:"loopStart"`
	LoopEnd       int64    `json:"loopEnd"`
	SampleRate    int      `json:"sampleRate"`
	Callbacks     int64    `json:"callbacks"`
	MissedBlocks  int64    `json:"missedBlocks"`
	Loops         int64    `json:"loops"`
	SkippedEvents int      `json:"skippedEvents"`
	Engine        string   `json:"engine"`
	Settings      Settings `json:"settings"`
	ResolvedFont  string   `json:"resolvedFont"` // 引擎实际加载的音色名
	ResolvedKind  string   `json:"resolvedKind"` // user | bundled | basic
	Notice        string   `json:"notice"`       // 回退/告警（非错误）
	Error         string   `json:"error"`
}
type Getter func(string) (Snapshot, error)
type Manager struct {
	mu            sync.Mutex
	control       sync.Mutex // serialize user controls, not audio callbacks
	dir           string
	open          func(string, string) (*native, error)
	get           Getter
	settings      Settings
	settingsErr   error
	engine        *native
	score         *Score
	request       Request
	targetID      string // most recently requested project, including in-flight Play
	generation    uint64
	closed        bool
	errorText     string
	resolvedFont  string
	resolvedKind  string
	notice        string
	updates       chan struct{}
	done          chan struct{}
	wg            sync.WaitGroup
	lastCallbacks int64
	lastProgress  time.Time
}

// BundledFontName 是随发行物提供的默认 GM 音色（MIT，见 assets/soundfonts 授权档）。
const BundledFontName = "MuseScore_General.sf3"

const settingsVersion = 2

func New(dir string, get Getter) *Manager {
	m := &Manager{dir: dir, get: get, settings: Settings{Version: settingsVersion, Driver: "pulseaudio"}, updates: make(chan struct{}, 1), done: make(chan struct{})}
	if dir != "" {
		data, err := os.ReadFile(filepath.Join(dir, "audio.json"))
		if err == nil {
			var saved Settings
			err = json.Unmarshal(data, &saved)
			if err == nil && saved.Version != settingsVersion {
				err = errors.New("音频配置版本不受支持（需要 v2），原文件已保留；请在音色管理中重新设置")
			}
			if err == nil {
				if err = validateSettings(saved); err == nil {
					m.settings = saved
				}
			}
		}
		if err != nil && !os.IsNotExist(err) {
			m.settingsErr = err
			m.errorText = err.Error()
		}
	}
	m.wg.Add(1)
	go m.worker()
	return m
}

func fontID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("font_%012x", time.Now().UnixNano())
	}
	return "font_" + hex.EncodeToString(b[:])
}

func validateFontPath(path string) (int64, error) {
	if path == "" {
		return 0, errors.New("音色路径为空")
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("音色文件不可用：%w", err)
	}
	if !st.Mode().IsRegular() || st.Size() > 512<<20 || st.Size() < 12 {
		return 0, errors.New("音色须是 12 字节到 512 MiB 的普通 SF2/SF3 文件")
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".sf2" && ext != ".sf3" {
		return 0, errors.New("请选择 SF2 或 SF3 音色文件")
	}
	return st.Size(), nil
}

func validateSettings(s Settings) error {
	if s.Version != settingsVersion {
		return errors.New("音频配置版本不受支持")
	}
	switch s.Driver {
	case "pulseaudio", "pipewire", "alsa":
	default:
		return errors.New("请选择 PulseAudio、PipeWire 或 ALSA 音频驱动")
	}
	seen := map[string]bool{}
	for _, f := range s.Fonts {
		if f.ID == "" || seen[f.ID] {
			return errors.New("音色列表含空 ID 或重复 ID")
		}
		seen[f.ID] = true
		if f.Name == "" || f.Path == "" {
			return errors.New("音色列表含空名称或空路径")
		}
	}
	if s.DefaultFont != "" && !seen[s.DefaultFont] {
		return errors.New("默认音色不在音色列表中")
	}
	return nil
}

// bundledCandidates 按顺序探测发行物默认音色：数据目录、可执行文件旁及其 soundfonts 子目录。
func (m *Manager) bundledCandidates() []string {
	out := []string{}
	if m.dir != "" {
		out = append(out, filepath.Join(m.dir, "audio", BundledFontName))
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		out = append(out, filepath.Join(d, BundledFontName), filepath.Join(d, "soundfonts", BundledFontName))
	}
	return out
}

type resolved struct {
	path  string // "" = Basic Waves
	kind  string // user | bundled | basic
	name  string
	notice string
}

// resolve 按 默认音色 → 打包默认 → 用户列表（优先级）→ Basic Waves 的顺序确定引擎音色。
// 回退不是静默失败：每一步跳过都记入 notice，最终结果经 Status 展示。
func (m *Manager) resolve(s Settings) resolved {
	out := resolved{kind: "basic", name: "Basic Waves（内置基础波形）"}
	if s.DefaultFont != "" {
		for _, f := range s.Fonts {
			if f.ID == s.DefaultFont {
				if _, err := validateFontPath(f.Path); err == nil {
					return resolved{path: f.Path, kind: "user", name: f.Name}
				} else {
					out.notice = fmt.Sprintf("默认音色「%s」不可用（%s），已回退。", f.Name, err.Error())
				}
				break
			}
		}
	}
	for _, path := range m.bundledCandidates() {
		if _, err := validateFontPath(path); err == nil {
			out.path, out.kind, out.name = path, "bundled", BundledFontName
			return out
		}
	}
	ordered := append([]FontEntry(nil), s.Fonts...)
	slices.SortStableFunc(ordered, func(a, b FontEntry) int { return a.Priority - b.Priority })
	for _, f := range ordered {
		if f.ID == s.DefaultFont {
			continue
		}
		if _, err := validateFontPath(f.Path); err == nil {
			out.path, out.kind, out.name = f.Path, "user", f.Name
			if out.notice == "" {
				out.notice = fmt.Sprintf("打包默认音色缺失，已回退到用户音色「%s」。", f.Name)
			}
			return out
		}
	}
	if out.notice == "" && s.DefaultFont == "" {
		out.notice = "未找到打包默认音色与可用用户音色，当前使用 Basic Waves 基础波形；可在音色管理中添加 SF2/SF3。"
	}
	return out
}

func (m *Manager) prepare(settings Settings) (*native, error) {
	if err := validateSettings(settings); err != nil {
		return nil, err
	}
	choice := m.resolve(settings)
	font := choice.path
	if font == "" {
		if m.dir == "" {
			return nil, errors.New("未设置音频数据目录")
		}
		font = filepath.Join(m.dir, "audio", "basic-v1.sf2")
		if err := writeAtomic(font, BasicFont()); err != nil {
			return nil, err
		}
	}
	if m.open != nil {
		return m.open(font, settings.Driver)
	}
	return openNative(font, settings.Driver, false)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".audio-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (m *Manager) persist(settings Settings) error {
	data, _ := json.MarshalIndent(settings, "", "  ")
	return writeAtomic(filepath.Join(m.dir, "audio.json"), data)
}

// reconfigure 用给定设置重建引擎并重置播放；调用方持有 control 锁。
func (m *Manager) reconfigure(settings Settings) error {
	candidate, err := m.prepare(settings)
	if err != nil {
		return err
	}
	if m.dir != "" {
		if err = m.persist(settings); err != nil {
			candidate.close()
			return err
		}
	}
	choice := m.resolve(settings)
	m.mu.Lock()
	old := m.engine
	m.engine = candidate
	m.settings = settings
	m.score = nil
	m.targetID = ""
	m.generation++
	m.errorText = ""
	m.resolvedFont = choice.name
	m.resolvedKind = choice.kind
	m.notice = choice.notice
	m.lastProgress = time.Now()
	m.lastCallbacks = 0
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	return nil
}
func (m *Manager) Configure(settings Settings) error {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	if m.settingsErr != nil {
		err := m.settingsErr
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	return m.reconfigure(settings)
}

// cloneSettings 返回带独立 Fonts 底层数组的副本；持久化成功前不提交 m.settings。
func (m *Manager) cloneSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.settings
	out.Fonts = slices.Clone(m.settings.Fonts)
	return out
}

// ResetSettings 在用户确认后把不支持的 audio.json 改名保留（.unsupported）并回到默认配置。
// 不读取旧格式、不删除文件；这是 v1 等不支持配置的唯一恢复入口。
func (m *Manager) ResetSettings() error {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	if m.settingsErr == nil {
		m.mu.Unlock()
		return errors.New("当前音频配置可用，无需重置")
	}
	m.mu.Unlock()
	if m.dir != "" {
		path := filepath.Join(m.dir, "audio.json")
		if err := os.Rename(path, path+".unsupported"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	m.mu.Lock()
	m.settings = Settings{Version: settingsVersion, Driver: "pulseaudio"}
	m.settingsErr = nil
	m.errorText = ""
	m.mu.Unlock()
	return nil
}

// AuditionRequest 是绕过 timeline 的即时试听（P2 工作区契约 §7）：不产生 Op、
// 不进入工程修订/撤销历史。通道策略（2026-10-24 决策）：不新增第 5 个 synth 组
// （避免重复加载音色的内存），从当前播放快照未占用的全局通道自高向低取一个，
// 与 Play/Seek/Configure 同把 control 锁串行，选取后不存在安装竞争；工程后续
// 编辑占用了该通道时，plan 安装的 clear 会顺带干净地结束试听，不产生 stuck notes。
type AuditionRequest struct {
	Program    int   `json:"program"`
	Bank       int   `json:"bank"` // 0 melodic；128 打击组
	Keys       []int `json:"keys"`
	Velocity   int   `json:"velocity"`
	DurationMs int   `json:"durationMs"`
}

const auditionChannelSpace = 1024 // 4 synth 组 × 256 通道的全局空间

func (m *Manager) Audition(req AuditionRequest) error {
	if req.Program < 0 || req.Program > 127 {
		return errors.New("试听 program 须为 0–127")
	}
	if req.Bank != 0 && req.Bank != 128 {
		return errors.New("试听 bank 须为 0（melodic）或 128（打击组）")
	}
	if len(req.Keys) < 1 || len(req.Keys) > 8 {
		return errors.New("试听须为 1–8 个音")
	}
	for _, k := range req.Keys {
		if k < 0 || k > 127 {
			return errors.New("试听音高须为 0–127")
		}
	}
	if req.Velocity == 0 {
		req.Velocity = 100
	}
	if req.Velocity < 1 || req.Velocity > 127 {
		return errors.New("试听力度须为 1–127")
	}
	if req.DurationMs < 50 || req.DurationMs > 5000 {
		return errors.New("试听时长须为 50–5000 毫秒")
	}
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	used := map[int32]bool{}
	if m.score != nil {
		for _, ch := range m.score.Channels {
			used[ch] = true
		}
	}
	m.mu.Unlock()
	channel := -1
	for ch := 0; ch < auditionChannelSpace; ch++ {
		if !used[int32(ch)] {
			channel = ch
			break
		}
	}
	if channel < 0 {
		return errors.New("工程已占用全部通道，暂无法试听；停止播放后重试")
	}
	if err := m.ensure(); err != nil {
		return err
	}
	m.mu.Lock()
	engine := m.engine
	m.mu.Unlock()
	if engine == nil {
		return errors.New("音频引擎不可用")
	}
	return engine.audition(channel, req.Program, req.Bank, req.Keys, req.Velocity, int64(req.DurationMs)*SampleRate/1000)
}

// AuditionStop 立即结束试听（all-notes-off）；停止、翻页、切工程、关窗时由前端调用。
func (m *Manager) AuditionStop() {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	engine := m.engine
	m.mu.Unlock()
	if engine != nil {
		engine.auditionStop()
	}
}

// SoundFonts 返回全部音色来源的当前状态；文件级检查不打开引擎。
func (m *Manager) SoundFonts() FontList {
	settings := m.cloneSettings()
	m.mu.Lock()
	activeName, activeKind := m.resolvedFont, m.resolvedKind
	m.mu.Unlock()
	state := func(id, name, path, source string, priority int, isDefault bool) FontState {
		f := FontState{ID: id, Name: name, Path: path, Source: source, Priority: priority, IsDefault: isDefault,
			Active: engineActiveFor(source, name, activeName, activeKind)}
		if source == "basic" {
			f.Exists = true
			return f
		}
		size, err := validateFontPath(path)
		if err != nil {
			f.Error = err.Error()
			return f
		}
		f.Exists, f.Size = true, size
		return f
	}
	notice := ""
	bundledPath := ""
	for _, path := range m.bundledCandidates() {
		if _, err := validateFontPath(path); err == nil {
			bundledPath = path
			break
		}
	}
	if bundledPath == "" && settings.DefaultFont == "" {
		notice = "未找到打包默认音色；发行物应内嵌 MuseScore_General.sf3（开发环境运行 scripts/fetch-soundfont.sh）。"
	}
	out := FontList{DefaultFont: settings.DefaultFont, Notice: notice,
		Bundled: state("bundled", BundledFontName, bundledPath, "bundled", -1, settings.DefaultFont == ""),
		Basic:   state("basic", "Basic Waves（内置基础波形）", "", "basic", 1<<30, settings.DefaultFont == "" && bundledPath == "")}
	if bundledPath == "" {
		out.Bundled.Error = "未找到打包默认音色文件（发行物应内嵌；开发环境运行 scripts/fetch-soundfont.sh）"
	}
	for _, f := range settings.Fonts {
		out.Fonts = append(out.Fonts, state(f.ID, f.Name, f.Path, "user", f.Priority, f.ID == settings.DefaultFont))
	}
	slices.SortStableFunc(out.Fonts, func(a, b FontState) int { return a.Priority - b.Priority })
	return out
}

func engineActiveFor(source, name, activeName, activeKind string) bool {
	if activeName == "" {
		return false
	}
	if source == "basic" {
		return activeKind == "basic"
	}
	return activeKind == source && name == activeName
}

// AddFont 登记一个本地 SF2/SF3；不改变默认音色（由 SetDefaultFont 单独切换）。
func (m *Manager) AddFont(path string) (FontEntry, error) {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return FontEntry{}, errors.New("播放服务已关闭")
	}
	if m.settingsErr != nil {
		err := m.settingsErr
		m.mu.Unlock()
		return FontEntry{}, err
	}
	m.mu.Unlock()
	if _, err := validateFontPath(path); err != nil {
		return FontEntry{}, err
	}
	settings := m.cloneSettings()
	for _, f := range settings.Fonts {
		if f.Path == path {
			return FontEntry{}, errors.New("该音色文件已在列表中：" + f.Name)
		}
	}
	priority := 0
	for _, f := range settings.Fonts {
		if f.Priority >= priority {
			priority = f.Priority + 1
		}
	}
	entry := FontEntry{ID: fontID(), Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Path: path,
		Priority: priority, AddedAt: time.Now().Format(time.RFC3339)}
	settings.Fonts = append(settings.Fonts, entry)
	if err := validateSettings(settings); err != nil {
		return FontEntry{}, err
	}
	if m.dir != "" {
		if err := m.persist(settings); err != nil {
			return FontEntry{}, err
		}
	}
	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()
	return entry, nil
}

// RemoveFont 从列表移除用户音色（不删除磁盘文件）；若移除的是默认音色，默认回退到打包音色并在下次初始化音频时生效。
func (m *Manager) RemoveFont(id string) error {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	if m.settingsErr != nil {
		err := m.settingsErr
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	settings := m.cloneSettings()
	kept := settings.Fonts[:0:0]
	removed := false
	for _, f := range settings.Fonts {
		if f.ID == id {
			removed = true
			continue
		}
		kept = append(kept, f)
	}
	if !removed {
		return errors.New("要移除的音色不在列表中（打包音色与 Basic Waves 不可移除）")
	}
	settings.Fonts = kept
	if settings.DefaultFont == id {
		settings.DefaultFont = ""
	}
	if err := validateSettings(settings); err != nil {
		return err
	}
	if m.dir != "" {
		if err := m.persist(settings); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.settings = settings
	if settings.DefaultFont == "" {
		m.notice = "默认音色已移除，将在下次初始化音频时回退到打包默认音色。"
	}
	m.mu.Unlock()
	return nil
}

// SetDefaultFont 切换默认音源并立即重建引擎（播放会按既有纪律停止）；"" 表示打包默认。
func (m *Manager) SetDefaultFont(id string) error {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	if m.settingsErr != nil {
		err := m.settingsErr
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	settings := m.cloneSettings()
	if id != "" {
		found := false
		for _, f := range settings.Fonts {
			if f.ID == id {
				found = true
				// 用户显式切换默认：目标不可用立即报错并保留在用引擎；
				// 自动回退（含提示）只发生在启动时的延迟失效场景。
				if _, err := validateFontPath(f.Path); err != nil {
					return fmt.Errorf("无法切换默认音色「%s」：%w", f.Name, err)
				}
				break
			}
		}
		if !found {
			return errors.New("要设为默认的音色不在列表中")
		}
	}
	settings.DefaultFont = id
	if err := validateSettings(settings); err != nil {
		return err
	}
	return m.reconfigure(settings)
}

// MoveFont 调整用户音色回退优先级；delta 为负提升、为正下降（UI 传 ±1）。
func (m *Manager) MoveFont(id string, delta int) error {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	if m.settingsErr != nil {
		err := m.settingsErr
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	settings := m.cloneSettings()
	slices.SortStableFunc(settings.Fonts, func(a, b FontEntry) int { return a.Priority - b.Priority })
	index := slices.IndexFunc(settings.Fonts, func(f FontEntry) bool { return f.ID == id })
	if index < 0 {
		return errors.New("要调整优先级的音色不在列表中")
	}
	next := min(max(index+delta, 0), len(settings.Fonts)-1)
	if next == index {
		return nil
	}
	entry := settings.Fonts[index]
	settings.Fonts = slices.Delete(settings.Fonts, index, index+1)
	settings.Fonts = slices.Insert(settings.Fonts, next, entry)
	for i := range settings.Fonts {
		settings.Fonts[i].Priority = i
	}
	if err := validateSettings(settings); err != nil {
		return err
	}
	if m.dir != "" {
		if err := m.persist(settings); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()
	return nil
}
func (m *Manager) ensure() error {
	m.mu.Lock()
	ready := m.engine != nil
	settings := m.settings
	err := m.settingsErr
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if ready {
		return nil
	}
	candidate, err := m.prepare(settings)
	if err != nil {
		return err
	}
	choice := m.resolve(settings)
	m.mu.Lock()
	m.engine = candidate
	m.resolvedFont = choice.name
	m.resolvedKind = choice.kind
	if m.notice == "" {
		m.notice = choice.notice
	}
	m.lastProgress = time.Now()
	m.mu.Unlock()
	return nil
}
func (m *Manager) Play(req Request) error {
	// Requery after installing: a commit during the first read/compile may
	// have sent its notification before there was a current playback score.
	// Rejected requests must also preserve coalesced updates for the old score.
	defer func() {
		m.mu.Lock()
		id := ""
		if m.score != nil {
			id = m.score.ID
		}
		m.mu.Unlock()
		if id != "" {
			m.Changed(id)
		}
	}()

	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("播放服务已关闭")
	}
	m.generation++
	generation := m.generation
	m.targetID = req.ProjectID
	m.mu.Unlock()
	if req.ProjectID == "" || req.Tick < 0 || (req.Loop && (req.LoopStart < 0 || req.LoopEnd <= req.LoopStart)) {
		return errors.New("播放位置或循环范围无效")
	}
	snapshot, err := m.get(req.ProjectID)
	if err != nil {
		return err
	}
	if snapshot.Revision != req.Revision {
		return errors.New("工程已修改，请刷新后再播放")
	}
	score, err := Compile(snapshot)
	if err != nil {
		return err
	}
	if err = score.ValidateRange(req.Tick, req.LoopStart, req.LoopEnd, req.Loop); err != nil {
		return err
	}
	if req.Loop && req.Tick >= req.LoopEnd {
		req.Tick = req.LoopStart
	}
	if err = m.ensure(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || generation != m.generation {
		return errors.New("播放请求已取消")
	}
	if err = m.engine.install(score, req.Tick, req.LoopStart, req.LoopEnd, req.Loop, true); err != nil {
		return err
	}
	m.score = score
	m.request = req
	m.errorText = ""
	return nil
}
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	if m.engine != nil {
		m.engine.stop()
	}
}

// StopProject is used by an editor being unmounted or completing late work.
// It must not stop a newer request belonging to another music project.
func (m *Manager) StopProject(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || m.targetID != id {
		return
	}
	m.generation++
	if m.engine != nil {
		m.engine.stop()
	}
}

// Seek changes position and loop configuration without starting stopped audio.
func (m *Manager) Seek(req Request) error {
	m.control.Lock()
	defer m.control.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.engine == nil || m.score == nil || m.score.ID != req.ProjectID {
		return errors.New("请先播放当前工程，再调整播放位置")
	}
	if req.Revision != m.score.Revision {
		return errors.New("播放快照已更新，请重试定位")
	}
	if err := m.score.ValidateRange(req.Tick, req.LoopStart, req.LoopEnd, req.Loop); err != nil {
		return err
	}
	if req.Loop && req.Tick >= req.LoopEnd {
		req.Tick = req.LoopStart
	}
	playing := m.engine.status().Playing
	if err := m.engine.install(m.score, req.Tick, req.LoopStart, req.LoopEnd, req.Loop, playing); err != nil {
		return err
	}
	m.request = req
	m.generation++
	m.targetID = req.ProjectID
	m.errorText = ""
	return nil
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Status{Available: nativeAvailable(), Engine: "FluidSynth " + nativeVersion(), Settings: m.settings, SampleRate: SampleRate, Error: m.errorText, Ready: m.engine != nil,
		ResolvedFont: m.resolvedFont, ResolvedKind: m.resolvedKind, Notice: m.notice}
	if !out.Available && out.Error == "" {
		out.Error = "当前构建未启用 Linux/cgo FluidSynth 播放；仍可编辑和导出 MIDI"
	}
	if m.engine == nil {
		return out
	}
	s := m.engine.status()
	out.Playing = s.Playing
	out.Callbacks = s.Callbacks
	out.MissedBlocks = s.Misses
	out.Loops = s.Loops
	if s.Callbacks != m.lastCallbacks {
		m.lastCallbacks = s.Callbacks
		m.lastProgress = time.Now()
	} else if s.Playing && time.Since(m.lastProgress) > 2*time.Second {
		out.Error = "音频设备超过 2 秒没有回调，请停止并重新初始化音频"
	}
	if m.score != nil {
		out.ProjectID = m.score.ID
		out.Revision = m.score.Revision
		out.Tick = math.Min(float64(m.score.EndTick), m.score.Timeline.Tick(s.Frame))
		out.EndTick = m.score.EndTick
		out.SkippedEvents = m.score.Skipped
		out.Loop = m.request.Loop
		out.LoopStart = m.request.LoopStart
		out.LoopEnd = m.request.LoopEnd
	}
	return out
}
func (m *Manager) Changed(id string) {
	m.mu.Lock()
	relevant := !m.closed && m.score != nil && m.score.ID == id
	m.mu.Unlock()
	if !relevant {
		return
	}
	select {
	case m.updates <- struct{}{}:
	default:
	} // one pending hint suffices: worker reads latest state
}
func (m *Manager) Deleted(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.targetID == id {
		m.generation++
		m.targetID = ""
	}
	if m.score != nil && m.score.ID == id {
		m.engine.stop()
		m.score = nil
	}
}
func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.done:
			return
		case <-m.updates:
			m.mu.Lock()
			id := ""
			if m.score != nil {
				id = m.score.ID
			}
			m.mu.Unlock()
			if id != "" {
				m.refresh(id)
			}
		}
	}
}
func (m *Manager) refresh(id string) {
	m.mu.Lock()
	if m.closed || m.score == nil || m.score.ID != id {
		m.mu.Unlock()
		return
	}
	generation := m.generation
	m.mu.Unlock()
	snapshot, err := m.get(id)
	var score *Score
	if err == nil {
		score, err = Compile(snapshot)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || generation != m.generation || m.score == nil || m.score.ID != id {
		return
	}
	if err != nil {
		m.errorText = "新修订未应用于播放：" + err.Error()
		return
	}
	if score.Revision <= m.score.Revision {
		return
	}
	status := m.engine.status()
	tick := int64(math.Round(m.score.Timeline.Tick(status.Frame)))
	tick = min(max(tick, 0), score.EndTick)
	req := m.request
	req.Tick = tick
	req.Revision = score.Revision
	if req.Loop && (req.LoopEnd > score.EndTick || req.LoopStart >= score.EndTick) {
		req.Loop = false
	}
	if req.Loop && tick >= req.LoopEnd {
		req.Tick = req.LoopStart
	}
	if err = m.engine.install(score, req.Tick, req.LoopStart, req.LoopEnd, req.Loop, status.Playing); err != nil {
		m.errorText = "播放快照更新失败：" + err.Error()
		return
	}
	m.score = score
	m.request = req
	m.errorText = ""
}
func (m *Manager) Close() {
	m.control.Lock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.control.Unlock()
		return
	}
	m.closed = true
	m.generation++
	close(m.done)
	engine := m.engine
	m.engine = nil
	m.mu.Unlock()
	m.control.Unlock()
	m.wg.Wait()
	if engine != nil {
		engine.close()
	}
}
