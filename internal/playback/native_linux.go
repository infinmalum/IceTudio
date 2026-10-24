//go:build linux && cgo

package playback

/*
#cgo LDFLAGS: -lfluidsynth -lpthread
#include "native.h"
#include <stdlib.h>
*/
import "C"
import (
	"errors"
	"unsafe"
)

type native struct{ ptr *C.ice_engine }
type nativeStatus struct {
	Frame, Callbacks, Misses, Loops int64
	Playing                         bool
}

func openNative(font, driver string, offline bool) (*native, error) {
	f, d := C.CString(font), C.CString(driver)
	defer C.free(unsafe.Pointer(f))
	defer C.free(unsafe.Pointer(d))
	off := C.int(0)
	if offline {
		off = 1
	}
	p := C.ice_new(f, d, off)
	if p == nil {
		return nil, errors.New("FluidSynth 无法打开音色或音频设备；请检查 SoundFont、驱动及系统音频服务")
	}
	return &native{p}, nil
}
func nativeVersion() string { return C.GoString(C.ice_version()) }
func nativeAvailable() bool { return true }
func (n *native) channelValue(channel, kind, cc int) int {
	return int(C.ice_channel_value(n.ptr, C.int(channel), C.int(kind), C.int(cc)))
}
func (n *native) close() {
	if n != nil && n.ptr != nil {
		C.ice_delete(n.ptr)
		n.ptr = nil
	}
}
func (n *native) stop() { C.ice_stop(n.ptr) }
func (n *native) status() nativeStatus {
	s := C.ice_get_status(n.ptr)
	return nativeStatus{int64(s.frame), int64(s.callbacks), int64(s.misses), int64(s.loops), s.playing != 0}
}
func (n *native) install(s *Score, start, loopA, loopB int64, loop, playing bool) error {
	if err := s.ValidateRange(start, loopA, loopB, loop); err != nil {
		return err
	}
	startFrame, a, b := s.Timeline.Frame(float64(start)), s.Timeline.Frame(float64(loopA)), s.Timeline.Frame(float64(loopB))
	if loop && b <= a {
		return errors.New("循环间隔小于一个音频采样帧")
	}
	convert := func(events []Event) []C.ice_event {
		out := make([]C.ice_event, len(events))
		for i, e := range events {
			out[i] = C.ice_event{frame: C.int64_t(e.Frame), kind: C.int32_t(e.Kind), channel: C.int32_t(e.Channel), a: C.int32_t(e.A), b: C.int32_t(e.B), token: C.uint32_t(e.Token)}
		}
		return out
	}
	events, initial, chase := convert(s.Events), convert(s.Chase(startFrame)), convert(s.Chase(a))
	ptr := func(v []C.ice_event) *C.ice_event {
		if len(v) == 0 {
			return nil
		}
		return &v[0]
	}
	var channels *C.int32_t
	if len(s.Channels) > 0 {
		channels = (*C.int32_t)(unsafe.Pointer(&s.Channels[0]))
	}
	l, p := C.int(0), C.int(0)
	if loop {
		l = 1
	}
	if playing {
		p = 1
	}
	end := s.EndFrame + SampleRate/4
	plan := C.ice_plan_new(ptr(events), C.int(len(events)), ptr(initial), C.int(len(initial)), ptr(chase), C.int(len(chase)), channels, C.int(len(s.Channels)), C.int64_t(startFrame), C.int64_t(end), C.int64_t(a), C.int64_t(b), l)
	if plan == nil {
		return errors.New("无法分配播放快照")
	}
	C.ice_install(n.ptr, plan, p)
	return nil
}
func (n *native) render(frames int) ([]float32, []float32) {
	l, r := make([]float32, frames), make([]float32, frames)
	if frames > 0 {
		C.ice_render(n.ptr, C.int(frames), (*C.float)(unsafe.Pointer(&l[0])), (*C.float)(unsafe.Pointer(&r[0])))
	}
	return l, r
}
func (n *native) audition(channel, program, bank int, keys []int, velocity int, frames int64) error {
	if len(keys) == 0 || len(keys) > 8 {
		return errors.New("试听须为 1–8 个音")
	}
	ck := make([]C.int, len(keys))
	for i, k := range keys {
		ck[i] = C.int(k)
	}
	C.ice_audition(n.ptr, C.int(channel), C.int(program), C.int(bank), &ck[0], C.int(len(keys)), C.int(velocity), C.int64_t(frames))
	return nil
}
func (n *native) auditionStop() { C.ice_audition_stop(n.ptr) }
