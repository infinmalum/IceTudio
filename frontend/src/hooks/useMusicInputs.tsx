import { createContext, useContext, useRef, useState } from "react";
import type { EditTarget } from "../music/edits";
import { useCloseBlocker } from "../shared/closeGuard";

export type MusicInput = { key: string; label: string; title: string; target: EditTarget; trackID?: string; clipID?: string; noteIDs?: string[]; raw: string };
const storageKey = "icetudio.music-inputs.v1";
type State = { version: 1; entries: MusicInput[] };
function read(): State {
  const raw = localStorage.getItem(storageKey);
  if (!raw) return { version: 1, entries: [] };
  if (raw.length > 4 * 1024 * 1024) throw Error("音乐输入草稿超过 4 MiB；原记录已保留");
  const state = JSON.parse(raw) as State;
  if (state.version !== 1 || !Array.isArray(state.entries) || state.entries.length > 256 || state.entries.some(e => !e || typeof e.key !== "string" || typeof e.raw !== "string" || e.raw.length > 1024 || typeof e.label !== "string" || typeof e.title !== "string" || typeof e.target?.id !== "string" || !Number.isSafeInteger(e.target?.revision) || e.target.revision < 0 || (e.trackID !== undefined && typeof e.trackID !== "string") || (e.clipID !== undefined && typeof e.clipID !== "string") || (e.noteIDs !== undefined && (!Array.isArray(e.noteIDs) || e.noteIDs.some(id => typeof id !== "string")))) || new Set(state.entries.map(e => e.key)).size !== state.entries.length) throw Error("音乐输入草稿格式不受支持或已损坏；原记录已保留");
  for (const entry of state.entries) {
    if (entry.key !== JSON.stringify([entry.target.id, entry.trackID ?? "", entry.clipID ?? "", entry.label])) throw Error("音乐输入草稿目标不匹配；原记录已保留");
    if (entry.label === "播放与音频设置") {
      const value = JSON.parse(entry.raw);
      if (!value || typeof value.position !== "string" || typeof value.start !== "string" || typeof value.end !== "string" || typeof value.loop !== "boolean" || !["pulseaudio", "pipewire", "alsa"].includes(value.driver)) throw Error("播放输入草稿格式不受支持；原记录已保留");
    }
  }
  return state;
}
export function useMusicInputs() {
  const initial = useRef<{ state: State; error: string }>();
  if (!initial.current) { try { initial.current = { state: read(), error: "" }; } catch (e) { initial.current = { state: { version: 1, entries: [] }, error: String(e) }; } }
  const current = useRef(initial.current.state);
  const [state, setState] = useState(current.current);
  const [error, setError] = useState(initial.current.error);
  const failure = useRef(initial.current.error);
  const readable = useRef(!initial.current.error);
  // Restoring a record is explicit. Mounting/reopening never applies its input.
  const [restored, setRestored] = useState(new Set<string>());
  const [focus, setFocus] = useState<MusicInput | null>(null);
  useCloseBlocker(() => failure.current ? { kind: "busy", message: `音乐输入草稿尚未保存，请重试。${failure.current}` } : current.current.entries.length ? { kind: "draft", message: `有 ${current.current.entries.length} 项未应用的音乐输入，可保留后退出。`, preserved: true, version: JSON.stringify(current.current) } : null);
  function persist(next: State) {
    current.current = next; setState(next);
    try {
      if (!readable.current) throw Error("请先重试读取原音乐输入草稿；不会覆盖未读取的记录");
      const raw = JSON.stringify(next);
      if (next.entries.length > 256 || raw.length > 4 * 1024 * 1024) throw Error("音乐输入草稿超过保存上限，请先处理已有草稿");
      localStorage.setItem(storageKey, raw); failure.current = ""; setError(""); return true;
    } catch (e) { failure.current = String(e); setError(String(e)); return false; }
  }
  return {
    state, error, focus, restored,
    put(entry: MusicInput) { if (!readable.current || entry.raw.length > 1024) return false; setRestored(s => new Set(s).add(entry.key)); return persist({ version: 1, entries: [...current.current.entries.filter(e => e.key !== entry.key), entry] }); },
    clear(key: string) { return persist({ version: 1, entries: current.current.entries.filter(e => e.key !== key) }); },
    restore(entry: MusicInput) { setRestored(s => new Set(s).add(entry.key)); setFocus({ ...entry }); },
    retry() {
      if (readable.current) return persist(current.current);
      try { const value = read(); readable.current = true; current.current = value; setState(value); failure.current = ""; setError(""); } catch (e) { failure.current = String(e); setError(String(e)); }
    },
  };
}
export const MusicInputs = createContext<ReturnType<typeof useMusicInputs> | null>(null);
export function useMusicField(meta: Omit<MusicInput, "key" | "raw">, fallback: string) {
  const store = useContext(MusicInputs);
  if (!store) throw Error("Music field requires MusicInputs");
  const key = JSON.stringify([meta.target.id, meta.trackID ?? "", meta.clipID ?? "", meta.label]);
  const entry = store.state.entries.find(e => e.key === key);
  const live = entry && store.restored.has(key) ? entry : null;
  return {
    entry: live, raw: live?.raw ?? fallback, error: store.error, locked: !!entry && !live,
    change(raw: string, frozen?: Omit<MusicInput, "key" | "raw">) { return store.put({ ...(live ?? frozen ?? meta), key, raw }); },
    clear: () => store.clear(key),
  };
}
