package playback

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in integration with a PRIVATE PipeWire graph, Pulse server and policy
// instance. Never stops/reconfigures the user's system audio service/devices.
func TestIsolatedAudioDeviceRestart(t *testing.T) {
	if os.Getenv("ICETUDIO_AUDIO_DEVICE_TEST") != "1" {
		t.Skip("opt-in isolated audio service integration")
	}
	for _, name := range []string{"pipewire", "pipewire-pulse", "wireplumber", "pactl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs("../../.cache")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "p2-audio-")
	if err != nil {
		t.Fatal(err)
	}
	// Keep artifacts/logs for diagnosis; this is an ephemeral validation service.
	t.Log("isolated audio artifacts:", dir)
	pulsePath := filepath.Join(dir, "pulse.sock")
	core := `context.properties = { core.daemon = true core.name = p2-core support.dbus = false default.clock.rate = 48000 default.clock.quantum = 128 }
context.spa-libs = { audio.convert.* = audioconvert/libspa-audioconvert support.* = support/libspa-support }
context.modules = [ { name = libpipewire-module-protocol-native } { name = libpipewire-module-access args = { access.socket = { p2-core = "unrestricted" } } } { name = libpipewire-module-metadata } { name = libpipewire-module-spa-node-factory } { name = libpipewire-module-client-node } { name = libpipewire-module-adapter } { name = libpipewire-module-link-factory } ]
context.objects = [ { factory = adapter args = { factory.name = support.null-audio-sink node.name = p2_a media.class = Audio/Sink audio.position = [ FL FR ] node.virtual = true object.linger = true } } ]`
	pulse := fmt.Sprintf(`context.properties = { support.dbus = false }
context.spa-libs = { audio.convert.* = audioconvert/libspa-audioconvert support.* = support/libspa-support }
context.modules = [ { name = libpipewire-module-protocol-native } { name = libpipewire-module-client-node } { name = libpipewire-module-adapter } { name = libpipewire-module-metadata } { name = libpipewire-module-protocol-pulse } ]
pulse.properties = { server.address = [ %q ] pulse.min.req = 128/48000 pulse.default.req = 128/48000 pulse.default.tlength = 384/48000 }`, "unix:"+pulsePath)
	for name, data := range map[string]string{"core.conf": core, "pulse.conf": pulse} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIPEWIRE_RUNTIME_DIR", dir)
	t.Setenv("PIPEWIRE_REMOTE", "p2-core")
	t.Setenv("PULSE_SERVER", "unix:"+pulsePath)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	start := func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(name, args...)
		file, e := os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		cmd.Stdout = file
		cmd.Stderr = file
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = file.Close() })
		return cmd
	}
	start("pipewire", "-c", filepath.Join(dir, "core.conf"))
	start("wireplumber", "--profile", "policy")
	server := start("pipewire-pulse", "-c", filepath.Join(dir, "pulse.conf"))
	pactl := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "pactl", args...).CombinedOutput()
	}
	waitServer := func() {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if _, e := pactl("info"); e == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("private Pulse server unavailable; inspect artifacts")
	}
	waitServer()
	m := New(filepath.Join(dir, "app"), func(string) (Snapshot, error) { return managerSnapshot(), nil })
	t.Cleanup(m.Close)
	if err = m.Play(Request{ProjectID: "p_a", Revision: 1, Loop: true, LoopEnd: 480}); err != nil {
		t.Fatal(err)
	}
	wait := func(predicate func(Status) bool) Status {
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			s := m.Status()
			if predicate(s) {
				return s
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("audio status timeout: %+v", m.Status())
		return Status{}
	}
	first := wait(func(s Status) bool { return s.Callbacks > 4 && s.Playing })
	if _, err = pactl("load-module", "module-null-sink", "sink_name=p2_b"); err != nil {
		t.Fatal(err)
	}
	data, err := pactl("-f", "json", "list", "sink-inputs")
	if err != nil {
		t.Fatal(err)
	}
	var streams []struct {
		Index int `json:"index"`
	}
	if err = json.Unmarshal(data, &streams); err != nil || len(streams) != 1 {
		t.Fatalf("private streams: %s %v", data, err)
	}
	if _, err = pactl("move-sink-input", fmt.Sprint(streams[0].Index), "p2_b"); err != nil {
		t.Fatal(err)
	}
	switched := wait(func(s Status) bool { return s.Callbacks > first.Callbacks+8 && s.Playing })
	_ = server.Process.Kill()
	_ = server.Wait()
	lost := wait(func(s Status) bool { return strings.Contains(s.Error, "2 秒") })
	if lost.ProjectID != "p_a" || lost.Revision != 1 {
		t.Fatal("device loss discarded project target")
	}
	start("pipewire-pulse", "-c", filepath.Join(dir, "pulse.conf"))
	waitServer()
	if err = m.Configure(Settings{Version: 1, Driver: "pulseaudio"}); err != nil {
		t.Fatal(err)
	}
	if m.Status().Playing {
		t.Fatal("reinitialization auto-resumed playback")
	}
	if err = m.Play(Request{ProjectID: "p_a", Revision: 1, Loop: true, LoopEnd: 480}); err != nil {
		t.Fatal(err)
	}
	recovered := wait(func(s Status) bool { return s.Callbacks > 5 && s.Playing })
	if err = m.Configure(Settings{Version: 1, Driver: "alsa", SoundFont: filepath.Join(dir, "absent.sf2")}); err == nil {
		t.Fatal("unavailable font accepted")
	}
	if !m.Status().Playing || m.Status().Settings.Driver != "pulseaudio" {
		t.Fatal("failed configuration destroyed working engine")
	}
	started := time.Now()
	m.Stop()
	elapsed := time.Since(started)
	if m.Status().Playing || elapsed > 50*time.Millisecond {
		t.Fatalf("stop: %v %+v", elapsed, m.Status())
	}
	t.Logf("real isolated device switch callbacks=%d; lost=%q; restart callbacks=%d misses=%d; stop=%v; virtual devices, no physical latency claim", switched.Callbacks, lost.Error, recovered.Callbacks, recovered.MissedBlocks, elapsed)
}
