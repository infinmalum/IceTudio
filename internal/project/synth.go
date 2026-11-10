package project

type SoundPreset struct {
	ID, Name, Category string
	Tone, Release      float64
}

var soundPresets = []SoundPreset{
	{"soft-saw", "雾面锯齿", "减法合成", .4, .28},
	{"hollow-square", "空心方波", "减法合成", .65, .16},
	{"fm-bell", "冰晶铃", "FM 合成", .52, .67},
	{"modeled-string", "尼龙拨弦", "物理建模", .36, .48},
}

func SoundPresets() []SoundPreset {
	return append([]SoundPreset(nil), soundPresets...)
}

func FindSoundPreset(id string) (SoundPreset, bool) {
	for _, preset := range soundPresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return SoundPreset{}, false
}

type SynthPatch struct {
	Preset  string  `json:"preset"`
	Tone    float64 `json:"tone"`
	Release float64 `json:"release"`
}

func (s SynthPatch) Valid() bool {
	_, ok := FindSoundPreset(s.Preset)
	return ok && mixValue(s.Tone, 0, 1) && mixValue(s.Release, 0, 1)
}
