package playback

import (
	"errors"
	"icetudio/internal/project"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func offlineManager(t *testing.T, get Getter) *Manager {
	t.Helper()
	if !nativeAvailable() {
		t.Skip("native rendering unavailable")
	}
	m := New(t.TempDir(), get)
	m.open = func(font, driver string) (*native, error) { return openNative(font, driver, true) }
	t.Cleanup(m.Close)
	return m
}
func waitStatus(t *testing.T, m *Manager, check func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := m.Status()
		if check(s) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status not reached: %+v", m.Status())
	return Status{}
}
func managerSnapshot() Snapshot {
	st, _ := project.Blank("test", 480)
	st.Tracks = []project.Track{{ID: "t_a", Clips: []project.Clip{{ID: "c_a", Length: 1920, Notes: []project.Note{{ID: "n_a", Duration: 1440, Pitch: 69, Velocity: 100}}}}}}
	return Snapshot{ID: "p_a", Revision: 1, State: st}
}
func TestManagerRefreshPreservesTickAndStop(t *testing.T) {
	var mu sync.Mutex
	snap := managerSnapshot()
	m := offlineManager(t, func(id string) (Snapshot, error) { mu.Lock(); defer mu.Unlock(); return snap, nil })
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.engine.render(12000)
	m.mu.Unlock()
	if tick := m.Status().Tick; tick != 240 {
		t.Fatalf("tick %g", tick)
	}
	mu.Lock()
	snap.Revision = 2
	snap.State.Tempo = []project.TempoPoint{{Tick: 0, USPerQuarter: 250000}}
	mu.Unlock()
	m.Changed("p_a")
	s := waitStatus(t, m, func(s Status) bool { return s.Revision == 2 })
	if s.Tick != 240 || !s.Playing {
		t.Fatalf("refresh changed musical position: %+v", s)
	}
	m.Stop()
	m.mu.Lock()
	l, _ := m.engine.render(1024)
	m.mu.Unlock()
	if energy(l) != 0 {
		t.Fatal("stop not silent")
	}
	mu.Lock()
	snap.Revision = 3
	mu.Unlock()
	m.Changed("p_a")
	s = waitStatus(t, m, func(s Status) bool { return s.Revision == 3 })
	if s.Playing {
		t.Fatal("stopped update restarted playback")
	}
}
func TestPendingCompileCannotUndoStop(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	calls := 0
	m := offlineManager(t, func(id string) (Snapshot, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		s := managerSnapshot()
		if call == 2 {
			close(entered)
			<-release
		}
		if call > 1 {
			s.Revision = 2
		}
		return s, nil
	})
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	m.Changed("p_a")
	<-entered
	m.Stop()
	close(release)
	time.Sleep(30 * time.Millisecond)
	if s := m.Status(); s.Playing {
		t.Fatalf("stale compilation restarted playback after stop: %+v", s)
	}
}

func TestCommitDuringInitialPlayIsNotLost(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	m := offlineManager(t, func(string) (Snapshot, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		s := managerSnapshot()
		if calls > 1 {
			s.Revision = 2
		}
		return s, nil
	})
	// The first snapshot precedes a commit; no score existed to receive its hint.
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, func(s Status) bool { return s.Revision == 2 && s.Playing })
}

