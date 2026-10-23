import { useCallback, useEffect, useRef, useState } from "react";
import { Call, Events } from "@wailsio/runtime";
import { ProjectService, type Project, type ProjectSummary } from "../../bindings/icetudio";
import type { Op } from "../../bindings/icetudio/internal/project/models";
import type { EditTarget } from "../music/edits";
import { useDialog } from "../shared/dialogs";
import { useCloseBlocker } from "../shared/closeGuard";
import { useTimeMapDraft } from "./useTimeMapDraft";
import { useMusicInputs } from "./useMusicInputs";

type Pending = { projectID: string; opID: string; title?: string };

export function useProjects(
  conversationID: string | null,
  boundProjectID: string,
  refreshConversations?: () => Promise<unknown>,
) {
  const dialog = useDialog();
  const timeMapDrafts = useTimeMapDraft();
  const inputs = useMusicInputs();
  const [error, setError] = useState("");
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [activeID, setActiveID] = useState<string | null>(null);
  const [detail, setDetail] = useState<Project | null>(null);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Pending | null>(null);
  const [recoveryReady, setRecoveryReady] = useState(false);
  const [recoveryError, setRecoveryError] = useState("");
  const recoveryReadyRef = useRef(false);
  const recoverySequence = useRef(0);
  const [saveStatus, setSaveStatus] = useState("");
  const currentID = useRef(activeID);
  const activeRef = useRef<Project | null>(null);
  const lock = useRef(false);
  const pendingRef = useRef(pending);
  const savedProject = useRef<Pending | null>(null);
  useCloseBlocker(() => !recoveryReadyRef.current ? { kind: "busy", message: "工程恢复检查尚未完成，请先重试恢复检查。" } : lock.current ? { kind: "busy", message: "工程操作仍在进行，请等待操作完成后再退出。" } : pendingRef.current ? { kind: "busy", message: "工程保存结果尚未确认，请使用“查询保存结果”核对原操作；不会自动重放编辑。", version: JSON.stringify(pendingRef.current) } : null);
  const detailRequest = useRef(0);
  const listRequest = useRef(0);
  currentID.current = activeID;
  const active = detail?.id === activeID ? detail : null;
  activeRef.current = active;

  const select = useCallback((id: string | null) => {
    currentID.current = id;
    detailRequest.current++;
    setActiveID(id);
  }, []);

  const refresh = useCallback(async () => {
    const seq = ++listRequest.current;
    const items = (await ProjectService.ListProjects()) ?? [];
    if (seq !== listRequest.current) return items;
    setProjects(items);
    const current = currentID.current;
    if (!current || !items.some(p => p.id === current)) {
      let stored: string | null = null;
      try { stored = localStorage.getItem("icetudio.lastProject"); } catch {}
      select(items.find(p => p.id === boundProjectID)?.id
        ?? items.find(p => p.id === stored)?.id ?? items[0]?.id ?? null);
    }
    return items;
  }, [boundProjectID, select]);

  const refreshDetail = useCallback(async (id: string) => {
    // A completion for an older project must not invalidate the selected
    // project's in-flight read, even though it may still need an outcome query.
    if (currentID.current !== id) return ProjectService.GetProject(id);
    const seq = ++detailRequest.current;
    const item = await ProjectService.GetProject(id);
    if (seq === detailRequest.current && currentID.current === id) {
      setDetail(prev => prev?.id === id && prev.revision > item.revision ? prev : item);
    }
    return item;
  }, []);

  useEffect(() => { void refresh().catch(e => setError(String(e))); }, [refresh]);
  useEffect(() => { if (boundProjectID) select(boundProjectID); }, [boundProjectID, select]);
  useEffect(() => {
    const id = activeID;
    try {
      if (id) localStorage.setItem("icetudio.lastProject", id);
      else localStorage.removeItem("icetudio.lastProject");
    } catch {}
    if (!id) { setDetail(null); return; }
    void refreshDetail(id).catch(e => { if (currentID.current === id) setError(String(e)); });
    return () => { detailRequest.current++; };
  }, [activeID, refreshDetail]);

  // The event is a hint: always query committed state. Out-of-order or duplicate
  // notifications must never replace a newer detail or another selected project.
  useEffect(() => Events.On("icetudio:service-event", event => {
    const evt = event.data as { type?: string; projectId?: string };
    if (!evt?.type?.startsWith("project.")) return;
    void refresh().catch(e => setError(String(e)));
    if (evt.type === "project.deleted") {
      void refreshConversations?.().catch(e => setError(String(e)));
    } else if (evt.projectId && evt.projectId === currentID.current) {
      void refreshDetail(evt.projectId).catch(e => setError(String(e)));
    }
  }), [refresh, refreshDetail, refreshConversations]);

  async function run(action: () => Promise<unknown>) {
    if (!recoveryReadyRef.current || lock.current || pendingRef.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try { await action(); }
    catch (e) { setError(String(e)); }
    finally { lock.current = false; setBusy(false); }
  }

  function remember(value: Pending | null) {
    pendingRef.current = value;
    setPending(value);
  }

  const checkRecovery = useCallback(async () => {
    if (lock.current) return;
    const sequence = ++recoverySequence.current;
    recoveryReadyRef.current = false; setRecoveryReady(false); setRecoveryError("");
    try {
      const record = await ProjectService.GetPendingProjectEdit();
      if (sequence !== recoverySequence.current) return;
      remember(record);
      recoveryReadyRef.current = true; setRecoveryReady(true);
      if (record) setSaveStatus("发现待确认工程操作，请查询保存结果；未重放编辑");
    } catch (e) {
      if (sequence === recoverySequence.current) setRecoveryError(`无法读取工程恢复记录，编辑暂不可用；请重试恢复检查。${String(e)}`);
    }
  }, []);
  useEffect(() => {
    void checkRecovery();
    return () => { recoverySequence.current++; };
  }, [checkRecovery]);

  async function clearPending(record: Pending) {
    await ProjectService.ClearPendingProjectEdit(record.projectID, record.opID);
    remember(null);
  }

  async function confirmed(record: Pending, revision: number) {
    savedProject.current = record;
    await reloadAfterCommit(record.projectID, revision);
    try { await clearPending(record); }
    catch (e) { setError(`修改已保存（修订 ${revision}），恢复标记清理失败；请查询保存结果后重试清理。${String(e)}`); }
  }

  async function reloadAfterCommit(id: string, revision: number) {
    setSaveStatus(`已保存 · 工程「${savedProject.current?.title || "原工程"}」 · 修订 ${revision}`);
    try { await Promise.all([refresh(), refreshDetail(id)]); }
    catch (e) { setError(`修改已保存，视图刷新失败；请刷新工程。${String(e)}`); }
  }

  async function mutate(target: EditTarget, ops: Op[] | "undo" | "redo"): Promise<boolean> {
    if (!recoveryReadyRef.current || lock.current || pendingRef.current) return false;
    lock.current = true;
    setBusy(true);
    setError("");
    const opID = crypto.randomUUID();
    const record = { projectID: target.id, opID, title: activeRef.current?.id === target.id ? activeRef.current.title : "" };
    remember(record);
    setSaveStatus("正在保存…");
    try {
      try {
        await ProjectService.BeginProjectEdit(target.id, opID, target.revision, ops === "undo" || ops === "redo" ? ops : "edit");
      } catch (reason) {
        // No music request was sent. A lost Begin reply can nevertheless leave
        // a durable intention; load it rather than overwriting another one.
        try {
          const stored = await ProjectService.GetPendingProjectEdit();
          remember(stored ?? (reason instanceof Call.RuntimeError ? null : record));
        } catch (lookupError) {
          recoveryReadyRef.current = false; setRecoveryReady(false);
          setRecoveryError(`无法核对恢复记录，请重试恢复检查。${String(lookupError)}`);
        }
        setError(`编辑未发送：无法确认持久恢复标记。${String(reason)}`);
        setSaveStatus("本次编辑未发送");
        return false;
      }
      const result = ops === "undo"
        ? await ProjectService.UndoProject(target.id, opID, target.revision)
        : ops === "redo"
          ? await ProjectService.RedoProject(target.id, opID, target.revision)
          : await ProjectService.SubmitProjectOps(target.id, opID, "gui", target.revision, ops);
      await confirmed(record, result.revision);
      return true;
    } catch (reason) {
      // A lost reply is distinct from a rejected edit. Confirm the original
      // opID; never blindly resubmit with a new one.
      try {
        const [result, found] = await ProjectService.GetProjectTransaction(target.id, opID);
        if (found) {
          await confirmed(record, result.revision);
          return true;
        }
        if (reason instanceof Call.RuntimeError) {
          // Wails explicitly reports a completed method rejection. Together
          // with an absent committed result, this is a rejected edit, not a
          // disconnected request that might still be running.
          try { await clearPending(record); }
          catch (e) { setSaveStatus("编辑已拒绝，恢复标记待清理"); setError(`修改未提交，恢复标记清理失败；请查询原结果。${String(e)}`); return false; }
          setSaveStatus("本次编辑已拒绝");
          try {
            await refreshDetail(target.id);
            setError(`修改未提交：${String(reason)}。已刷新工程，请根据当前内容重新编辑。`);
          } catch (refreshError) {
            setError(`修改未提交：${String(reason)}；工程刷新失败，请稍后刷新。${String(refreshError)}`);
          }
          return false;
        }
        // Absence alone cannot exclude a request still running after a lost
        // transport reply. Keep the operation ID until a later query confirms it.
        setError(`编辑未获确认：${String(reason)}。请查询原操作结果，确认后再继续。`);
        setSaveStatus("等待确认保存结果");
        try { await refreshDetail(target.id); } catch {}
      } catch (lookupError) {
        setError(`保存结果未知。${String(reason)}；查询失败：${String(lookupError)}`);
        setSaveStatus("保存结果未知");
      }
      return false;
    } finally { lock.current = false; setBusy(false); }
  }

  // Network failures may leave the original request running. Query its
  // committed outcome without parsing error strings or replaying the edit.
  async function resolvePending() {
    const record = pendingRef.current;
    if (!record || !recoveryReadyRef.current || lock.current) return;
    lock.current = true; setBusy(true);
    try {
      const [result, found] = await ProjectService.GetProjectTransaction(record.projectID, record.opID);
      if (found) {
        setError("");
        await confirmed(record, result.revision);
      } else {
        const ok = await dialog.confirm({ title: "尚未查到已提交结果", body: `工程「${record.title || "原工程"}」的操作可能被拒绝，也可能仍在处理。继续等待可再次查询；只有你确认原任务已结束，才选择刷新工程并停止等待。原操作不会自动重放。`, confirm: "刷新工程并继续", cancel: "继续等待" });
        if (ok) {
          const items = await refresh();
          if (items.some(p => p.id === record.projectID)) await refreshDetail(record.projectID);
          await clearPending(record);
          setError(""); setSaveStatus("已刷新工程；未重放原操作");
        }
      }
    } catch (e) { setError(`无法完成原操作核对或清理恢复标记，请稍后再试。${String(e)}`); }
    finally { lock.current = false; setBusy(false); }
  }

  function create() { void run(async () => {
    const title = await dialog.ask({ title: "新建音乐工程", label: "工程名", initial: "未命名工程", singleLine: true, confirm: "创建" });
    if (title === null) return;
    const item = await ProjectService.CreateProject(title || "未命名工程", 0);
    await refresh(); select(item.id);
  }); }
  function importMIDI() { void run(async () => {
    const path = await ProjectService.PickOpenMIDIFile();
    if (!path) return;
    const item = await ProjectService.ImportProjectMIDI(path);
    await refresh(); select(item.id);
  }); }
  function exportMIDI() {
    const target = activeRef.current;
    if (!target) return;
    void run(async () => {
      const path = await ProjectService.PickSaveMIDIFile((target.title || "工程") + ".mid");
      if (path) await ProjectService.ExportProjectMIDI(target.id, path);
    });
  }
  function remove(id: string) { void run(async () => {
    const title = projects.find(p => p.id === id)?.title ?? id;
    const ok = await dialog.confirm({ title: "删除工程", body: `删除工程「${title}」及其全部编辑历史。对话和消息不受影响，无法撤销。`, confirm: "删除", danger: true });
    if (!ok) return;
    await ProjectService.DeleteProject(id);
    await refresh(); await refreshConversations?.();
  }); }
  async function rename() {
    const target = activeRef.current;
    if (!target || !recoveryReadyRef.current || lock.current || pendingRef.current) return;
    const title = await dialog.ask({ title: "重命名工程", label: "新工程名", initial: target.title, singleLine: true, confirm: "应用" });
    if (title !== null && title !== target.title) await mutate(target, [{ kind: "project.rename", title }]);
  }
  function shift(direction: "undo" | "redo") {
    const target = activeRef.current;
    if (target) void mutate(target, direction);
  }
  function bind(bindToConversation: boolean) {
    const id = activeID;
    void run(async () => {
      if (!conversationID) throw Error("请先选择对话，再绑定工程。");
      await ProjectService.SetConversationProject(conversationID, bindToConversation ? id ?? "" : "");
      await refreshConversations?.();
    });
  }
  return {
    inputs,
    timeMapDrafts,
    error, projects, activeID, active, busy: busy || !!pending || !recoveryReady,
    recoveryReady, recoveryError, checkRecovery,
    saveStatus: saveStatus.startsWith("已保存") && active && active.id === savedProject.current?.projectID ? `已保存 · 修订 ${active.revision}` : saveStatus,
    pending, resolvePending, conversationID, boundProjectID, select, refresh,
    reload: () => { const id = currentID.current; if (id) void refreshDetail(id).catch(e => setError(String(e))); },
    edit: (target: EditTarget, ops: Op[]) => mutate(target, ops),
    reportError: (e: unknown) => setError(String(e)),
    create, importMIDI, exportMIDI, remove, rename, shift, bind,
  };
}
