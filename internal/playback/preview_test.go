package playback

import (
	"encoding/json"
	"testing"
)

func TestPreviewUsesTemporaryScoreAndIsolatesOrdinaryPlayback(t *testing.T) {
	original := managerSnapshot()
	raw, _ := json.Marshal(original)
	var candidate Snapshot
	if err := json.Unmarshal(raw, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.State.Tracks[0].Clips[0].Notes[0].Pitch += 12
	m := offlineManager(t, func(string) (Snapshot, error) { return original, nil })
	req := Request{ProjectID: original.ID, Revision: original.Revision, Loop: true, LoopStart: 0, LoopEnd: 1920}
	play := func(s Snapshot, variant string) float64 {
		t.Helper()
		if err := m.PlayPreview(req, s, "candidate-token", variant); err != nil {
			t.Fatal(err)
		}
		status := m.Status()
		if status.PreviewID != "candidate-token" || status.PreviewVariant != variant || !status.Playing {
			t.Fatalf("missing preview state: %+v", status)
		}
		m.mu.Lock()
		data, _ := m.engine.render(12000)
		m.mu.Unlock()
		if energy(data) < .00001 {
			t.Fatal("native preview was silent")
		}
		return crossingFrequency(data[4800:])
	}
	a, b := play(original, "original"), play(candidate, "candidate")
	if b < a*1.8 || b > a*2.2 {
		t.Fatalf("candidate did not use changed pitch: original %g candidate %g", a, b)
	}
	if original.State.Tracks[0].Clips[0].Notes[0].Pitch != 69 {
		t.Fatal("temporary score modified the committed snapshot")
	}
	if err := m.Seek(req); err == nil {
		t.Fatal("ordinary seek silently edited a temporary score")
	}
	m.Changed(original.ID)
	if s := m.Status(); s.Playing || s.PreviewID != "" {
		t.Fatalf("commit did not retire preview: %+v", s)
	}
	if err := m.Play(req); err != nil {
		t.Fatal(err)
	}
	m.StopPreview("candidate-token")
	if s := m.Status(); !s.Playing || s.PreviewID != "" {
		t.Fatalf("late preview cleanup stopped ordinary playback: %+v", s)
	}
	m.Stop()
	m.mu.Lock()
	data, _ := m.engine.render(16000)
	m.mu.Unlock()
	if energy(data) > .000001 {
		t.Fatal("preview/ordinary switch leaked sustained notes after stop")
	}
}

func TestPreviewCancellationPreventsLateInstallation(t *testing.T) {
	for _, mode := range []string{"stop", "changed", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			m := offlineManager(t, func(string) (Snapshot, error) { close(entered); <-release; return managerSnapshot(), nil })
			done := make(chan error, 1)
			go func() {
				done <- m.PlayPreview(Request{ProjectID: "p_a", Revision: 1}, managerSnapshot(), "pending-token", "candidate")
			}()
			<-entered
			switch mode {
			case "stop":
				m.StopPreview("pending-token")
			case "changed":
				m.Changed("p_a")
			case "deleted":
				m.Deleted("p_a")
			}
			close(release)
			if err := <-done; err == nil {
				t.Fatal("cancelled preview installed")
			}
			if s := m.Status(); s.Playing || s.PreviewID != "" {
				t.Fatalf("late preview restarted audio: %+v", s)
			}
		})
	}
}