func TestUnmountedProjectCannotStopNewPlayback(t *testing.T) {
	m := offlineManager(t, func(id string) (Snapshot, error) {
		s := managerSnapshot()
		s.ID = id
		return s, nil
	})
	for _, id := range []string{"p_old", "p_new"} {
		if err := m.Play(Request{ProjectID: id, Revision: 1}); err != nil {
			t.Fatal(err)
		}
	}
	m.StopProject("p_old")
	if s := m.Status(); !s.Playing || s.ProjectID != "p_new" {
		t.Fatalf("old editor stopped new audio: %+v", s)
	}
	m.StopProject("p_new")
	if m.Status().Playing {
		t.Fatal("current editor did not stop its audio")
	}
}
func TestInvalidRefreshAndFontRetainWorkingState(t *testing.T) {
	var mu sync.Mutex
	invalid := false
	m := offlineManager(t, func(id string) (Snapshot, error) {
		mu.Lock()
		defer mu.Unlock()
		if invalid {
			return Snapshot{}, errors.New("test read failure")
		}
		return managerSnapshot(), nil
	})
	if err := m.Configure(Settings{Version: 2, Driver: "pulseaudio"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	fontDir := t.TempDir()
	fontPath := filepath.Join(fontDir, "chosen.sf2")
	if err := os.WriteFile(fontPath, []byte("RIFFxxxxsfbk"), 0600); err != nil {
		t.Fatal(err)
	}
	entry, err := m.AddFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fontPath); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDefaultFont(entry.ID); err == nil {
		t.Fatal("missing default font accepted")
	}
	if s := m.Status(); !s.Playing || s.Settings.DefaultFont != "" {
		t.Fatal("bad font destroyed working playback")
	}
	list := m.SoundFonts()
	if len(list.Fonts) != 1 || list.Fonts[0].Exists || list.Fonts[0].Error == "" {
		t.Fatalf("font list state wrong: %+v", list.Fonts)
	}
	mu.Lock()
	invalid = true
	mu.Unlock()
	m.Changed("p_a")
	s := waitStatus(t, m, func(s Status) bool { return s.Error != "" })
	if !s.Playing || s.Revision != 1 {
		t.Fatal("invalid update replaced old snapshot")
	}
	m.Deleted("p_a")
	if m.Status().Playing {
		t.Fatal("deleted project still playing")
	}
}
func TestAudioSettingsRejectUnsupportedAndPreserveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.json")
	original := []byte(`{"version":999,"driver":"pulseaudio"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	m := New(dir, func(string) (Snapshot, error) { return managerSnapshot(), nil })
	defer m.Close()
	if err := m.Configure(Settings{Version: 2, Driver: "pulseaudio"}); err == nil {
		t.Fatal("unsupported settings overwritten")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("old file changed")
	}
}

func TestStopCancelsPlayStillReadingProject(t *testing.T) {
	for _, mode := range []string{"global stop", "project stop", "project delete"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			m := offlineManager(t, func(string) (Snapshot, error) { close(entered); <-release; return managerSnapshot(), nil })
			result := make(chan error, 1)
			go func() { result <- m.Play(Request{ProjectID: "p_a", Revision: 1}) }()
			<-entered
			start := time.Now()
			switch mode {
			case "global stop":
				m.Stop()
			case "project stop":
				m.StopProject("p_a")
			case "project delete":
				m.Deleted("p_a")
			}
			if time.Since(start) > 50*time.Millisecond {
				t.Fatal("stop waited for project I/O")
			}
			close(release)
			if err := <-result; err == nil {
				t.Fatal("cancelled Play succeeded")
			}
			if m.Status().Playing {
				t.Fatal("stale Play restarted audio after stop")
			}
		})
	}
}

func writeTestFont(t *testing.T, dir, name string) string {
	t.Helper()
	// BasicFont 是真实可加载的最小 SF2；伪造字节过不了 FluidSynth。
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, BasicFont(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSoundFontManagerListAddRemovePriorityAndFallback(t *testing.T) {
	m := offlineManager(t, func(string) (Snapshot, error) { return managerSnapshot(), nil })
	dir := t.TempDir()
	// 无打包音色、无用户音色：basic 兜底且提示明确。
	if err := m.Configure(Settings{Version: 2, Driver: "pulseaudio"}); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.ResolvedKind != "basic" || s.ResolvedFont == "" || s.Notice == "" {
		t.Fatalf("basic fallback state wrong: %+v", s)
	}
	list := m.SoundFonts()
	if !list.Basic.Active || list.Bundled.Exists || len(list.Fonts) != 0 {
		t.Fatalf("empty list state wrong (basic 应为在用兜底): %+v", list)
	}
	// 添加两个用户音色：不自动切换默认；重复路径拒绝。
	a, err := m.AddFont(writeTestFont(t, dir, "alpha.sf2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AddFont(a.Path); err == nil {
		t.Fatal("duplicate font path accepted")
	}
	b, err := m.AddFont(writeTestFont(t, dir, "beta.sf3"))
	if err != nil {
		t.Fatal(err)
	}
	list = m.SoundFonts()
	if len(list.Fonts) != 2 || list.Fonts[0].ID != a.ID || list.Fonts[1].ID != b.ID {
		t.Fatalf("font order wrong: %+v", list.Fonts)
	}
	if s := m.Status(); s.Settings.DefaultFont != "" || s.ResolvedKind != "basic" {
		t.Fatal("adding a font silently switched the default")
	}
	// 非法文件拒绝添加且不改列表。
	if _, err = m.AddFont(filepath.Join(dir, "nope.txt")); err == nil {
		t.Fatal("invalid extension accepted")
	}
	// 切换默认：立即生效（引擎重建，播放按纪律停止）。
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = m.SetDefaultFont(b.ID); err != nil {
		t.Fatal(err)
	}
	s := m.Status()
	if s.Playing || s.Settings.DefaultFont != b.ID || s.ResolvedKind != "user" || s.ResolvedFont != b.Name {
		t.Fatalf("default switch state wrong: %+v", s)
	}
	list = m.SoundFonts()
	for _, f := range list.Fonts {
		if f.ID == b.ID && (!f.Active || !f.IsDefault) {
			t.Fatalf("beta should be active default: %+v", f)
		}
		if f.ID == a.ID && (f.Active || f.IsDefault) {
			t.Fatalf("alpha should be inactive non-default: %+v", f)
		}
	}
	// 优先级：beta 上移到第一位；持久化按新顺序编号。
	if err = m.MoveFont(b.ID, -1); err != nil {
		t.Fatal(err)
	}
	list = m.SoundFonts()
	if len(list.Fonts) != 2 || list.Fonts[0].ID != b.ID || list.Fonts[1].ID != a.ID {
		t.Fatalf("priority move failed: %+v", list.Fonts)
	}
	// 移除默认音色：默认回退打包/自动，条目消失，文件保留。
	if err = m.RemoveFont(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(b.Path); err != nil {
		t.Fatal("removing list entry deleted the file on disk")
	}
	if s = m.Status(); s.Settings.DefaultFont != "" {
		t.Fatal("default survived removing its font")
	}
	if err = m.RemoveFont("bundled"); err == nil {
		t.Fatal("removing the bundled pseudo-entry accepted")
	}
	// 重开后配置持久（audio.json v2 列表与默认）。
	dir2 := m.dir
	get := m.get
	m.Close()
	m2 := New(dir2, get)
	defer m2.Close()
	list = m2.SoundFonts()
	if len(list.Fonts) != 1 || list.Fonts[0].ID != a.ID || list.Fonts[0].Priority != 1 {
		t.Fatalf("persisted list wrong after reopen: %+v", list.Fonts)
	}
}

func TestAudioSettingsRejectV1FileAndBundledResolution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"driver":"pulseaudio","soundFont":"/old/x.sf2"}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(dir, func(string) (Snapshot, error) { return managerSnapshot(), nil })
	defer m.Close()
	if err := m.Configure(Settings{Version: 2, Driver: "pulseaudio"}); err == nil {
		t.Fatal("v1 settings file should be rejected until reconfigured")
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"version":1`) {
		t.Fatal("rejected file was rewritten")
	}
	// 用户显式重置：旧文件改名保留，恢复可用配置。
	if err := m.ResetSettings(); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path + ".unsupported")
	if err != nil || !strings.Contains(string(saved), `"version":1`) {
		t.Fatalf("v1 file not preserved on reset: %v", err)
	}
	m.open = func(font, driver string) (*native, error) { return openNative(font, driver, true) }
	// 打包默认音色放入数据目录后：新配置解析为 bundled。
	bundled := writeTestFont(t, dir, "MuseScore_General.sf3")
	audioDir := filepath.Join(dir, "audio")
	if err := os.MkdirAll(audioDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(bundled, filepath.Join(audioDir, BundledFontName)); err != nil {
		t.Fatal(err)
	}
	if err := m.Configure(Settings{Version: 2, Driver: "pulseaudio"}); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.ResolvedKind != "bundled" || s.Notice != "" {
		t.Fatalf("bundled resolution wrong: %+v", s)
	}
	list := m.SoundFonts()
	if !list.Bundled.Exists || !list.Bundled.IsDefault || !list.Bundled.Active {
		t.Fatalf("bundled list state wrong: %+v", list.Bundled)
	}
	// 默认音色文件被删后重开：回退并给出 notice，不静默。
	if err := os.Remove(filepath.Join(audioDir, BundledFontName)); err != nil {
		t.Fatal(err)
	}
	m3 := New(dir, func(string) (Snapshot, error) { return managerSnapshot(), nil })
	m3.open = func(font, driver string) (*native, error) { return openNative(font, driver, true) }
	defer m3.Close()
	if err := m3.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if s := m3.Status(); s.ResolvedKind != "basic" || s.Notice == "" {
		t.Fatalf("deferred font failure must fall back with notice: %+v", s)
	}
}

