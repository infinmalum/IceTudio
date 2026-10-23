package playback

import (
	"errors"
	"icetudio/internal/project"
	"os"
	"path/filepath"
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
	if err := m.Configure(Settings{Version: 1, Driver: "pulseaudio"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Play(Request{ProjectID: "p_a", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := m.Configure(Settings{Version: 1, Driver: "pulseaudio", SoundFont: "/missing/icetudio.sf2"}); err == nil {
		t.Fatal("missing font accepted")
	}
	if s := m.Status(); !s.Playing || s.Settings.SoundFont != "" {
		t.Fatal("bad font destroyed working playback")
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
	if err := m.Configure(Settings{Version: 1, Driver: "pulseaudio"}); err == nil {
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
