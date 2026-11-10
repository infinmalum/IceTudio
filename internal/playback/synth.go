package playback

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"

	"icetudio/internal/project"
)

const tableFrames = 1024
const stringPeriod = 109

func synthWave(p project.SynthPatch) []int16 {
	if p.Preset == "modeled-string" {
		// Karplus–Strong: excite a tuned delay line with deterministic
		// noise and feed a low-pass-filtered wave back into the string.
		var delay [stringPeriod]float64
		seed := uint32(0x12345678)
		for i := range delay {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			delay[i] = 1.4*float64(seed)/float64(math.MaxUint32) - .7
		}
		out := make([]int16, SampleRate*4)
		for i := 0; i < len(out)/2; i++ {
			k, next := i%len(delay), (i+1)%len(delay)
			v := delay[k]
			delay[k] = (.5*v + .5*delay[next]) * (.969 + .028*p.Tone)
			out[i*2] = int16(v * 14000)
			out[i*2+1] = out[i*2]
		}
		return out
	}
	out := make([]int16, tableFrames*2)
	for i := 0; i < tableFrames; i++ {
		phase := 2 * math.Pi * float64(i) / tableFrames
		var v float64
		if p.Preset == "fm-bell" {
			v = math.Sin(phase + (0.25+5*p.Tone)*math.Sin(3*phase))
		} else {
			// A filtered saw/square table. The tone macro adjusts the
			// harmonic rolloff without discontinuous oscillator resets.
			for h := 1; h <= 24; h++ {
				if p.Preset == "hollow-square" && h%2 == 0 {
					continue
				}
				rolloff := 1 / (1 + math.Pow(float64(h)/(1+15*p.Tone), 4))
				v += math.Sin(float64(h)*phase) * rolloff / float64(h)
			}
			v *= .65
		}
		out[i*2] = int16(max(-1, min(1, v)) * 14500)
		out[i*2+1] = out[i*2]
	}
	return out
}

func synthVoice(p project.SynthPatch, pitch, velocity int, gate int64) sampleVoice {
	frequency := 440 * math.Exp2(float64(pitch-69)/12)
	rate := frequency * tableFrames / SampleRate
	loop := p.Preset != "modeled-string"
	if !loop {
		rate = frequency * (stringPeriod - .5) / SampleRate
	}
	return sampleVoice{loop: loop, rate: rate, attack: 96, decay: 6400, gate: gate,
		release: int64(1200 + 36000*p.Release), sustain: .7, velocity: float64(velocity) / 127}
}

func compileSynthNote(score *Score, track int, patch project.SynthPatch, n project.Note, tick int64) error {
	start := score.Timeline.Frame(float64(tick + n.Start))
	gate := max(1, score.Timeline.Frame(float64(tick+n.Start+n.Duration))-start)
	v := synthVoice(patch, n.Pitch, n.Velocity, gate)
	key := fmt.Sprintf("builtin:%s:%x", patch.Preset, math.Float64bits(patch.Tone))
	if score.waveforms == nil {
		score.waveforms = make(map[string][]int16)
	}
	samples := score.waveforms[key]
	if samples == nil {
		samples = synthWave(patch)
		score.waveforms[key] = samples
	}
	frames := int64(len(samples) / 2)
	length := gate + v.release
	if !v.loop {
		length = min(length, int64(math.Ceil(float64(frames)/v.rate)))
	}
	if start+length > MaxFrames || len(score.Audio) >= maxSamplerVoices {
		return fmt.Errorf("合成器声部数量或时长超限")
	}
	score.Audio = append(score.Audio, audioRegion{Track: track, Frame: start, Length: length, Voice: &v,
		Clip:  project.AudioClip{Frames: frames, Gain: 75},
		Asset: project.AudioAsset{Digest: key, Frames: frames}, Samples: samples})
	score.EndFrame = max(score.EndFrame, start+length)
	return nil
}

// SynthPreview uses the same source and envelope as project playback.
func SynthPreview(ctx context.Context, p project.SynthPatch) ([]byte, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("内置音色无效")
	}
	samples := synthWave(p)
	v := synthVoice(p, 60, 100, SampleRate/3)
	frames := v.gate + v.release
	out := make([]byte, frames*4)
	for i := int64(0); i < frames; i++ {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pos := float64(i) * v.rate
		if v.loop {
			pos = math.Mod(pos, float64(len(samples)/2))
		}
		k := int(pos) * 2
		if k >= len(samples) {
			break
		}
		next := min(k+2, len(samples)-2)
		if v.loop && k+2 == len(samples) {
			next = 0
		}
		gain := v.velocity * .75
		held := min(i, v.gate)
		if held < v.attack {
			gain *= float64(held) / float64(v.attack)
		} else if held-v.attack < v.decay {
			gain *= 1 - (1-v.sustain)*float64(held-v.attack)/float64(v.decay)
		} else {
			gain *= v.sustain
		}
		if i >= v.gate {
			gain *= 1 - float64(i-v.gate)/float64(v.release)
		}
		value := uint16(int16((float64(samples[k]) + (float64(samples[next])-float64(samples[k]))*(pos-math.Floor(pos))) * gain))
		binary.LittleEndian.PutUint16(out[i*4:], value)
		binary.LittleEndian.PutUint16(out[i*4+2:], value)
	}
	return out, nil
}
