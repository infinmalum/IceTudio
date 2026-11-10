//go:build linux && cgo

package playback

/*
#cgo LDFLAGS: -lm
#include "native.h"
*/
import "C"

import "fmt"

func attachMixer(plan *C.ice_plan, mix *mixPlan, offline bool) error {
	if mix == nil {
		return nil
	}
	m := C.ice_mix_new(C.int(len(mix.nodes)), C.int(len(mix.edges)), C.int(mix.master))
	if m == nil {
		return fmt.Errorf("无法分配混音快照")
	}
	// Transfer ownership immediately. All error paths free the plan.
	C.ice_plan_mixer(plan, m)
	withPoints := func(p mixParam, apply func(*C.ice_mix_point, C.int) bool) bool {
		points := make([]C.ice_mix_point, len(p.points))
		var ptr *C.ice_mix_point
		for j, pt := range p.points {
			points[j] = C.ice_mix_point{frame: C.int64_t(pt.frame), value: C.float(pt.value)}
			if pt.step {
				points[j].step = 1
			}
		}
		if len(points) > 0 {
			ptr = &points[0]
		}
		return apply(ptr, C.int(len(points)))
	}
	param := func(i, slot, which int, p mixParam) bool {
		return withPoints(p, func(ptr *C.ice_mix_point, count C.int) bool {
			return C.ice_mix_parameter(m, C.int(i), C.int(slot), C.int(which), C.float(p.value), ptr, count) != 0
		})
	}
	plugin := func(i, j int, fx mixFX) bool {
		var wire *C.ice_plugin_wire
		var count int
		if fx.worker != nil {
			wire = (*C.ice_plugin_wire)(fx.worker.Shared())
			count = len(fx.parameters)
		}
		off := C.int(0)
		if offline {
			off = 1
		}
		if C.ice_mix_plugin(m, C.int(i), C.int(j), wire, C.int(fx.latency), off, C.int(count)) == 0 {
			return false
		}
		if wire == nil {
			return true
		}
		for k, p := range fx.parameters {
			if !withPoints(p, func(ptr *C.ice_mix_point, n C.int) bool {
				return C.ice_mix_plugin_param(m, C.int(i), C.int(j), C.int(k), C.uint32_t(fx.plugin.Parameters[k].ID), C.float(p.value), C.float(fx.initial[k]), ptr, n) != 0
			}) {
				return false
			}
		}
		return true
	}
	fail := fmt.Errorf("无法分配混音参数或延迟缓冲")
	for i, n := range mix.nodes {
		if C.ice_mix_node(m, C.int(i), C.int(n.track), C.int(n.align)) == 0 ||
			!param(i, -1, 0, n.gain) || !param(i, -1, 1, n.pan) {
			return fail
		}
		if n.instrument != nil && !plugin(i, -1, *n.instrument) {
			return fail
		}
		for j, fx := range n.effects {
			if fx.plugin != nil {
				if !plugin(i, j, fx) || !param(i, j, 1, fx.wet) {
					return fail
				}
				continue
			}
			if C.ice_mix_effect(m, C.int(i), C.int(j), C.int(fx.kind)) == 0 ||
				!param(i, j, 0, fx.amount) || !param(i, j, 1, fx.wet) {
				return fail
			}
		}
	}
	for i, e := range mix.edges {
		var side C.int
		if e.side {
			side = 1
		}
		if C.ice_mix_edge(m, C.int(i), C.int(e.from), C.int(e.to), C.int(e.delay), C.float(e.gain), side) == 0 {
			return fail
		}
	}
	return nil
}

func (n *native) meters(mix *mixPlan) map[string]Meter {
	if mix == nil || len(mix.nodes) == 0 {
		return nil
	}
	var data [C.ICE_MIX_NODES]C.ice_meter
	C.ice_read_meters(n.ptr, &data[0], C.int(len(mix.nodes)))
	out := make(map[string]Meter, len(mix.nodes))
	for i, node := range mix.nodes {
		m := data[i]
		out[node.id] = Meter{float64(m.peak), float64(m.hold), uint64(m.clips)}
	}
	return out
}

func (n *native) resetMeters() { C.ice_reset_meters(n.ptr) }
func (n *native) override(i, slot int, param string, value float64) {
	var which C.int
	if param == "pan" || param == "wet" {
		which = 1
	}
	C.ice_override(n.ptr, C.int(i), C.int(slot), which, C.float(value))
}
func (n *native) callbackLoad() (int, int64) {
	return int(C.ice_callback_p99(n.ptr)), int64(C.ice_overruns(n.ptr))
}

func (n *native) callbackFrames() int { return int(C.ice_callback_frames(n.ptr)) }

func (n *native) pluginOverride(i, j int, id uint32, value float64) bool {
	return C.ice_plugin_override(n.ptr, C.int(i), C.int(j), C.uint32_t(id), C.float(value)) != 0
}
