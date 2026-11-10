package project

import (
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxPluginInstances = 16

// PluginState identifies an exact scanned binary without embedding its path.
type PluginState struct {
	Format     string            `json:"format"`
	Class      string            `json:"class"`
	SHA256     string            `json:"sha256"`
	Version    string            `json:"version,omitempty"`
	Vendor     string            `json:"vendor,omitempty"`
	Name       string            `json:"name"`
	State      []byte            `json:"state,omitempty"`
	Parameters []PluginParameter `json:"parameters,omitempty"`
}

type PluginParameter struct {
	ID      uint32  `json:"id"`
	Name    string  `json:"name"`
	Default float64 `json:"default"`
	Value   float64 `json:"value"`
}

func (p *PluginState) copy() *PluginState {
	if p == nil {
		return nil
	}
	out := *p
	out.State = slices.Clone(p.State)
	out.Parameters = slices.Clone(p.Parameters)
	return &out
}

func (p *PluginState) valid() bool {
	if p == nil || (p.Format != "clap" && p.Format != "vst3") || len(p.Class) == 0 ||
		len(p.Class) > 256 || strings.ContainsRune(p.Class, 0) || !utf8.ValidString(p.Class) ||
		p.Name == "" || len(p.Name) > 256 || len(p.Version) > 256 || len(p.Vendor) > 256 || len(p.State) > 1<<20 || len(p.Parameters) > 512 {
		return false
	}
	if hash, err := hex.DecodeString(p.SHA256); err != nil || len(hash) != 32 || p.SHA256 != strings.ToLower(p.SHA256) {
		return false
	}
	seen := make(map[uint32]bool, len(p.Parameters))
	for _, v := range p.Parameters {
		if seen[v.ID] || len(v.Name) > 56 || !mixValue(v.Value, 0, 1) || !mixValue(v.Default, 0, 1) {
			return false
		}
		seen[v.ID] = true
	}
	return true
}

func PluginParameterKey(id uint32) string {
	return "plugin:" + strconv.FormatUint(uint64(id), 10)
}

func pluginParameterID(key string) (uint32, bool) {
	raw, ok := strings.CutPrefix(key, "plugin:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, 32)
	return uint32(id), err == nil && PluginParameterKey(uint32(id)) == key
}

func (p *PluginState) hasParameter(id uint32) bool {
	return p != nil && slices.ContainsFunc(p.Parameters, func(v PluginParameter) bool { return v.ID == id })
}
