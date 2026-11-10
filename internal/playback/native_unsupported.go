//go:build !linux || !cgo

package playback

import (
	"context"
	"errors"

	"icetudio/internal/pluginhost"
)

type native struct{}
type nativeStatus struct {
	Frame, Callbacks, Misses, Loops int64
	Playing, CountIn                bool
}

func openNative(string, string, bool) (*native, error) {
	return nil, errors.New("当前构建未启用 Linux/cgo FluidSynth 播放；仍可编辑和导出 MIDI")
}
func nativeVersion() string                    { return "unavailable" }
func nativeAvailable() bool                    { return false }
func (*native) channelValue(int, int, int) int { return -1 }
func (*native) close()                         {}
func (*native) stop()                          {}
func (*native) status() nativeStatus           { return nativeStatus{} }
func (*native) install(*Score, int64, int64, int64, bool, bool) error {
	return errors.New("当前构建不支持原生播放")
}
func (*native) render(n int) ([]float32, []float32) { return make([]float32, n), make([]float32, n) }
func (*native) audition(int, int, int, []int, int, int64) error {
	return errors.New("当前构建不支持原生播放")
}
func (*native) auditionStop() {}

func (*native) installCount(*Score, int64, int64, int64, bool, bool, int, int64) error {
	return errors.New("当前构建不支持原生播放")
}

func (*native) meters(*mixPlan) map[string]Meter   { return nil }
func (*native) resetMeters()                       {}
func (*native) override(int, int, string, float64) {}
func (*native) callbackLoad() (int, int64)         { return 0, 0 }

func (*native) installContext(context.Context, *Score, int64, int64, int64, bool, bool, int, int64) error {
	return errors.New("当前构建不支持原生播放")
}
func (*native) mixerLatency() int      { return 0 }
func (*native) pluginIssues() []string { return nil }

func (*native) installPrepared(_ *Score, _, _, _ int64, _, _ bool, _ int, _ int64, _ *mixPlan, workers []*pluginhost.Instance) error {
	closePlugins(workers)
	return errors.New("当前构建不支持原生播放")
}

func (*native) needsPluginRebuild() bool { return false }

func (*native) hasPluginWorkers() bool { return false }

func (*native) pluginOverride(int, int, uint32, float64) bool { return false }
