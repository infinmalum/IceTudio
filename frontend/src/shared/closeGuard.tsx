import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Events } from "@wailsio/runtime";
import { DesktopService } from "../../bindings/icetudio";
import { useDialog } from "./dialogs";

type Blocker = { kind: "busy" | "draft"; message: string; version?: string; preserved?: boolean };
type Register = (read: () => Blocker | null) => () => void;
const CloseContext = createContext<Register | null>(null);
const CloseFrozenContext = createContext(false);
export const useCloseFrozen = () => useContext(CloseFrozenContext);

// Readers consult current refs at request time, rather than asynchronously
// publishing dirty flags to the native host before every input event.
export function useCloseBlocker(read: () => Blocker | null) {
  const register = useContext(CloseContext);
  if (!register) throw Error("Music editing requires CloseGuardProvider");
  const current = useRef(read);
  current.current = read;
  useLayoutEffect(() => register(() => current.current()), [register]);
}

export function CloseGuardProvider({ children }: { children: ReactNode }) {
  const dialog = useDialog();
  const readers = useRef(new Map<number, () => Blocker | null>());
  const serial = useRef(0);
  const register = useRef<Register>(read => {
    const id = ++serial.current;
    readers.current.set(id, read);
    return () => { readers.current.delete(id); };
  }).current;
  const [ready, setReady] = useState(false), [closing, setClosing] = useState(false), [error, setError] = useState("");
  const checking = useRef(false);
  const mounted = useRef(false);
  const ask = useRef(dialog.ask);
  ask.current = dialog.ask;
  const activate = async () => {
    try { await DesktopService.Ready(); if (mounted.current) { setReady(true); setError(""); } }
    catch (e) { if (mounted.current) setError(`无法启用退出保护，请重试。${String(e)}`); }
  };
  useEffect(() => {
    mounted.current = true;
    const snapshot = () => [...readers.current].flatMap(([id, read]) => {
      const blocker = read();
      return blocker ? [{ id, ...blocker }] : [];
    });
    const off = Events.On("icetudio:close-requested", event => {
      const id = event.data;
      if (typeof id !== "string" || !id || checking.current) return;
      checking.current = true;
      // A close question must not queue behind a still-interactive modal.
      // Leave that input in place and let the user finish/cancel it first.
      if (ask.current.hasPending()) {
        setError("请先完成或取消当前对话框，再关闭窗口。");
        void DesktopService.CancelClose(id).catch(e => setError(String(e))).finally(() => { checking.current = false; });
        return;
      }
      // Preserve the existing single-value onBlur commit contract. Its edit
      // lock is set synchronously, before the close checks are read below.
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
      setClosing(true); setError("");
      void (async () => {
        let confirmed = false;
        try {
          const before = snapshot();
          const busy = before.filter(b => b.kind === "busy");
          if (busy.length) {
            await ask.current({ title: "工程操作尚未确认", body: busy.map(b => b.message).join("；"), confirm: "返回工程处理", cancel: "继续留在应用" });
          } else {
            const preserved = before.length > 0 && before.every(b => b.preserved);
            const allowExit = !before.length || await ask.current({ title: preserved ? "保留音乐编辑草稿并退出？" : "丢弃音乐编辑草稿并退出？", body: before.map(b => b.message).join("；") + (preserved ? "。草稿已持久保留，下次可恢复；不会自动应用到工程。" : "。已保存的工程不会删除；这些未应用输入不会自动保存。"), confirm: preserved ? "保留草稿并退出" : "丢弃草稿并退出", cancel: "继续编辑", danger: !preserved }) !== null;
            if (allowExit && mounted.current) {
              if (JSON.stringify(snapshot()) !== JSON.stringify(before)) {
                await ask.current({ title: "编辑状态已经变化", body: "本次退出已取消，请检查当前草稿或保存结果后重新关闭。", confirm: "返回检查" });
              } else {
                await DesktopService.ConfirmClose(id);
                confirmed = true;
              }
            }
          }
        } catch (e) {
          if (mounted.current) setError(`退出未完成，工程仍在当前窗口。${String(e)}`);
        } finally {
          if (!confirmed) {
            try { await DesktopService.CancelClose(id); }
            catch (e) { if (mounted.current) setError(`退出请求未取消成功；请重新关闭窗口。${String(e)}`); }
            checking.current = false;
            if (mounted.current) setClosing(false);
          }
          // After confirmation, keep input blocked until the native app exits.
        }
      })();
    });
    void activate();
    return () => { mounted.current = false; off(); };
  }, []);
  return <CloseContext.Provider value={register}><CloseFrozenContext.Provider value={!ready || closing}>
    {(!ready || error) && <div className="close-guard-status" role={error ? "alert" : "status"}>{error || "正在启用退出保护…"}{!ready && <button onClick={() => void activate()}>重试退出保护</button>}</div>}
    <div style={{ display: "contents" }} ref={node => node?.toggleAttribute("inert", !ready || closing)} data-close-guard={closing ? "checking" : ready ? "ready" : "starting"}>{children}</div>
  </CloseFrozenContext.Provider></CloseContext.Provider>;
}
