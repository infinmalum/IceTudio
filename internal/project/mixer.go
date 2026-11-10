package project

import (
	"fmt"
	"math"
	"slices"
)

const MasterChannel = "@master"

type Mixer struct {
	Channels       []MixChannel `json:"channels,omitempty"`
	Routes         []MixRoute   `json:"routes,omitempty"`
	Lanes          []Automation `json:"lanes,omitempty"`
	RackPresets    []RackPreset `json:"rackPresets,omitempty"`
	FavoriteSounds []string     `json:"favoriteSounds,omitempty"`
	Presets        []MixPreset  `json:"presets,omitempty"`
}

type MixChannel struct {
	ID            string      `json:"id"`
	BusName       string      `json:"busName,omitempty"`
	Gain          float64     `json:"gain"` // linear amplitude, 1 = unity
	Pan           float64     `json:"pan"`  // stereo balance, -1..1
	Rack          *MixRack    `json:"rack,omitempty"`
	Synth         *SynthPatch `json:"synth,omitempty"`
	FrozenAssetID string      `json:"frozenAssetId,omitempty"`
	Instrument    *MixEffect  `json:"instrument,omitempty"`
	Effects       []MixEffect `json:"effects,omitempty"`
}

type MixEffect struct {
	ID     string       `json:"id"`
	Kind   string       `json:"kind"`
	Amount float64      `json:"amount"`
	Wet    float64      `json:"wet"`
	Bypass bool         `json:"bypass,omitempty"`
	Plugin *PluginState `json:"plugin,omitempty"`
}

type MixPreset struct {
	Name   string    `json:"name"`
	Effect MixEffect `json:"effect"` // no instance ID
}

type MixRoute struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Kind string  `json:"kind"` // output | send | sidechain
	Gain float64 `json:"gain"`
}

type Automation struct {
	ID        string            `json:"id"`
	Target    string            `json:"target"` // channel or effect instance ID
	Parameter string            `json:"parameter"`
	Points    []AutomationPoint `json:"points"`
}

type AutomationPoint struct {
	Tick  int64   `json:"tick"`
	Value float64 `json:"value"`
	Step  bool    `json:"step,omitempty"` // hold until the next point
}

func (m Mixer) Channel(id string) MixChannel {
	for _, ch := range m.Channels {
		if ch.ID == id {
			return ch
		}
	}
	return MixChannel{ID: id, Gain: 1}
}

func (m *Mixer) SetChannel(ch MixChannel) {
	for i := range m.Channels {
		if m.Channels[i].ID == ch.ID {
			m.Channels[i] = ch
			return
		}
	}
	m.Channels = append(m.Channels, ch)
}

func (c MixChannel) copy() MixChannel {
	c.Rack = c.Rack.Copy()
	if c.Synth != nil {
		patch := *c.Synth
		c.Synth = &patch
	}
	c.Effects = slices.Clone(c.Effects)
	for j := range c.Effects {
		c.Effects[j].Plugin = c.Effects[j].Plugin.copy()
	}
	if c.Instrument != nil {
		f := *c.Instrument
		f.Plugin = f.Plugin.copy()
		c.Instrument = &f
	}
	return c
}

func (m Mixer) Copy() Mixer {
	m.Channels = slices.Clone(m.Channels)
	for i := range m.Channels {
		m.Channels[i] = m.Channels[i].copy()
	}
	m.RackPresets = slices.Clone(m.RackPresets)
	for i := range m.RackPresets {
		m.RackPresets[i].Chain = m.RackPresets[i].Chain.copy()
	}
	m.Routes = slices.Clone(m.Routes)
	m.Lanes = slices.Clone(m.Lanes)
	for i := range m.Lanes {
		m.Lanes[i].Points = slices.Clone(m.Lanes[i].Points)
	}
	m.FavoriteSounds = slices.Clone(m.FavoriteSounds)
	m.Presets = slices.Clone(m.Presets)
	for i := range m.Presets {
		m.Presets[i].Effect.Plugin = m.Presets[i].Effect.Plugin.copy()
	}
	return m
}

func (m Mixer) IDs() []string {
	var ids []string
	for _, c := range m.Channels {
		if c.BusName != "" {
			ids = append(ids, c.ID)
		}
		if c.Instrument != nil {
			ids = append(ids, c.Instrument.ID)
		}
		for _, f := range c.Effects {
			ids = append(ids, f.ID)
		}
	}
	for _, l := range m.Lanes {
		ids = append(ids, l.ID)
	}
	return ids
}

func (m *Mixer) RemoveChannel(id string) {
	c := m.Channel(id)
	m.Channels = slices.DeleteFunc(m.Channels, func(c MixChannel) bool { return c.ID == id })
	m.Routes = slices.DeleteFunc(m.Routes, func(r MixRoute) bool { return r.From == id || r.To == id })
	m.Lanes = slices.DeleteFunc(m.Lanes, func(l Automation) bool {
		return l.Target == id || (c.Instrument != nil && c.Instrument.ID == l.Target) || slices.ContainsFunc(c.Effects, func(f MixEffect) bool { return f.ID == l.Target })
	})
}

