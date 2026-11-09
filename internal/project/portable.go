package project

import (
	"fmt"
	"reflect"
	"slices"
)

// ValidatePortable checks the complete portable project before publication.
func ValidatePortable(st *State) error {
	if st.PPQ < 1 || st.PPQ > MaxPPQ || len(st.Tracks) > MaxTracks || len(st.AudioAssets) > MaxAudioAssets || len([]rune(st.Title)) > MaxTitleRunes {
		return fmt.Errorf("工程资源数量或时间分辨率无效")
	}
	t, err := normalizeTempo(slices.Clone(st.Tempo))
	if err != nil || !slices.Equal(t, st.Tempo) {
		return fmt.Errorf("工程速度图无效")
	}
	m, err := normalizeMeter(slices.Clone(st.Meter))
	if err != nil || !slices.Equal(m, st.Meter) {
		return fmt.Errorf("工程拍号图无效")
	}
	ids := map[string]bool{}
	claim := func(id string) error {
		if !ValidID(id) || ids[id] {
			return fmt.Errorf("工程对象 ID 无效或重复")
		}
		ids[id] = true
		return nil
	}
	assets := map[string]AudioAsset{}
	for _, a := range st.AudioAssets {
		if err := claim(a.ID); err != nil {
			return err
		}
		if err := ValidateAudioAsset(a); err != nil {
			return err
		}
		if d := a.Derivation; d != nil {
			source, ok := assets[d.SourceID]
			if !ok || source.Digest != d.SourceDigest || d.Source > source.Frames-d.Frames {
				return fmt.Errorf("资源包缺少派生音频的原资产")
			}
		}
		assets[a.ID] = a
	}
	notes, rawBytes, audioCount := 0, 0, 0
	validClip := func(c Clip, body bool) error {
		if c.Start < 0 || c.Start > MaxTick || c.Length < 1 || c.Length > MaxTick || !ValidColor(c.Color) {
			return fmt.Errorf("片段时间或颜色无效")
		}
		if err := clipFitCheck("导入", &c); err != nil {
			return err
		}
		for _, n := range c.Notes {
			if !body {
				if err := claim(n.ID); err != nil {
					return err
				}
			} else if n.ID != "" {
				return fmt.Errorf("Pattern 源音符身份无效")
			}
			if err := validateNoteValues("导入", NewNote(n)); err != nil {
				return err
			}
			if n.Duration > c.Length-n.Start {
				return fmt.Errorf("音符超出片段")
			}
			notes++
		}
		for _, e := range c.Controls {
			if !body {
				if err := claim(e.ID); err != nil {
					return err
				}
			} else if e.ID != "" {
				return fmt.Errorf("Pattern 源控制身份无效")
			}
			if err := validateEventValues("导入", EventSpec(e)); err != nil {
				return err
			}
		}
		for _, r := range c.Raw {
			if !body {
				if err := claim(r.ID); err != nil {
					return err
				}
			} else if r.ID != "" {
				return fmt.Errorf("Pattern 源事件身份无效")
			}
			if len(r.Data) == 0 || len(r.Data) > 1<<20 || r.Data[0] < 0x80 {
				return fmt.Errorf("原始 MIDI 事件无效")
			}
			rawBytes += len(r.Data)
		}
		return nil
	}
	patterns := map[string]Clip{}
	for _, p := range st.Patterns {
		if err := claim(p.ID); err != nil {
			return err
		}
		if p.Body.ID != "" || p.Body.GroupID != "" || p.Body.PatternID != "" || p.Body.Start != 0 {
			return fmt.Errorf("Pattern 资源无效")
		}
		if err := validClip(p.Body, true); err != nil {
			return err
		}
		patterns[p.ID] = p.Body
	}
	groups := map[string]bool{}
	patternCount := map[string]int{}
	for _, tr := range st.Tracks {
		if err := ValidateSampler(st, tr.Sampler); err != nil {
			return err
		}
		if err := claim(tr.ID); err != nil {
			return err
		}
		if err := validateTrackValues("导入", tr.Channel, tr.Program); err != nil {
			return err
		}
		if !ValidColor(tr.Color) {
			return fmt.Errorf("音轨颜色无效")
		}
		for _, a := range tr.Audio {
			if err := claim(a.ID); err != nil {
				return err
			}
			if err := ValidateAudioClip(a, assets[a.AssetID]); err != nil {
				return err
			}
			audioCount++
		}
		for _, c := range tr.Clips {
			if err := claim(c.ID); err != nil {
				return err
			}
			if err := validClip(c, false); err != nil {
				return err
			}
			if c.GroupID != "" {
				groups[c.GroupID] = true
			}
			if c.PatternID != "" {
				p, ok := patterns[c.PatternID]
				if !ok || !reflect.DeepEqual(patternBody(c), p) {
					return fmt.Errorf("共享实例与 Pattern 内容不一致")
				}
				patternCount[c.PatternID]++
				if patternCount[c.PatternID] > 256 {
					return fmt.Errorf("Pattern 实例超过上限")
				}
			}
		}
	}
	for id := range groups {
		if err := claim(id); err != nil {
			return err
		}
	}
	if st.Mixer != nil {
		if _, _, err := MixOrder(st, *st.Mixer); err != nil {
			return err
		}
		for _, id := range st.Mixer.IDs() {
			if err := claim(id); err != nil {
				return err
			}
		}
	}
	if notes > MaxNotes*2 || rawBytes > MaxRawBytes*2 || audioCount > MaxAudioClips {
		return fmt.Errorf("工程资源超过上限")
	}
	return nil
}