func peakOf(l []float32) float32 {
	peak := float32(0)
	for _, v := range l {
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

func TestAuditionBypassesTimelineAndCleansUp(t *testing.T) {
	m := offlineManager(t, func(string) (Snapshot, error) { return managerSnapshot(), nil })
	if err := m.Configure(Settings{Version: 2, Driver: "pulseaudio"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	m.Stop()
	waitStatus(t, m, func(s Status) bool { return !s.Playing })
	engine := func() *native { m.mu.Lock(); defer m.mu.Unlock(); return m.engine }
	silent, _ := engine().render(2048)
	if p := peakOf(silent); p > 0.0001 {
		t.Fatalf("stopped engine should be silent before audition, peak %v", p)
	}
	before := m.Status()
	// 停止状态下试听（绕过 timeline）：立即出声，不产生工程修订。
	if err := m.Audition(AuditionRequest{Program: 40, Bank: 0, Keys: []int{60}, DurationMs: 300}); err != nil {
		t.Fatal(err)
	}
	heard, _ := engine().render(2048)
	if p := peakOf(heard); p == 0 {
		t.Fatal("audition produced no audio while playback stopped")
	}
	after := m.Status()
	if after.Revision != before.Revision || after.Playing {
		t.Fatalf("audition touched project state: %+v -> %+v", before, after)
	}
	// 显式停止：all-notes-off 后静音。
	m.AuditionStop()
	stopped, _ := engine().render(2048)
	if p := peakOf(stopped); p > 0.0001 {
		t.Fatalf("audition stop left sound, peak %v", p)
	}
	// 时限到期自动结束（100ms = 4800 帧）。
	if err := m.Audition(AuditionRequest{Program: 0, Bank: 0, Keys: []int{60, 64, 67}, DurationMs: 100}); err != nil {
		t.Fatal(err)
	}
	chord, _ := engine().render(4800)
	if p := peakOf(chord); p == 0 {
		t.Fatal("chord audition produced no audio")
	}
	expired, _ := engine().render(2048)
	if p := peakOf(expired); p > 0.0001 {
		t.Fatalf("expired audition kept sounding, peak %v", p)
	}
	// 打击组 bank 与参数校验。
	if err := m.Audition(AuditionRequest{Program: 0, Bank: 128, Keys: []int{38}, DurationMs: 100}); err != nil {
		t.Fatal(err)
	}
	m.AuditionStop()
	for _, bad := range []AuditionRequest{
		{Program: 128, Bank: 0, Keys: []int{60}, DurationMs: 100},
		{Program: 0, Bank: 5, Keys: []int{60}, DurationMs: 100},
		{Program: 0, Bank: 0, Keys: []int{}, DurationMs: 100},
		{Program: 0, Bank: 0, Keys: []int{128}, DurationMs: 100},
		{Program: 0, Bank: 0, Keys: []int{60}, DurationMs: 10},
	} {
		if err := m.Audition(bad); err == nil {
			t.Fatalf("invalid audition accepted: %+v", bad)
		}
	}
}
