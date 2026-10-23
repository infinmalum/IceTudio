import { useCallback, useEffect, useRef, useState } from "react";
import { ProjectService } from "../../bindings/icetudio";
import type { MapDraft } from "../music/timeMap";
import type { MeterPoint, TempoPoint } from "../../bindings/icetudio/internal/project/models";
import { useCloseBlocker } from "../shared/closeGuard";

export type MapSnapshot = { version: 1; projectID: string; title: string; ppq: number; baseRevision: number; tempo: TempoPoint[]; meter: MeterPoint[]; draft: MapDraft };
type Record = { version: number; token: string; payload: string };
export function useTimeMapDraft() {
  const [record, setRecord] = useState<Record | null>(null);
  const current = useRef<Record | null>(null);
  const attempt = useRef<{ expected: string; token: string; payload: string } | null>(null);
  const latest = useRef("");
  const [writing, setWriting] = useState(false);
  const desire = useCallback((payload: string) => { latest.current = payload; }, []);
  const [ready, setReady] = useState(false), [error, setError] = useState("");
  const readyRef = useRef(false), sequence = useRef(0);
  const put = useCallback((r: Record | null) => { current.current = r; setRecord(r); }, []);
  useCloseBlocker(() => !readyRef.current ? { kind: "busy", message: "时间图草稿检查尚未完成，请重试草稿检查。" } : attempt.current ? { kind: "busy", message: "时间图草稿写入尚未确认，请重试保留草稿。" } : null);
  const check = useCallback(async () => {
    const seq = ++sequence.current;
    readyRef.current = false; setReady(false); setError("");
    try {
      const r = await ProjectService.GetTimeMapDraft();
      if (seq !== sequence.current) return;
      put(r); latest.current = r?.payload ?? ""; readyRef.current = true; setReady(true);
    } catch (e) { if (seq === sequence.current) setError(`无法读取时间图草稿，原文件已保留。${String(e)}`); }
  }, [put]);
  useEffect(() => { void check(); return () => { sequence.current++; }; }, [check]);
  const write = useCallback(async (payload: string) => {
    if (!readyRef.current) throw Error("请先完成草稿检查");
    setWriting(true);
    // Retry an unconfirmed checkpoint with exactly its original CAS/token,
    // before writing newer input or clearing. Never invent a new intention.
    try { while (attempt.current || (current.current?.payload ?? "") !== payload) {
      const req = attempt.current ?? { token: crypto.randomUUID(), expected: current.current?.token ?? "", payload };
      attempt.current = req;
      try {
        if (req.payload) await ProjectService.SaveTimeMapDraft(req.expected, req.token, req.payload);
        else await ProjectService.ClearTimeMapDraft(req.expected, req.token);
      } catch (e) {
        let observed: Record | null = null;
        try { observed = await ProjectService.GetTimeMapDraft(); } catch { /* Original error remains actionable. */ }
        if (observed?.token !== req.token || observed.payload !== req.payload) throw e;
      }
      put({ version: 1, token: req.token, payload: req.payload });
      attempt.current = null;
    } setError(""); }
    catch (e) { setError(`时间图草稿尚未保留，请重试保留草稿。${String(e)}`); throw e; }
    finally { setWriting(false); }
  }, [put]);
  const snapshot = record?.payload ? JSON.parse(record.payload) as MapSnapshot : null;
  return { record, snapshot, ready, error, writing, check, write, desire, retry: () => write(latest.current), clear: () => { latest.current = ""; return write(""); } };
}
export type TimeMapDrafts = ReturnType<typeof useTimeMapDraft>;

// One dialog serializes writes and coalesces the latest desired input.
export function useMapCheckpoint(drafts: TimeMapDrafts, payload: string | null) {
  const desired = useRef(payload), acknowledged = useRef<string | null>(drafts.record?.payload || null);
  desired.current = payload;
  drafts.desire(payload ?? "");
  const running = useRef<Promise<void> | null>(null), stopped = useRef(false);
  const [, render] = useState(0);
  const [error, setError] = useState("");
  const writer = useRef(drafts.write); writer.current = drafts.write;
  const flush = useCallback((): Promise<void> => {
    if (running.current) return running.current;
    const task = (async () => {
      setError("");
      while (!stopped.current && desired.current !== acknowledged.current) {
        const next = desired.current;
        await writer.current(next ?? "");
        acknowledged.current = next; render(n => n + 1);
      }
    })();
    running.current = task;
    void task.catch(e => setError(`草稿尚未保留，请重试；当前输入仍在。${String(e)}`)).finally(() => { running.current = null; render(n => n + 1); });
    return task;
  }, []);
  useEffect(() => {
    const timer = setTimeout(() => { if (!stopped.current) void flush().catch(() => {}); }, 200);
    return () => clearTimeout(timer);
  }, [payload, flush]);
  useEffect(() => () => { if (!stopped.current) void flush().catch(() => {}); }, [flush]);
  const discard = async () => {
    stopped.current = true;
    drafts.desire("");
    try {
      await running.current?.catch(() => {});
      await writer.current(""); acknowledged.current = null;
    } catch (e) { stopped.current = false; drafts.desire(desired.current ?? ""); throw e; }
  };
  return { pending: desired.current !== acknowledged.current, error, flush, discard };
}
