package audioasset

import (
	"encoding/binary"
	"math"
	"slices"
)

const AnalysisFrames = 30 * 48000

type Analysis struct {
	BPM     int
	Key     string
	MIDI    float64 // robust average over voiced windows; valid when Pitched
	Pitched bool
}

// Onsets detects transient energy rises with a local floor and 80 ms
// retrigger guard. Frame zero is an implicit boundary, not a detected event.
func Onsets(samples []int16) []int64 {
	const hop = 480
	var out []int64
	previous, floor := 0.0, 0.0
	last := -8
	for i := 0; i+hop*2 <= len(samples); i += hop * 2 {
		var power float64
		for j := i; j < i+hop*2; j++ {
			v := float64(samples[j]) / 32768
			power += v * v
		}
		energy := math.Sqrt(power / (hop * 2))
		if energy > .015 && energy > previous*1.7 && energy > floor*1.5 && i/(hop*2)-last >= 8 {
			out = append(out, int64(i/2))
			last = i / (hop * 2)
		}
		previous = energy
		floor = .92*floor + .08*energy
	}
	return out
}

func Analyze(samples []int16) Analysis {
	samples = samples[:min(len(samples), AnalysisFrames*2)]
	onsets := Onsets(samples)
	out := Analysis{BPM: estimateTempo(onsets)}
	// Sparse voiced windows keep cost bounded even on large loops.
	var pitched []float64
	var chroma [12]float64
	const window, hop = 4800, 12000
	for start := 0; start+window*2 <= len(samples); start += hop * 2 {
		if note, ok := windowPitch(samples[start : start+window*2]); ok {
			pitched = append(pitched, note)
			chroma[int(math.Round(note))%12]++
		}
	}
	if len(pitched) > 0 {
		slices.Sort(pitched)
		middle := pitched[len(pitched)/2]
		deviations := make([]float64, len(pitched))
		for i, v := range pitched {
			deviations[i] = math.Abs(v - middle)
		}
		slices.Sort(deviations)
		out.MIDI, out.Pitched = middle, deviations[len(deviations)/2] < .40
		out.Key = estimateKey(chroma, len(pitched))
	}
	return out
}

func estimateTempo(onsets []int64) int {
	if len(onsets) < 4 {
		return 0
	}
	const hop = 480
	grid := make([]float64, onsets[len(onsets)-1]/hop+1)
	for _, t := range onsets {
		grid[t/hop] = 1
	}
	best, score := 0, 0.0
	for lag := 30; lag <= 100; lag++ {
		n := 0.0
		for _, t := range onsets {
			i := int(t/hop) - lag
			if i >= 1 && i+1 < len(grid) {
				n += max(grid[i], .65*max(grid[i-1], grid[i+1]))
			}
		}
		if n > score {
			best, score = lag, n
		}
	}
	if score < float64(len(onsets))*.45 || best == 0 {
		return 0
	}
	return int(math.Round(6000 / float64(best)))
}

func windowPitch(samples []int16) (float64, bool) {
	const decimation = 6
	mono := make([]float64, len(samples)/(2*decimation))
	mean := 0.0
	for i := range mono {
		for j := 0; j < decimation*2; j++ {
			mono[i] += float64(samples[i*decimation*2+j])
		}
		mono[i] /= decimation * 2
		mean += mono[i]
	}
	mean /= float64(len(mono))
	var power float64
	for i := range mono {
		mono[i] -= mean
		power += mono[i] * mono[i]
	}
	if power < float64(len(mono))*160*160 {
		return 0, false
	}
	const lo, hi = 8, 134
	var correlation [hi + 2]float64
	best := 0.0
	for lag := lo - 1; lag <= hi+1; lag++ {
		dot, a, b := 0.0, 0.0, 0.0
		for i := 0; i < len(mono)-lag; i++ {
			x, y := mono[i], mono[i+lag]
			dot += x * y
			a += x * x
			b += y * y
		}
		if a > 0 && b > 0 {
			correlation[lag] = dot / math.Sqrt(a*b)
		}
		best = max(best, correlation[lag])
	}
	if best < .78 {
		return 0, false
	}
	for lag := lo; lag <= hi; lag++ {
		a, b, c := correlation[lag-1], correlation[lag], correlation[lag+1]
		if b < max(.75, .93*best) || b < a || b < c {
			continue
		}
		d := 0.0
		if a-2*b+c != 0 {
			d = .5 * (a - c) / (a - 2*b + c)
		}
		note := 69 + 12*math.Log2(8000/(float64(lag)+d)/440)
		return note, true
	}
	return 0, false
}

func estimateKey(chroma [12]float64, voiced int) string {
	unique := 0
	for _, x := range chroma {
		if x > 0 {
			unique++
		}
	}
	if unique < 3 || voiced < 8 {
		return ""
	}
	major := [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	minor := [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
	best, next := 0.0, 0.0
	key := ""
	names := []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	for _, mode := range []struct {
		name   string
		weight [12]float64
	}{{" major", major}, {" minor", minor}} {
		for root := range names {
			score := 0.0
			for i, w := range mode.weight {
				score += chroma[(i+root)%12] * w
			}
			if score > best {
				next, best, key = best, score, names[root]+mode.name
			} else {
				next = max(next, score)
			}
		}
	}
	if best-next < float64(voiced)*.13 {
		return ""
	}
	return key
}

func pcmSamples(raw []byte) []int16 {
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples
}
