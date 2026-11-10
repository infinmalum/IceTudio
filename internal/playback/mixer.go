package playback

import (
	"fmt"
	"math"

	"icetudio/internal/pluginhost"
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
	plugin      *project.PluginState
	parameters  []mixParam
	initial     []float64
	worker      *pluginhost.Instance
	latency     int
}
type mixNode struct {
	id                                string
	gain, pan                         mixParam
	effects                           []mixFX
	instrument                        *mixFX
	sourceLatency                     int
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
	issues                []string
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
	solo := false
	for _, tr := range st.Tracks {
		solo = solo || tr.Solo
	}
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
		audible := true
		if t, ok := tracks[id]; ok {
			node.track = t
			audible = !st.Tracks[t].Muted && (!solo || st.Tracks[t].Solo)
		}
		if node.track >= 0 && audible && ch.FrozenAssetID == "" && ch.Instrument != nil {
			f := *ch.Instrument
			node.instrument = &mixFX{kind: 11, id: f.ID, plugin: f.Plugin}
			for _, p := range f.Plugin.Parameters {
				node.instrument.parameters = append(node.instrument.parameters, param(f.ID, project.PluginParameterKey(p.ID), ch.MacroValue(f.ID, project.PluginParameterKey(p.ID), p.Value)))
			}
		}
		for _, effect := range ch.Effects {
			if effect.Bypass || ch.FrozenAssetID != "" || (!audible && effect.Plugin != nil) {
				continue
			}
			kind := map[string]int{
				"eq": 1, "compressor": 2, "limiter": 3, "delay": 4, "reverb": 5,
				"chorus": 6, "lowpass": 7, "drive": 8, "gate": 9, "plugin": 10,
			}[effect.Kind]
			fx := mixFX{kind: kind, id: effect.ID, amount: param(effect.ID, "amount", ch.MacroValue(effect.ID, "amount", effect.Amount)),
				wet: param(effect.ID, "wet", ch.MacroValue(effect.ID, "wet", effect.Wet)), plugin: effect.Plugin}
			if effect.Plugin != nil {
				for _, p := range effect.Plugin.Parameters {
					fx.parameters = append(fx.parameters, param(effect.ID, project.PluginParameterKey(p.ID), ch.MacroValue(effect.ID, project.PluginParameterKey(p.ID), p.Value)))
				}
			}
			node.effects = append(node.effects, fx)
		}
		out.nodes = append(out.nodes, node)
		index[id] = i
	}
	for _, edge := range routes {
		from, to := index[edge.From], index[edge.To]
		out.edges = append(out.edges, mixEdge{from, to, 0, edge.Gain, edge.Kind == "sidechain"})
	}
	out.master = index[project.MasterChannel]
	out.compensate()
	return out, nil
}

func (out *mixPlan) compensate() {
	for i := range out.nodes {
		node := &out.nodes[i]
		node.sourceLatency, node.tail = 0, 0
		if node.instrument != nil {
			node.sourceLatency = node.instrument.latency
			if node.instrument.worker != nil {
				node.tail = SampleRate
			}
		}
		aligned := node.sourceLatency
		for _, e := range out.edges {
			if e.to == i {
				pred := out.nodes[e.from]
				aligned = max(aligned, pred.outputLatency)
				node.tail = max(node.tail, pred.tail)
			}
		}
		node.align = aligned - node.sourceLatency
		node.outputLatency = aligned
		for _, f := range node.effects {
			switch f.kind {
			case 3:
				node.outputLatency += 64
			case 4:
				node.tail += 4 * SampleRate
			case 5:
				node.tail += 2 * SampleRate
			case 6:
				node.tail += SampleRate * 15 / 1000
			case 7:
				node.tail += SampleRate / 50
			case 10:
				node.outputLatency += f.latency
				if f.worker != nil {
					node.tail += SampleRate
				}
			}
		}
	}
	for i := range out.edges {
		edge := &out.edges[i]
		target := out.nodes[edge.to]
		edge.delay = target.align + target.sourceLatency - out.nodes[edge.from].outputLatency
	}
	out.latency = out.nodes[out.master].outputLatency
	out.tail = min(10*SampleRate, out.nodes[out.master].tail)
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
	pluginOverride := func(f *mixFX, i, slot int) bool {
		if f == nil || f.id != target || f.plugin == nil {
			return false
		}
		for _, p := range f.plugin.Parameters {
			if project.PluginParameterKey(p.ID) == parameter {
				return m.engine.pluginOverride(i, slot, p.ID, value)
			}
		}
		return false
	}
	for i, n := range m.score.mix.nodes {
		if n.id == target && (parameter == "gain" || parameter == "pan") {
			m.engine.override(i, -1, parameter, value)
			return nil
		}
		if pluginOverride(n.instrument, i, -1) {
			return nil
		}
		for j, f := range n.effects {
			if pluginOverride(&f, i, j) {
				return nil
			}
			if f.id == target && ((parameter == "amount" && f.plugin == nil) || parameter == "wet") {
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
