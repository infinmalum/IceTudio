package playback

// Resolve parameter controller protocols before audio scheduling. Selection
// and data-entry state follow FluidSynth 2.6.1, not numerical CC order. Every
// resulting Parameter event is self-contained: seeks and loops need one value
// per affected register, and future data entries never depend on a reconstructed
// synth's hidden selector state. Original project/MIDI events remain untouched.
func resolveParameters(events []Event) []Event {
	type channel struct {
		cc         [128]int32
		nrpn       bool
		selectNRPN int32
		tuningBank uint32
	}
	channels := map[int32]*channel{}
	out := make([]Event, 0, len(events))
	for _, event := range events {
		e := event
		if e.Kind != CC {
			out = append(out, e)
			continue
		}
		c := channels[e.Channel]
		if c == nil {
			c = &channel{}
			for _, n := range []int{98, 99, 100, 101} {
				c.cc[n] = 127
			}
			channels[e.Channel] = c
		}
		c.cc[e.A] = e.B
		switch e.A {
		case 99:
			c.cc[98], c.cc[6], c.cc[38] = 0, 0, 0
			c.selectNRPN, c.nrpn = 0, true
		case 98:
			c.cc[6], c.cc[38], c.nrpn = 0, 0, true
			if c.cc[99] == 120 {
				// Values >= GEN_LAST can only remain invalid until the next
				// data-MSB or NRPN-MSB reset. Saturate to avoid signed overflow.
				add := e.B
				if add == 100 {
					add = 100
				}
				if add == 101 {
					add = 1000
				}
				if add == 102 {
					add = 10000
				}
				if add > 102 {
					add = 0
				}
				c.selectNRPN = min(16384, c.selectNRPN+add)
			}
		case 100, 101:
			c.cc[6], c.cc[38], c.nrpn = 0, 0, false
		case 6, 38:
			e.Kind, e.A, e.B = Parameter, -1, c.cc[6]*128+c.cc[38]
			if c.nrpn {
				switch c.cc[99] {
				case 120:
					if c.cc[98] < 100 && event.A == 6 {
						if c.selectNRPN < 64 {
							e.A = 256 + c.selectNRPN
						}
						c.selectNRPN = 0
					}
				case 127:
					if c.cc[98] <= 26 {
						e.A = 512 + c.cc[98]
						if c.cc[98] == 16 {
							e.Kind, e.A, e.B = Bend, e.B, 0
						}
					}
				case 1:
					if c.cc[98] >= 8 && c.cc[98] <= 10 {
						e.Kind, e.A, e.B = CC, 76+c.cc[98]-8, c.cc[6]
					}
				}
			} else if c.cc[101] == 0 && c.cc[100] <= 5 {
				e.A = c.cc[100]
				if e.A == 4 {
					c.tuningBank = uint32(c.cc[6])
				}
				if e.A == 3 {
					e.Token = c.tuningBank
				}
			}
			if e.A >= 0 {
				out = append(out, e)
			}
		case 121:
			for _, n := range []int{98, 99, 100, 101} {
				c.cc[n] = 127
			}
			c.cc[6], c.cc[38] = 0, 0
			out = append(out, e)
		default:
			out = append(out, e)
		}
	}
	return out
}

func parameterKey(e Event) int32 {
	// RPN fine/coarse tune and SF2 NRPN share the same generator offset.
	if e.A == 1 {
		return 256 + 52
	}
	if e.A == 2 {
		return 256 + 51
	}
	return e.A
}
