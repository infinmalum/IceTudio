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
	Playing, CountIn                bool
}

func openNative(font, driver string, offline bool) (*native, error) {
	return openNativePeriod(font, driver, offline, 128)
}

func openNativePeriod(font, driver string, offline bool, period int) (*native, error) {
	f, d := C.CString(font), C.CString(driver)
	defer C.free(unsafe.Pointer(f))
	defer C.free(unsafe.Pointer(d))
	off := C.int(0)
	if offline {
		off = 1
	}
	p := C.ice_new(f, d, off, C.int(period))
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
	return nativeStatus{int64(s.frame), int64(s.callbacks), int64(s.misses), int64(s.loops), s.playing != 0, s.counting != 0}
}
func (n *native) install(s *Score, start, loopA, loopB int64, loop, playing bool) error {
	return n.installCount(s, start, loopA, loopB, loop, playing, 0, 0)
}
func (n *native) installCount(s *Score, start, loopA, loopB int64, loop, playing bool, beats int, beatFrames int64) error {
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
	if s.mix != nil {
		end = s.EndFrame + int64(s.mix.latency) + max(SampleRate/4, int64(s.mix.tail))
	}
	plan := C.ice_plan_new(ptr(events), C.int(len(events)), ptr(initial), C.int(len(initial)), ptr(chase), C.int(len(chase)), channels, C.int(len(s.Channels)), C.int64_t(startFrame), C.int64_t(end), C.int64_t(a), C.int64_t(b), l)
	if plan == nil {
		return errors.New("无法分配播放快照")
	}
	if err := attachMixer(plan, s.mix); err != nil {
		C.ice_plan_delete(plan)
		return err
	}
	C.ice_plan_audio_limit(plan, C.int64_t(s.audioLimit))
	if C.ice_plan_audio_reserve(plan, C.int(len(s.Audio))) == 0 {
		C.ice_plan_delete(plan)
		return errors.New("无法分配音频声部快照")
	}
	sources := map[string]int{}
	for i, region := range s.Audio {
		c := region.Clip
		if int64(len(region.Samples)) != region.Asset.Frames*2 || len(region.Samples) == 0 {
			C.ice_plan_delete(plan)
			return errors.New("音频快照缺少采样")
		}
		reuse := -1
		if previous, ok := sources[region.Asset.Digest]; ok {
			reuse = previous
		} else {
			sources[region.Asset.Digest] = i
		}
		reverse := C.int(0)
		if c.Reverse {
			reverse = 1
		}
		reg := C.ice_audio_region{start: C.int64_t(region.Frame), source: C.int64_t(c.Source), source_frames: C.int64_t(c.Frames),
			length: C.int64_t(c.Frames), fadein: C.int64_t(c.FadeIn), fadeout: C.int64_t(c.FadeOut), gain: C.float(float64(c.Gain) / 3276800),
			reverse: reverse, track: C.int(region.Track)}
		if v := region.Voice; v != nil {
			reg.length = C.int64_t(region.Length)
			reg.rate = C.double(v.rate)
			reg.attack, reg.decay, reg.gate, reg.release = C.int64_t(v.attack), C.int64_t(v.decay), C.int64_t(v.gate), C.int64_t(v.release)
			reg.sustain = C.float(v.sustain)
			reg.gain *= C.float(v.velocity)
		}
		if C.ice_plan_audio(plan, (*C.int16_t)(unsafe.Pointer(&region.Samples[0])), C.int64_t(region.Asset.Frames), C.int(reuse), reg) == 0 {
			C.ice_plan_delete(plan)
			return errors.New("无法分配音频片段快照")
		}
	}
	C.ice_plan_audio_sort(plan)
	C.ice_install(n.ptr, plan, p, C.int(beats), C.int64_t(beatFrames))
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
	if C.ice_audition(n.ptr, C.int(channel), C.int(program), C.int(bank), &ck[0], C.int(len(keys)), C.int(velocity), C.int64_t(frames)) == 0 {
		return errors.New("无法初始化试听音色")
	}
	return nil
}
func (n *native) auditionStop() { C.ice_audition_stop(n.ptr) }
