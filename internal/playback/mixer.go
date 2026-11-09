package playback

import (
	"fmt"
	"math"

	"icetudio/internal/project"
)

type mixParam struct {
	value  float64
	points []mixPoint
}
type mixPoint struct {
	frame int64
	value float64
	step  bool
}
type mixFX struct {
	kind        int
	id          string
	amount, wet mixParam
}
type mixNode struct {
	id                                string
	gain, pan                         mixParam
	effects                           []mixFX
	track, align, outputLatency, tail int
}
type mixEdge struct {
	from, to, delay int
	gain            float64
	side            bool
}
type mixPlan struct {
	nodes                 []mixNode
	edges                 []mixEdge
	master, latency, tail int
}

// All inputs to a node (including the detector feed) align to the slowest
// predecessor. Intentional echo/reverb time is an effect, not graph latency.
func compileMixer(st *project.State, timeline Timeline) (*mixPlan, error) {
	var mixer project.Mixer
	if st.Mixer != nil {
		mixer = *st.Mixer
	}
	order, routes, err := project.MixOrder(st, mixer)
	if err != nil {
		return nil, err
	}
	out := &mixPlan{}
	index, tracks := map[string]int{}, map[string]int{}
	for i, tr := range st.Tracks {
		tracks[tr.ID] = i
	}
	params := map[[2]string][]mixPoint{}
	for _, lane := range mixer.Lanes {
		key := [2]string{lane.Target, lane.Parameter}
		for _, point := range lane.Points {
			frame := timeline.Frame(float64(point.Tick))
			if frame > MaxFrames {
				return nil, fmt.Errorf("自动化超出 24 小时播放上限")
			}
			p := mixPoint{frame, point.Value, point.Step}
			points := params[key]
			if n := len(points); n > 0 && points[n-1].frame == frame {
				points[n-1] = p
			} else {
				params[key] = append(points, p)
			}
		}
	}
	param := func(id, key string, value float64) mixParam {
		return mixParam{value, params[[2]string{id, key}]}
	}
	for i, id := range order {
		ch := mixer.Channel(id)
		node := mixNode{id: id, gain: param(id, "gain", ch.Gain), pan: param(id, "pan", ch.Pan), track: -1}
		if t, ok := tracks[id]; ok {
			node.track = t
		}
		for _, edge := range routes {
			if edge.To == id {
				node.align = max(node.align, out.nodes[index[edge.From]].outputLatency)
				node.tail = max(node.tail, out.nodes[index[edge.From]].tail)
			}
		}
		node.outputLatency = node.align
		for _, effect := range ch.Effects {
			if effect.Bypass {
				continue
			}
			kind := map[string]int{
				"eq": 1, "compressor": 2, "limiter": 3, "delay": 4, "reverb": 5,
				"chorus": 6, "lowpass": 7, "drive": 8, "gate": 9,
			}[effect.Kind]
			node.effects = append(node.effects, mixFX{kind, effect.ID, param(effect.ID, "amount", effect.Amount), param(effect.ID, "wet", effect.Wet)})
			switch effect.Kind {
			case "limiter":
				node.outputLatency += 64
			case "delay":
				node.tail += 4 * SampleRate
			case "reverb":
				node.tail += 2 * SampleRate
			case "chorus":
				node.tail += SampleRate * 15 / 1000
			case "lowpass":
				node.tail += SampleRate / 50
			}
		}
		out.nodes = append(out.nodes, node)
		index[id] = i
	}
	for _, edge := range routes {
		from, to := index[edge.From], index[edge.To]
		out.edges = append(out.edges, mixEdge{from, to, out.nodes[to].align - out.nodes[from].outputLatency, edge.Gain, edge.Kind == "sidechain"})
	}
	out.master = index[project.MasterChannel]
	out.latency = out.nodes[out.master].outputLatency
	out.tail = min(10*SampleRate, out.nodes[out.master].tail)
	return out, nil
}

type Meter struct {
	Peak  float64 `json:"peak"`
	Hold  float64 `json:"hold"`
	Clips uint64  `json:"clips"`
}

func (m *Manager) ResetMixerMeters() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		m.engine.resetMeters()
	}
}

func (m *Manager) SetMixerOverride(projectID string, revision int64, target, parameter string, value float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lo, hi, ok := project.MixRange(parameter)
	if !ok || math.IsNaN(value) || value < lo || value > hi {
		return fmt.Errorf("自动化参数无效")
	}
	if m.engine == nil || !m.engine.status().Playing || m.score == nil || m.score.mix == nil || m.score.ID != projectID || m.score.Revision != revision || m.previewID != "" {
		return fmt.Errorf("请播放当前工程修订后写入自动化")
	}
	for i, n := range m.score.mix.nodes {
		if n.id == target && (parameter == "gain" || parameter == "pan") {
			m.engine.override(i, -1, parameter, value)
			return nil
		}
		for j, f := range n.effects {
			if f.id == target && (parameter == "amount" || parameter == "wet") {
				m.engine.override(i, j, parameter, value)
				return nil
			}
		}
	}
	return fmt.Errorf("播放快照没有此参数；请先提交效果并关闭旁通")
}

func (m *Manager) ClearMixerOverride() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		m.engine.override(-1, -1, "", 0)
	}
}