func MixRange(parameter string) (lo, hi float64, ok bool) {
	switch parameter {
	case "gain":
		return 0, 2, true
	case "pan":
		return -1, 1, true
	case "amount", "wet":
		return 0, 1, true
	default:
		if _, ok := pluginParameterID(parameter); ok {
			return 0, 1, true
		}
		return 0, 0, false
	}
}

func mixValue(v, lo, hi float64) bool { return !math.IsNaN(v) && v >= lo && v <= hi }

func validateEffect(f MixEffect) bool {
	if f.Kind == "plugin" {
		return f.Plugin.valid() && mixValue(f.Wet, 0, 1) && f.Amount == 0
	}
	return f.Plugin == nil && slices.Contains([]string{"eq", "compressor", "limiter", "delay", "reverb", "chorus", "lowpass", "drive", "gate"}, f.Kind) &&
		mixValue(f.Amount, 0, 1) && mixValue(f.Wet, 0, 1)
}

// MixOrder returns the canonical graph with implicit output-to-Master edges.
func MixOrder(st *State, m Mixer) ([]string, []MixRoute, error) {
	if len(m.Channels) > MaxTracks+17 || len(m.Routes) > 192 || len(m.Lanes) > 128 || len(m.Presets) > 32 || len(m.RackPresets) > 16 {
		return nil, nil, fmt.Errorf("混音图超出资源上限")
	}
	nodes := []string{MasterChannel}
	channels := map[string]bool{MasterChannel: true}
	for _, t := range st.Tracks {
		nodes = append(nodes, t.ID)
		channels[t.ID] = true
	}
	seen, effects := map[string]bool{}, map[string]bool{}
	plugins := map[string]*PluginState{}
	pluginBytes := 0
	buses, fxCount := 0, 0
	for _, c := range m.Channels {
		if seen[c.ID] || len(c.Effects) > 8 || !mixValue(c.Gain, 0, 2) || !mixValue(c.Pan, -1, 1) {
			return nil, nil, fmt.Errorf("通道重复、参数无效或效果槽超过 8 个")
		}
		seen[c.ID] = true
		if c.BusName != "" {
			buses++
			if buses > 16 || len([]rune(c.BusName)) > 60 || channels[c.ID] || !ValidID(c.ID) {
				return nil, nil, fmt.Errorf("总线名称或身份无效，最多 16 条总线")
			}
			nodes = append(nodes, c.ID)
			channels[c.ID] = true
		}
		if !channels[c.ID] {
			return nil, nil, fmt.Errorf("混音通道没有对应轨道")
		}
		if err := c.validateRack(c.ID != MasterChannel && c.BusName == ""); err != nil {
			return nil, nil, err
		}
		if c.Synth != nil && (c.ID == MasterChannel || c.BusName != "" || c.Instrument != nil || !c.Synth.Valid()) {
			return nil, nil, fmt.Errorf("轨道内置音色无效")
		}
		if c.FrozenAssetID != "" {
			_, ok := FindAudioAsset(st, c.FrozenAssetID)
			if !ok || c.ID == MasterChannel || c.BusName != "" {
				return nil, nil, fmt.Errorf("冻结轨缺少对应音频资产")
			}
		}
		if c.Instrument != nil {
			f := c.Instrument
			if c.ID == MasterChannel || c.BusName != "" || f.Bypass || f.Wet != 1 ||
				!ValidID(f.ID) || effects[f.ID] || f.Kind != "plugin" || !validateEffect(*f) {
				return nil, nil, fmt.Errorf("轨道插件乐器无效")
			}
			effects[f.ID], plugins[f.ID] = true, f.Plugin
			pluginBytes += len(f.Plugin.State)
		}
		fxCount += len(c.Effects)
		if fxCount > 128 {
			return nil, nil, fmt.Errorf("工程最多 128 个效果实例")
		}
		for _, f := range c.Effects {
			if !ValidID(f.ID) || effects[f.ID] || !validateEffect(f) {
				return nil, nil, fmt.Errorf("效果参数或身份无效")
			}
			effects[f.ID] = true
			if f.Plugin != nil {
				plugins[f.ID] = f.Plugin
				pluginBytes += len(f.Plugin.State)
			}
		}
	}
	favoriteSounds := map[string]bool{}
	for _, id := range m.FavoriteSounds {
		if _, ok := FindSoundPreset(id); !ok || favoriteSounds[id] {
			return nil, nil, fmt.Errorf("收藏音色 ID 无效或重复")
		}
		favoriteSounds[id] = true
	}
	presetNames := map[string]bool{}
	for _, p := range m.Presets {
		if p.Name == "" || len([]rune(p.Name)) > 60 || presetNames[p.Name] || p.Effect.ID != "" || !validateEffect(p.Effect) {
			return nil, nil, fmt.Errorf("效果预设无效或重名")
		}
		presetNames[p.Name] = true
		if p.Effect.Plugin != nil {
			pluginBytes += len(p.Effect.Plugin.State)
		}
	}
	rackNames := map[string]bool{}
	for _, p := range m.RackPresets {
		n, err := p.validate()
		if err != nil || rackNames[p.Name] {
			return nil, nil, fmt.Errorf("组合预设无效或重名")
		}
		rackNames[p.Name] = true
		pluginBytes += n
	}
	if len(plugins) > MaxPluginInstances || pluginBytes > 8<<20 {
		return nil, nil, fmt.Errorf("插件实例或状态总量超出限制")
	}
	lanes := map[[2]string]bool{}
	points := 0
	for _, l := range m.Lanes {
		key := [2]string{l.Target, l.Parameter}
		lo, hi, ok := MixRange(l.Parameter)
		parameterID, isPlugin := pluginParameterID(l.Parameter)
		isEffect := l.Parameter == "amount" || l.Parameter == "wet"
		validTarget := channels[l.Target]
		if isEffect {
			validTarget = effects[l.Target] && (l.Parameter == "wet" || plugins[l.Target] == nil)
		}
		if isPlugin {
			validTarget = plugins[l.Target].hasParameter(parameterID)
		}
		if !ok || !ValidID(l.ID) || lanes[key] || len(l.Points) < 1 ||
			!validTarget {
			return nil, nil, fmt.Errorf("自动化目标无效或重复")
		}
		lanes[key] = true
		previous := int64(-1)
		points += len(l.Points)
		if points > 16384 {
			return nil, nil, fmt.Errorf("自动化点数超过上限")
		}
		for _, p := range l.Points {
			if p.Tick <= previous || p.Tick > MaxTick || !mixValue(p.Value, lo, hi) {
				return nil, nil, fmt.Errorf("自动化点必须严格按 tick 排序且值在参数范围内")
			}
			previous = p.Tick
		}
	}
	routes := slices.Clone(m.Routes)
	output, sides := map[string]bool{}, map[string]bool{}
	edges := map[[3]string]bool{}
	for _, r := range routes {
		key := [3]string{r.From, r.To, r.Kind}
		if r.From == MasterChannel || r.From == r.To || !channels[r.From] || !channels[r.To] || edges[key] || !mixValue(r.Gain, 0, 2) {
			return nil, nil, fmt.Errorf("路由端点、增益无效或路由重复")
		}
		edges[key] = true
		switch r.Kind {
		case "output":
			if output[r.From] || r.Gain != 1 || (r.To != MasterChannel && m.Channel(r.To).BusName == "") {
				return nil, nil, fmt.Errorf("每通道只有一个单位增益主输出，目标须为总线或 Master")
			}
			output[r.From] = true
		case "send":
			if m.Channel(r.To).BusName == "" {
				return nil, nil, fmt.Errorf("发送目标须为总线")
			}
		case "sidechain":
			if sides[r.To] || !slices.ContainsFunc(m.Channel(r.To).Effects, func(f MixEffect) bool { return f.Kind == "compressor" }) {
				return nil, nil, fmt.Errorf("侧链目标须有压缩器且只接受一个输入")
			}
			sides[r.To] = true
		default:
			return nil, nil, fmt.Errorf("未知混音路由")
		}
	}
	for _, n := range nodes[1:] {
		if !output[n] {
			routes = append(routes, MixRoute{From: n, To: MasterChannel, Kind: "output", Gain: 1})
		}
	}
	incoming := map[string]int{}
	outgoing := map[string][]string{}
	for _, r := range routes {
		incoming[r.To]++
		outgoing[r.From] = append(outgoing[r.From], r.To)
	}
	order := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if incoming[n] == 0 {
			order = append(order, n)
		}
	}
	for i := 0; i < len(order); i++ {
		for _, dest := range outgoing[order[i]] {
			incoming[dest]--
			if incoming[dest] == 0 {
				order = append(order, dest)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, nil, fmt.Errorf("混音路由形成反馈环")
	}
	return order, routes, nil
}

func replaceMixer(st *State, ids map[string]bool, next Mixer) ([]string, error) {
	if _, _, err := MixOrder(st, next); err != nil {
		return nil, err
	}
	var old []string
	if st.Mixer != nil {
		old = st.Mixer.IDs()
	}
	oldIDs := map[string]bool{}
	for _, id := range old {
		oldIDs[id] = true
		delete(ids, id)
	}
	var created []string
	for _, id := range next.IDs() {
		if ids[id] {
			return nil, fmt.Errorf("混音对象 ID 重复：%s", id)
		}
		ids[id] = true
		if !oldIDs[id] {
			created = append(created, id)
		}
	}
	if len(next.Channels)+len(next.Lanes)+len(next.Routes)+len(next.Presets)+len(next.FavoriteSounds)+len(next.RackPresets) == 0 {
		st.Mixer = nil
	} else {
		st.Mixer = &next
	}
	return created, nil
}
