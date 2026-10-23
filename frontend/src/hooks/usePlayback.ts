import { useEffect, useRef, useState } from "react";
import { PlaybackService } from "../../bindings/icetudio";
import type { Request, Settings, Status } from "../../bindings/icetudio/internal/playback/models";

export function usePlayback(projectID: string, revision: number) {
  const [status, setStatus] = useState<Status | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(true);
  const lock = useRef(false);
  const latest = useRef({ projectID, revision });
  latest.current = { projectID, revision };
  const refresh = async () => {
    const value = await PlaybackService.Status();
    if (mounted.current) setStatus(value);
    return value;
  };
  useEffect(() => {
    mounted.current = true;
    let polling = false;
    const poll = async () => {
      if (polling) return;
      polling = true;
      try { await refresh(); } catch (e) { if (mounted.current) setError(String(e)); }
      finally { polling = false; }
    };
    void poll(); const timer = setInterval(poll, 150);
    return () => {
      mounted.current = false; clearInterval(timer);
      void PlaybackService.StopProject(projectID).catch(() => {});
    };
  }, [projectID]);

  async function run(action: () => Promise<unknown>) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError("");
    try {
      const applied = await action();
      if (applied === false) return false;
      if (!mounted.current) { await PlaybackService.StopProject(projectID); return false; }
      await refresh();
      return true;
    } catch (e) { if (mounted.current) setError(String(e)); return false; }
    finally { lock.current = false; if (mounted.current) setBusy(false); }
  }
  function request(tick: number, loop: boolean, start: number, end: number): Request {
    return { projectId: projectID, revision: status?.projectId === projectID ? status.revision : latest.current.revision, tick, loop, loopStart: start, loopEnd: end };
  }
  return {
    status, busy, error,
    play: (tick: number, loop: boolean, start: number, end: number) => run(() => PlaybackService.Play({ ...request(tick, loop, start, end), revision: latest.current.revision })),
    stop: async () => {
      try { await PlaybackService.Stop(); if (mounted.current) await refresh(); }
      catch (e) { if (mounted.current) setError(String(e)); }
    },
    seek: (tick: number, loop: boolean, start: number, end: number) => run(() => PlaybackService.Seek(request(tick, loop, start, end))),
    configure: (settings: Settings) => run(() => PlaybackService.Configure(settings)),
    chooseFont: (driver: string) => run(async () => {
      const path = await PlaybackService.PickSoundFont();
      if (!path) return false;
      await PlaybackService.Configure({ version: 1, driver, soundFont: path });
    }),
  };
}
