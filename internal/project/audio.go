package project

import (
	"encoding/hex"
	"fmt"
	"slices"
)

const (
	AudioRate      = 48000
	MaxAudioFrames = 10 * 60 * AudioRate
	MaxAudioClips  = 256
	MaxAudioAssets = 256
)

type AudioDerivation struct {
	SourceID     string `json:"sourceId"`
	SourceDigest string `json:"sourceDigest"`
	Source       int64  `json:"source"`
	Frames       int64  `json:"frames"`
	Reverse      bool   `json:"reverse,omitempty"`
	Percent      int    `json:"percent"`
	Cents        int    `json:"cents"`
	Algorithm    string `json:"algorithm"`
}

type AudioAsset struct {
	TakeLoopFrames int64            `json:"takeLoopFrames,omitempty"`
	Derivation     *AudioDerivation `json:"derivation,omitempty"`
	TakeID         string           `json:"takeId,omitempty"`
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Digest         string           `json:"digest"`
	Frames         int64            `json:"frames"`
	Peaks          []int16          `json:"peaks,omitempty"`
}

type AudioClip struct {
	ID      string `json:"id"`
	AssetID string `json:"assetId"`
	Name    string `json:"name"`
	Start   int64  `json:"start"`
	Source  int64  `json:"source"`
	Frames  int64  `json:"frames"`
	FadeIn  int64  `json:"fadeIn"`
	FadeOut int64  `json:"fadeOut"`
	Gain    int    `json:"gain"` // percent, 0–200
	Reverse bool   `json:"reverse,omitempty"`
}

type AudioTrackHistory struct {
	TrackID string      `json:"trackId"`
	Clips   []AudioClip `json:"clips"`
}

func FindAudioAsset(st *State, id string) (AudioAsset, bool) {
	for _, asset := range st.AudioAssets {
		if asset.ID == id {
			return asset, true
		}
	}
	return AudioAsset{}, false
}

func ValidAudioDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func ValidateAudioAsset(a AudioAsset) error {
	if !ValidID(a.ID) || !ValidAudioDigest(a.Digest) || a.Frames < 1 || a.Frames > MaxAudioFrames ||
		len([]rune(a.Name)) > MaxTitleRunes || len(a.Peaks) > 512 {
		return fmt.Errorf("音频资产的标识、帧数或波形无效")
	}
	if a.TakeID != "" && !ValidID(a.TakeID) || a.TakeLoopFrames < 0 || a.TakeLoopFrames > MaxAudioFrames || a.TakeID == "" && a.TakeLoopFrames != 0 {
		return fmt.Errorf("录音来源标识无效")
	}
	if d := a.Derivation; d != nil {
		if !ValidID(d.SourceID) || d.SourceID == a.ID || !ValidAudioDigest(d.SourceDigest) || d.Source < 0 || d.Frames < 1 || d.Source > MaxAudioFrames-d.Frames ||
			d.Percent < 50 || d.Percent > 200 || d.Cents < -1200 || d.Cents > 1200 || d.Algorithm != "ffmpeg-rubberband" ||
			(d.Frames*int64(d.Percent)+50)/100 != a.Frames {
			return fmt.Errorf("派生音频参数无效")
		}
	}
	for _, p := range a.Peaks {
		if p < 0 {
			return fmt.Errorf("波形峰值无效")
		}
	}
	return nil
}

func ValidateAudioClip(c AudioClip, asset AudioAsset) error {
	if !ValidID(c.ID) || c.AssetID != asset.ID || len([]rune(c.Name)) > MaxTitleRunes ||
		c.Start < 0 || c.Start > MaxTick || c.Source < 0 || c.Source >= asset.Frames ||
		c.Frames < 1 || c.Frames > asset.Frames-c.Source || c.Gain < 0 || c.Gain > 200 ||
		c.FadeIn < 0 || c.FadeOut < 0 || c.FadeIn > c.Frames || c.FadeOut > c.Frames-c.FadeIn {
		return fmt.Errorf("音频片段的起点、源区间、增益或淡变无效")
	}
	return nil
}

