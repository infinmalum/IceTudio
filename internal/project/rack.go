package project

import (
	"fmt"
	"slices"
	"strings"
)

const SynthMacroTarget = "@synth"

type MixRack struct {
	Transpose int        `json:"transpose"`
	Velocity  float64    `json:"velocity"`
	Macros    []MixMacro `json:"macros,omitempty"`
}

type MixMacro struct {
	Name     string         `json:"name"`
	Value    float64        `json:"value"`
	Bindings []MacroBinding `json:"bindings"`
}

type MacroBinding struct {
	Target    string  `json:"target"`
	Parameter string  `json:"parameter"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
}

type RackPreset struct {
	Name  string     `json:"name"`
	Chain MixChannel `json:"chain"`
}

func (r *MixRack) Copy() *MixRack {
	if r == nil {
		return nil
	}
	out := *r
	out.Macros = slices.Clone(r.Macros)
	for i := range out.Macros {
		out.Macros[i].Bindings = slices.Clone(out.Macros[i].Bindings)
	}
	return &out
}

func (ch MixChannel) MacroValue(target, parameter string, base float64) float64 {
	if ch.Rack != nil {
		for _, macro := range ch.Rack.Macros {
			for _, bind := range macro.Bindings {
				if bind.Target == target && bind.Parameter == parameter {
					return bind.Min + (bind.Max-bind.Min)*macro.Value
				}
			}
		}
	}
	return base
}

func (ch MixChannel) parameterBounds(target, parameter string) (float64, float64, bool) {
	if parameter == "tone" || parameter == "release" {
		return 0, 1, target == SynthMacroTarget && ch.Synth != nil
	}
	var fx *MixEffect
	for i := range ch.Effects {
		if ch.Effects[i].ID == target {
			fx = &ch.Effects[i]
			break
		}
	}
	if ch.Instrument != nil && ch.Instrument.ID == target {
		fx = ch.Instrument
	}
	if fx == nil {
		return 0, 0, false
	}
	if id, ok := pluginParameterID(parameter); ok {
		return 0, 1, fx.Plugin.hasParameter(id)
	}
	return 0, 1, (parameter == "wet" && fx != ch.Instrument) || (parameter == "amount" && fx.Plugin == nil)
}

func (ch MixChannel) validateRack(track bool) error {
	rack := ch.Rack
	if rack == nil {
		return nil
	}
	if rack.Transpose < -48 || rack.Transpose > 48 || !mixValue(rack.Velocity, 0.25, 2) ||
		!track && (rack.Transpose != 0 || rack.Velocity != 1) || len(rack.Macros) > 8 {
		return fmt.Errorf("组合的 MIDI 或宏参数无效")
	}
	names := map[string]bool{}
	targets := map[[2]string]bool{}
	for _, macro := range rack.Macros {
		if strings.TrimSpace(macro.Name) == "" || len([]rune(macro.Name)) > 40 || names[macro.Name] ||
			!mixValue(macro.Value, 0, 1) || len(macro.Bindings) > 8 {
			return fmt.Errorf("宏名称、数量或取值无效")
		}
		names[macro.Name] = true
		for _, b := range macro.Bindings {
			lo, hi, ok := ch.parameterBounds(b.Target, b.Parameter)
			key := [2]string{b.Target, b.Parameter}
			if !ok || targets[key] || !mixValue(b.Min, lo, hi) || !mixValue(b.Max, lo, hi) {
				return fmt.Errorf("宏目标重复、缺失或映射范围无效")
			}
			targets[key] = true
		}
	}
	return nil
}

// RemoveMacroTarget removes orphaned links when their processor is removed.
func (ch *MixChannel) RemoveMacroTarget(id string) {
	ch.Rack = ch.Rack.Copy()
	if ch.Rack == nil {
		return
	}
	for i := range ch.Rack.Macros {
		m := &ch.Rack.Macros[i]
		m.Bindings = slices.DeleteFunc(m.Bindings, func(b MacroBinding) bool { return b.Target == id })
	}
}

func (ch MixChannel) SaveRack(name string) RackPreset {
	ch = ch.copy()
	ch.ID, ch.BusName, ch.FrozenAssetID = "", "", ""
	ch.Gain, ch.Pan = 1, 0
	return RackPreset{Name: name, Chain: ch}
}

// LoadRack is additive to an empty channel chain. Remap slot IDs so neither
// automation nor bindings can silently reach an existing processor elsewhere.
func (ch MixChannel) LoadRack(p RackPreset) (MixChannel, error) {
	if ch.Instrument != nil || ch.Synth != nil || len(ch.Effects) != 0 || ch.Rack != nil || ch.FrozenAssetID != "" {
		return ch, fmt.Errorf("请在空通道载入组合预设")
	}
	out := p.Chain.copy()
	out.ID, out.BusName, out.Gain, out.Pan = ch.ID, ch.BusName, ch.Gain, ch.Pan
	remap := map[string]string{}
	if out.Instrument != nil {
		old := out.Instrument.ID
		out.Instrument.ID = NewID("inst")
		remap[old] = out.Instrument.ID
	}
	for i := range out.Effects {
		old := out.Effects[i].ID
		out.Effects[i].ID = NewID("fx")
		remap[old] = out.Effects[i].ID
	}
	if out.Rack != nil {
		for i := range out.Rack.Macros {
			for j := range out.Rack.Macros[i].Bindings {
				b := &out.Rack.Macros[i].Bindings[j]
				if id, ok := remap[b.Target]; ok {
					b.Target = id
				}
			}
		}
	}
	return out, nil
}

func (p RackPreset) validate() (int, error) {
	ch := p.Chain
	if strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 60 ||
		ch.ID != "" || ch.BusName != "" || ch.FrozenAssetID != "" || ch.Gain != 1 || ch.Pan != 0 ||
		len(ch.Effects) > 8 || (ch.Synth != nil && (!ch.Synth.Valid() || ch.Instrument != nil)) {
		return 0, fmt.Errorf("组合预设无效")
	}
	fx := slices.Clone(ch.Effects)
	if ch.Instrument != nil {
		f := *ch.Instrument
		if f.Kind != "plugin" || f.Bypass || f.Wet != 1 {
			return 0, fmt.Errorf("组合预设乐器无效")
		}
		fx = append(fx, f)
	}
	ids := map[string]bool{}
	bytes := 0
	for _, f := range fx {
		if !ValidID(f.ID) || ids[f.ID] || !validateEffect(f) {
			return 0, fmt.Errorf("组合预设槽位无效")
		}
		ids[f.ID] = true
		if f.Plugin != nil {
			bytes += len(f.Plugin.State)
		}
	}
	return bytes, ch.validateRack(true)
}
