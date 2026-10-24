import { createContext, useContext, useEffect, useRef, useState } from "react";
import type { EditTarget } from "../music/edits";
import { ProjectService } from "../../bindings/icetudio";
import { useCloseBlocker } from "../shared/closeGuard";

export type MusicInput = { key: string; label: string; title: string; target: EditTarget; trackID?: string; clipID?: string; noteIDs?: string[]; raw: string };
type State = { version: 1; entries: MusicInput[] };

// Drafts live in the Go data dir, not browser localStorage: a draft written
// right before the native 保留草稿并退出 quit was lost because the WebKit
// storage flush is not guaranteed on that path (native acceptance 2026-10-24).
function validate(raw: string): State {
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

function newToken() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, b => b.toString(16).padStart(2, "")).join("");
}

export function useMusicInputs() {
  const initial = useRef<{ state: State }>();
  if (!initial.current) initial.current = { state: { version: 1, entries: [] } };
  const current = useRef(initial.current.state);
  const [state, setState] = useState(current.current);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState("");
  const failure = useRef("");
  const token = useRef("");
  const pending = useRef(0);
  const chain = useRef<Promise<void>>(Promise.resolve());
  const retryState = useRef<State | null>(null);
  // Restoring a record is explicit. Mounting/reopening never applies its input.
  const [restored, setRestored] = useState(new Set<string>());
  const [focus, setFocus] = useState<MusicInput | null>(null);
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const record = await ProjectService.GetMusicInputs();
        if (!alive) return;
        if (record?.payload) current.current = validate(record.payload);
        token.current = record?.token ?? "";
        setState(current.current);
      } catch (e) {
        if (!alive) return;
        failure.current = String(e);
        setError(String(e));
      } finally {
        if (alive) setReady(true);
      }
    })();
    return () => { alive = false; };
  }, []);
  // Saves are serialized; each one carries the token of the last successful
  // write so a delayed or reordered save cannot overwrite another draft.
  function persist(next: State) {
    current.current = next;
    setState(next);
    retryState.current = next;
    pending.current++;
    chain.current = chain.current.then(async () => {
      const desired = retryState.current;
      if (!desired) return;
      try {
        const payload = JSON.stringify(desired);
        if (desired.entries.length > 256 || payload.length > 4 * 1024 * 1024) throw Error("音乐输入草稿超过保存上限，请先处理已有草稿");
        const saved = newToken();
        await ProjectService.SaveMusicInputs(token.current, saved, payload);
        token.current = saved;
        failure.current = "";
        setError("");
      } catch (e) {
        failure.current = String(e);
        setError(String(e));
      } finally {
        pending.current--;
      }
    });
  }
  useCloseBlocker(() => failure.current ? { kind: "busy", message: `音乐输入草稿尚未保存，请重试。${failure.current}` } : pending.current > 0 ? { kind: "busy", message: "音乐输入草稿正在保存，请等待完成后再退出。" } : current.current.entries.length ? { kind: "draft", message: `有 ${current.current.entries.length} 项未应用的音乐输入，可保留后退出。`, preserved: true, version: JSON.stringify(current.current) } : null);
  return {
    state, ready, error, focus, restored,
    put(entry: MusicInput) { if (entry.raw.length > 1024) return false; setRestored(s => new Set(s).add(entry.key)); persist({ version: 1, entries: [...current.current.entries.filter(e => e.key !== entry.key), entry] }); return true; },
    clear(key: string) { persist({ version: 1, entries: current.current.entries.filter(e => e.key !== key) }); },
    restore(entry: MusicInput) { setRestored(s => new Set(s).add(entry.key)); setFocus({ ...entry }); },
    retry() {
      chain.current = chain.current.then(async () => {
        const desired = retryState.current;
        if (!desired) return;
        try {
          const record = await ProjectService.GetMusicInputs();
          if (record?.payload) validate(record.payload);
          token.current = record?.token ?? token.current;
          const saved = newToken();
          await ProjectService.SaveMusicInputs(token.current, saved, JSON.stringify(desired));
          token.current = saved;
          current.current = desired;
          setState(desired);
          failure.current = "";
          setError("");
        } catch (e) {
          failure.current = String(e);
          setError(String(e));
        }
      });
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
    change(raw: string, frozen?: Omit<MusicInput, "key" | "raw">) {
      // A restored entry keeps its original revision (silent-rebase refusal);
      // frozen metas may carry a ClipTarget, so entries persist exactly the
      // declared MusicInput shape checked by the service.
      const m = live ?? frozen ?? meta;
      return store.put({ key, label: m.label, title: m.title, target: { id: m.target.id, revision: m.target.revision }, trackID: m.trackID, clipID: m.clipID, noteIDs: m.noteIDs, raw });
    },
    clear: () => store.clear(key),
  };
}
