package playback

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	Version   int    `json:"version"`
	SoundFont string `json:"soundFont"`
	Driver    string `json:"driver"`
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
	updates       chan struct{}
	done          chan struct{}
	wg            sync.WaitGroup
	lastCallbacks int64
	lastProgress  time.Time
}

func New(dir string, get Getter) *Manager {
	m := &Manager{dir: dir, get: get, settings: Settings{Version: 1, Driver: "pulseaudio"}, updates: make(chan struct{}, 1), done: make(chan struct{})}
	if dir != "" {
		data, err := os.ReadFile(filepath.Join(dir, "audio.json"))
		if err == nil {
			var saved Settings
			err = json.Unmarshal(data, &saved)
			if err == nil {
				m.settings = saved
			}
			if err == nil && m.settings.Version != 1 {
				err = errors.New("音频配置版本不受支持，原文件已保留")
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
func (m *Manager) prepare(settings Settings) (*native, error) {
	if settings.Version != 1 {
		return nil, errors.New("音频配置版本不受支持")
	}
	switch settings.Driver {
	case "pulseaudio", "pipewire", "alsa":
	default:
		return nil, errors.New("请选择 PulseAudio、PipeWire 或 ALSA 音频驱动")
	}
	font := settings.SoundFont
	if font == "" {
		if m.dir == "" {
			return nil, errors.New("未设置音频数据目录")
		}
		font = filepath.Join(m.dir, "audio", "basic-v1.sf2")
		if err := writeAtomic(font, BasicFont()); err != nil {
			return nil, err
		}
	} else {
		st, err := os.Stat(font)
		if err != nil {
			return nil, fmt.Errorf("音色文件不可用：%w", err)
		}
		if !st.Mode().IsRegular() || st.Size() > 512<<20 || st.Size() < 12 {
			return nil, errors.New("音色须是 12 字节到 512 MiB 的普通 SF2/SF3 文件")
		}
		ext := strings.ToLower(filepath.Ext(font))
		if ext != ".sf2" && ext != ".sf3" {
			return nil, errors.New("请选择 SF2 或 SF3 音色文件")
		}
	}
	if m.open != nil {
		return m.open(font, settings.Driver)
	}
	return openNative(font, settings.Driver, false)
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
	candidate, err := m.prepare(settings)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err = writeAtomic(filepath.Join(m.dir, "audio.json"), data); err != nil {
		candidate.close()
		return err
	}
	m.mu.Lock()
	old := m.engine
	m.engine = candidate
	m.settings = settings
	m.score = nil
	m.targetID = ""
	m.generation++
	m.errorText = ""
	m.lastProgress = time.Now()
	m.lastCallbacks = 0
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
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
	m.mu.Lock()
	m.engine = candidate
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
	out := Status{Available: nativeAvailable(), Engine: "FluidSynth " + nativeVersion(), Settings: m.settings, SampleRate: SampleRate, Error: m.errorText, Ready: m.engine != nil}
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
