package playback

import (
	"bytes"
	"encoding/binary"
	"math"
)

// BasicFont produces a small, deterministic SoundFont from original periodic
// formulae. Generated waveform/preset data is dedicated under CC0-1.0; this
// generator's source follows the repository AGPL-3.0-only license. No recorded
// samples, downloaded banks, or GridSound assets are used.
func BasicFont() []byte {
	enc := func(values ...any) []byte {
		var b bytes.Buffer
		for _, v := range values {
			_ = binary.Write(&b, binary.LittleEndian, v)
		}
		return b.Bytes()
	}
	chunk := func(name string, data []byte) []byte {
		if (name == "INAM" || name == "isng" || name == "ICOP") && len(data)%2 != 0 {
			data = append(data, 0)
		}
		b := append([]byte(name), enc(uint32(len(data)))...)
		b = append(b, data...)
		if len(data)%2 != 0 {
			b = append(b, 0)
		}
		return b
	}
	list := func(name string, children ...[]byte) []byte {
		b := []byte(name)
		for _, child := range children {
			b = append(b, child...)
		}
		return chunk("LIST", b)
	}
	name := func(s string, n int) []byte { b := make([]byte, n); copy(b, s); return b }
	const frames = 256
	var samples []int16
	var shdr []byte
	for wave := 0; wave < 4; wave++ {
		start := len(samples)
		for i := 0; i < frames; i++ {
			x := float64(i) / frames
			v := math.Sin(2 * math.Pi * x)
			switch wave {
			case 1:
				v = 2 / math.Pi * math.Asin(v)
			case 2:
				v = 2*x - 1
			case 3:
				if x < .5 {
					v = .7
				} else {
					v = -.7
				}
			}
			samples = append(samples, int16(v*16000))
		}
		samples = append(samples, make([]int16, 46)...)
		shdr = append(shdr, name([]string{"Sine", "Triangle", "Saw", "Square"}[wave], 20)...)
		shdr = append(shdr, enc(uint32(start), uint32(start+frames), uint32(start), uint32(start+frames), uint32(440*frames), uint8(69), int8(0), uint16(0), uint16(1))...)
	}
	shdr = append(shdr, name("EOS", 20)...)
	shdr = append(shdr, make([]byte, 26)...)
	var phdr, pbag, pgen []byte
	for program := 0; program < 128; program++ {
		phdr = append(phdr, name([]string{"Basic Sine", "Basic Triangle", "Basic Saw", "Basic Square"}[program/32], 20)...)
		phdr = append(phdr, enc(uint16(program), uint16(0), uint16(program), uint32(0), uint32(0), uint32(0))...)
		pbag = append(pbag, enc(uint16(program), uint16(0))...)
		pgen = append(pgen, enc(uint16(41), uint16(program/32))...)
	}
	// A basic percussion-bank preset (not a sampled drum kit).
	phdr = append(phdr, name("Basic Percussion", 20)...)
	phdr = append(phdr, enc(uint16(0), uint16(128), uint16(128), uint32(0), uint32(0), uint32(0))...)
	pbag = append(pbag, enc(uint16(128), uint16(0))...)
	pgen = append(pgen, enc(uint16(41), uint16(3))...)
	phdr = append(phdr, name("EOP", 20)...)
	phdr = append(phdr, enc(uint16(0), uint16(0), uint16(129), uint32(0), uint32(0), uint32(0))...)
	pbag = append(pbag, enc(uint16(129), uint16(0))...)
	pgen = append(pgen, make([]byte, 4)...)
	var inst, ibag, igen []byte
	for wave := 0; wave < 4; wave++ {
		inst = append(inst, name([]string{"Sine", "Triangle", "Saw", "Square"}[wave], 20)...)
		inst = append(inst, enc(uint16(wave))...)
		ibag = append(ibag, enc(uint16(wave*5), uint16(0))...)
		for _, g := range [][2]int16{{34, -9000}, {38, -5200}, {54, 1}, {58, 69}, {53, int16(wave)}} {
			igen = append(igen, enc(uint16(g[0]), g[1])...)
		}
	}
	inst = append(inst, name("EOI", 20)...)
	inst = append(inst, enc(uint16(4))...)
	ibag = append(ibag, enc(uint16(20), uint16(0))...)
	igen = append(igen, make([]byte, 4)...)
	info := list("INFO", chunk("ifil", enc(uint16(2), uint16(1))), chunk("isng", []byte("EMU8000\x00")), chunk("INAM", []byte("IceTudio Basic Waves\x00")), chunk("ICOP", []byte("Original mathematical waveforms. CC0-1.0. No recorded samples.\x00")))
	sdta := list("sdta", chunk("smpl", enc(samples)))
	pdta := list("pdta", chunk("phdr", phdr), chunk("pbag", pbag), chunk("pmod", make([]byte, 10)), chunk("pgen", pgen), chunk("inst", inst), chunk("ibag", ibag), chunk("imod", make([]byte, 10)), chunk("igen", igen), chunk("shdr", shdr))
	data := append([]byte("sfbk"), info...)
	data = append(data, sdta...)
	data = append(data, pdta...)
	return chunk("RIFF", data)
}
