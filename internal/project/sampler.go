package project

import (
	"fmt"
	"slices"
)

const MaxSamplerZones = 32

type Sampler struct {
	Zones []SampleZone `json:"zones"`
}

// Zones use inclusive note and velocity ranges. Matching zones are layered.
// Envelope times are milliseconds, sustain and gain are linear percentages.
type SampleZone struct {
	AssetID     string `json:"assetId"`
	Name        string `json:"name,omitempty"`
	Source      int64  `json:"source"`
	Frames      int64  `json:"frames"`
	Root        int    `json:"root"`
	Low         int    `json:"low"`
	High        int    `json:"high"`
	MinVelocity int    `json:"minVelocity"`
	MaxVelocity int    `json:"maxVelocity"`
	Attack      int    `json:"attackMs"`
	Decay       int    `json:"decayMs"`
	Sustain     int    `json:"sustain"`
	Release     int    `json:"releaseMs"`
	Gain        int    `json:"gain"`
	OneShot     bool   `json:"oneShot,omitempty"`
}

func (s *Sampler) Copy() *Sampler {
	if s == nil {
		return nil
	}
	return &Sampler{Zones: slices.Clone(s.Zones)}
}

func ValidateSampler(st *State, s *Sampler) error {
	if s == nil {
		return nil
	}
	if len(s.Zones) < 1 || len(s.Zones) > MaxSamplerZones {
		return fmt.Errorf("采样器须包含 1–%d 个区域", MaxSamplerZones)
	}
	for _, z := range s.Zones {
		a, ok := FindAudioAsset(st, z.AssetID)
		if !ok || z.Source < 0 || z.Source >= a.Frames || z.Frames < 1 || z.Frames > a.Frames-z.Source {
			return fmt.Errorf("采样区间或资产无效")
		}
		if z.Root < 0 || z.Root > 127 || z.Low < 0 || z.Low > z.High || z.High > 127 ||
			z.Low < z.Root-48 || z.High > z.Root+48 || z.MinVelocity < 1 || z.MinVelocity > z.MaxVelocity || z.MaxVelocity > 127 {
			return fmt.Errorf("音高或力度范围无效。区域最多偏离根音 48 半音")
		}
		if len([]rune(z.Name)) > MaxTitleRunes || z.Gain < 0 || z.Gain > 200 || z.Attack < 0 || z.Attack > 5000 ||
			z.Decay < 0 || z.Decay > 5000 || z.Sustain < 0 || z.Sustain > 100 || z.Release < 0 || z.Release > 5000 {
			return fmt.Errorf("采样区域名称、增益或包络无效")
		}
	}
	return nil
}
