//go:build !linux || !cgo

package playback

import "errors"

type native struct{}
type nativeStatus struct {
	Frame, Callbacks, Misses, Loops int64
	Playing                         bool
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
