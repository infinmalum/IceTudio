// 标准 General MIDI Level 1 乐器元数据（固定公开事实，随代码内置）。
// 家族按 GM 规范每 8 个 program 一组：family 索引 = floor(program / 8)。
// 打击组不是 melodic program，走 bank 128（channel 10 / 索引 9）。

export interface GmFamily {
  readonly en: string;
  readonly zh: string;
}

export const GM_FAMILIES: readonly GmFamily[] = [
  { en: "Piano", zh: "钢琴" },
  { en: "Chromatic Percussion", zh: "色彩打击乐器" },
  { en: "Organ", zh: "风琴" },
  { en: "Guitar", zh: "吉他" },
  { en: "Bass", zh: "贝斯" },
  { en: "Strings", zh: "弦乐" },
  { en: "Ensemble", zh: "合奏" },
  { en: "Brass", zh: "铜管" },
  { en: "Reed", zh: "簧管" },
  { en: "Pipe", zh: "吹管" },
  { en: "Synth Lead", zh: "合成主音" },
  { en: "Synth Pad", zh: "合成铺底" },
  { en: "Synth Effects", zh: "合成效果" },
  { en: "Ethnic", zh: "民族乐器" },
  { en: "Percussive", zh: "打击" },
  { en: "Sound Effects", zh: "音效" },
];

export const GM_INSTRUMENT_NAMES: readonly string[] = [
  "Acoustic Grand Piano", "Bright Acoustic Piano", "Electric Grand Piano", "Honky-tonk Piano",
  "Electric Piano 1", "Electric Piano 2", "Harpsichord", "Clavi",
  "Celesta", "Glockenspiel", "Music Box", "Vibraphone",
  "Marimba", "Xylophone", "Tubular Bells", "Dulcimer",
  "Drawbar Organ", "Percussive Organ", "Rock Organ", "Church Organ",
  "Reed Organ", "Accordion", "Harmonica", "Tango Accordion",
  "Acoustic Guitar (nylon)", "Acoustic Guitar (steel)", "Electric Guitar (jazz)", "Electric Guitar (clean)",
  "Electric Guitar (muted)", "Overdriven Guitar", "Distortion Guitar", "Guitar harmonics",
  "Acoustic Bass", "Electric Bass (finger)", "Electric Bass (pick)", "Fretless Bass",
  "Slap Bass 1", "Slap Bass 2", "Synth Bass 1", "Synth Bass 2",
  "Violin", "Viola", "Cello", "Contrabass",
  "Tremolo Strings", "Pizzicato Strings", "Orchestral Harp", "Timpani",
  "String Ensemble 1", "String Ensemble 2", "Synth Strings 1", "Synth Strings 2",
  "Choir Aahs", "Voice Oohs", "Synth Voice", "Orchestra Hit",
  "Trumpet", "Trombone", "Tuba", "Muted Trumpet",
  "French Horn", "Brass Section", "Synth Brass 1", "Synth Brass 2",
  "Soprano Sax", "Alto Sax", "Tenor Sax", "Baritone Sax",
  "Oboe", "English Horn", "Bassoon", "Clarinet",
  "Piccolo", "Flute", "Recorder", "Pan Flute",
  "Blown Bottle", "Shakuhachi", "Whistle", "Ocarina",
  "Lead 1 (square)", "Lead 2 (sawtooth)", "Lead 3 (calliope)", "Lead 4 (chiff)",
  "Lead 5 (charang)", "Lead 6 (voice)", "Lead 7 (fifths)", "Lead 8 (bass + lead)",
  "Pad 1 (new age)", "Pad 2 (warm)", "Pad 3 (polysynth)", "Pad 4 (choir)",
  "Pad 5 (bowed)", "Pad 6 (metallic)", "Pad 7 (halo)", "Pad 8 (sweep)",
  "FX 1 (rain)", "FX 2 (soundtrack)", "FX 3 (crystal)", "FX 4 (atmosphere)",
  "FX 5 (brightness)", "FX 6 (goblins)", "FX 7 (echoes)", "FX 8 (sci-fi)",
  "Sitar", "Banjo", "Shamisen", "Koto",
  "Kalimba", "Bag pipe", "Fiddle", "Shanai",
  "Tinkle Bell", "Agogo", "Steel Drums", "Woodblock",
  "Taiko Drum", "Melodic Tom", "Synth Drum", "Reverse Cymbal",
  "Guitar Fret Noise", "Breath Noise", "Seashore", "Bird Tweet",
  "Telephone Ring", "Helicopter", "Applause", "Gunshot",
];

export interface GmInstrument {
  readonly program: number;
  readonly name: string;
  readonly family: GmFamily;
}

export const GM_PERCUSSION_CHANNEL = 9;
export const GM_PERCUSSION = {
  bank: 128,
  program: 0,
  name: "GM Standard Kit",
  zh: "GM 标准打击乐组",
} as const;

export function isGmProgram(program: number): boolean {
  return Number.isInteger(program) && program >= 0 && program <= 127;
}

export function gmInstrument(program: number): GmInstrument {
  if (!isGmProgram(program)) throw new Error(`GM program 超出范围：${program}`);
  return { program, name: GM_INSTRUMENT_NAMES[program], family: GM_FAMILIES[Math.floor(program / 8)] };
}

export function isPercussionChannel(channel: number): boolean {
  return channel === GM_PERCUSSION_CHANNEL;
}

// 打击通道的轨道乐器显示名；melodic 通道用 gmInstrument().name。
export function trackInstrumentLabel(program: number, channel: number): string {
  if (isPercussionChannel(channel)) return GM_PERCUSSION.name;
  return gmInstrument(program).name;
}

export interface GmMatch {
  readonly program: number;
  readonly name: string;
  readonly family: GmFamily;
  readonly matched: boolean;
}

// 搜索同时匹配英文名与家族的中英文名；空关键字匹配全部。
export function searchGmInstruments(query: string): readonly GmMatch[] {
  const q = query.trim().toLowerCase();
  return GM_INSTRUMENT_NAMES.map((name, program) => {
    const family = GM_FAMILIES[Math.floor(program / 8)];
    const matched = q === "" || name.toLowerCase().includes(q) || family.en.toLowerCase().includes(q) || family.zh.includes(q);
    return { program, name, family, matched };
  });
}