func applyAudioOp(st *State, ids map[string]bool, op Op) (Op, []string, error) {
	inv := Op{Kind: "audio.restore"}
	if op.Kind == "audio.restore" {
		if op.InvAssets != nil {
			old := append([]AudioAsset{}, st.AudioAssets...)
			inv.InvAssets = &old
			st.AudioAssets = slices.Clone(*op.InvAssets)
		}
		for _, saved := range op.InvAudioTracks {
			ti, ok := findTrack(st, saved.TrackID)
			if !ok {
				return Op{}, nil, opError(op.Kind, "恢复轨道不存在")
			}
			t := &st.Tracks[ti]
			inv.InvAudioTracks = append(inv.InvAudioTracks, AudioTrackHistory{t.ID, slices.Clone(t.Audio)})
			t.Audio = slices.Clone(saved.Clips)
		}
		clear(ids)
		for id := range idSet(st) {
			ids[id] = true
		}
		return inv, nil, nil
	}
	if op.Kind == "audio.asset" {
		if op.AudioAsset == nil || len(st.AudioAssets) >= MaxAudioAssets {
			return Op{}, nil, opError(op.Kind, "缺少资产或资产数已达上限")
		}
		a := *op.AudioAsset
		if err := ValidateAudioAsset(a); err != nil {
			return Op{}, nil, err
		}
		if d := a.Derivation; d != nil {
			source, ok := FindAudioAsset(st, d.SourceID)
			if !ok || source.Digest != d.SourceDigest || d.Source > source.Frames-d.Frames {
				return Op{}, nil, fmt.Errorf("派生音频缺少对应的原资产")
			}
		}
		if ids[a.ID] {
			return Op{}, nil, opError(op.Kind, "资产 ID 已使用")
		}
		old := append([]AudioAsset{}, st.AudioAssets...)
		inv.InvAssets = &old
		a.Peaks = slices.Clone(a.Peaks)
		if a.Derivation != nil {
			d := *a.Derivation
			a.Derivation = &d
		}
		st.AudioAssets = append(st.AudioAssets, a)
		ids[a.ID] = true
		return inv, []string{a.ID}, nil
	}
	ti, ok := findTrack(st, op.TrackID)
	if !ok {
		return Op{}, nil, opError(op.Kind, "音轨不存在")
	}
	t := &st.Tracks[ti]
	inv.InvAudioTracks = []AudioTrackHistory{{t.ID, slices.Clone(t.Audio)}}
	if op.Kind == "audio.remove" {
		i := slices.IndexFunc(t.Audio, func(c AudioClip) bool { return c.ID == op.ClipID })
		if i < 0 {
			return Op{}, nil, opError(op.Kind, "音频片段不存在")
		}
		t.Audio = slices.Delete(t.Audio, i, i+1)
		delete(ids, op.ClipID)
		return inv, nil, nil
	}
	if op.AudioClip == nil {
		return Op{}, nil, opError(op.Kind, "缺少音频片段")
	}
	c := *op.AudioClip
	asset, ok := FindAudioAsset(st, c.AssetID)
	if !ok {
		return Op{}, nil, opError(op.Kind, "音频资产不存在")
	}
	if err := ValidateAudioClip(c, asset); err != nil {
		return Op{}, nil, err
	}
	if op.Kind == "audio.add" {
		count := 0
		for _, tr := range st.Tracks {
			count += len(tr.Audio)
		}
		if count >= MaxAudioClips || ids[c.ID] {
			return Op{}, nil, opError(op.Kind, "片段超出上限或 ID 已使用")
		}
		ids[c.ID] = true
		t.Audio = append(t.Audio, c)
		return inv, []string{c.ID}, nil
	}
	i := slices.IndexFunc(t.Audio, func(old AudioClip) bool { return old.ID == c.ID })
	if i < 0 {
		return Op{}, nil, opError(op.Kind, "音频片段不存在")
	}
	t.Audio[i] = c
	return inv, nil, nil
}
