import { useEffect, useRef, useState } from "react";
import { useDialog } from "../shared/dialogs";
import { StatusService } from "../../bindings/icetudio";
import type {
  ContextPolicy,
  LocalContext,
  ExfilGrant,
} from "../../bindings/icetudio/internal/storage";
export function useLocalContext(contextID: string, selected: string | null) {
  const dialog = useDialog();
  useEffect(() => {
    setGrantForm({ destination: "", scopes: [] });
  }, [contextID]);
  const activeContextID = useRef(contextID);
  activeContextID.current = contextID;
  const contextBase = useRef<LocalContext | null>(null);
  const [contextDraft, setContextDraft] = useState<LocalContext | null>(null);
  const currentDraft = useRef(contextDraft);
  currentDraft.current = contextDraft;
  const [contextBusy, setContextBusy] = useState(false);
  const [contextError, setContextError] = useState("");
  const [grants, setGrants] = useState<ExfilGrant[]>([]);
  const [grantForm, setGrantForm] = useState<{
    destination: string;
    scopes: string[];
  }>({ destination: "", scopes: [] });
  const [grantBusy, setGrantBusy] = useState(false);
  const [grantError, setGrantError] = useState("");
  useEffect(() => {
    let current = true;
    setGrants([]);
    setGrantError("");
    if (!contextID) return;
    StatusService.ListExfilGrants(contextID)
      .then((items) => {
        if (current) setGrants(items ?? []);
      })
      .catch((reason) => {
        if (current) setGrantError(String(reason));
      });
    return () => {
      current = false;
    };
  }, [contextID]);
  // Unsaved edits survive switching conversations: they are stashed per
  // context and restored on return instead of being silently dropped.
  const contextCache = useRef(new Map<string, LocalContext>());
  const prevContext = useRef("");
  useEffect(() => {
    const previous = prevContext.current;
    prevContext.current = contextID;
    if (previous && previous !== contextID) {
      const base = contextBase.current;
      const draft = currentDraft.current;
      if (
        base &&
        base.id === previous &&
        draft &&
        JSON.stringify(draft) !== JSON.stringify(base)
      )
        contextCache.current.set(previous, draft);
      else contextCache.current.delete(previous);
    }
    let current = true;
    contextBase.current = null;
    setContextError("");
    const cached = contextCache.current.get(contextID) ?? null;
    setContextDraft(cached);
    if (selected)
      StatusService.GetLocalContext(contextID)
        .then((item) => {
          if (!current) return;
          contextBase.current = item;
          if (!contextCache.current.has(contextID)) setContextDraft(item);
        })
        .catch((reason) => {
          if (current) setContextError(String(reason));
        });
    return () => {
      current = false;
    };
  }, [contextID, selected]);
  const contextDirty =
    !!contextDraft &&
    (!contextBase.current ||
      JSON.stringify(contextDraft) !== JSON.stringify(contextBase.current));
  function discardContextEdits() {
    contextCache.current.delete(contextID);
    setContextDraft(contextBase.current);
    setContextError("");
  }
  async function refreshContext() {
    if (selected) {
      const item = await StatusService.GetLocalContext(contextID);
      if (activeContextID.current === contextID) {
        contextBase.current = item;
        contextCache.current.delete(contextID);
        setContextDraft(item);
      }
    }
  }
  async function refreshVisible() {
    if (!contextID) return;
    try {
      const [item, updatedGrants] = await Promise.all([
        StatusService.GetLocalContext(contextID),
        StatusService.ListExfilGrants(contextID),
      ]);
      if (activeContextID.current !== contextID) return;
      setGrants(updatedGrants ?? []);
      if (
        JSON.stringify(currentDraft.current) ===
        JSON.stringify(contextBase.current)
      ) {
        contextBase.current = item;
        setContextDraft(item);
      }
    } catch (e) {
      if (activeContextID.current === contextID) setContextError(String(e));
    }
  }
  async function saveContext(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!contextDraft || !selected) return;
    setContextBusy(true);
    try {
      const latest = await StatusService.GetLocalContext(contextID);
      const base = contextBase.current;
      if (!base || base.id !== contextID) {
        setContextError("设置状态已过期，请重新打开此对话的权限设置后再保存。");
        return;
      }
      // Apply only fields edited here; folder grants may have changed in chat.
      const merge = <T extends object>(
        original: T,
        edited: T,
        current: T,
      ): T => {
        const result = { ...current };
        for (const key of Object.keys(edited) as (keyof T)[]) {
          if (JSON.stringify(edited[key]) === JSON.stringify(original[key]))
            continue;
          if (
            JSON.stringify(current[key]) !== JSON.stringify(original[key]) &&
            JSON.stringify(current[key]) !== JSON.stringify(edited[key])
          )
            throw new Error(
              "此项权限已在其他操作中修改，请重新打开对话后核对再保存。",
            );
          result[key] = edited[key];
        }
        return result;
      };
      const permissions = merge(
        base.policy.permissions,
        contextDraft.policy.permissions,
        latest.policy.permissions,
      );
      const policy = merge(
        { ...base.policy, permissions },
        { ...contextDraft.policy, permissions },
        { ...latest.policy, permissions },
      );
      await StatusService.UpdateContextPolicy(contextID, policy);
      await refreshContext();
      setContextError("");
    } catch (reason) {
      setContextError(String(reason));
    } finally {
      setContextBusy(false);
    }
  }
  async function saveGrant(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!grantForm.destination.trim() || grantForm.scopes.length === 0) return;
    setGrantBusy(true);
    try {
      await StatusService.GrantExfil(
        contextID,
        grantForm.destination,
        grantForm.scopes,
      );
      const updated = (await StatusService.ListExfilGrants(contextID)) ?? [];
      if (activeContextID.current !== contextID) return;
      setGrants(updated);
      setGrantForm({ destination: "", scopes: [] });
      setGrantError("");
    } catch (reason) {
      setGrantError(String(reason));
    } finally {
      setGrantBusy(false);
    }
  }
  async function revokeGrant(destination: string) {
    if (
      !(await dialog.confirm({
        title: "停止向此服务发送内容？",
        body: destination,
        confirm: "撤销授权",
        danger: true,
      }))
    )
      return;
    setGrantBusy(true);
    try {
      await StatusService.RevokeExfil(contextID, destination);
      const updated = (await StatusService.ListExfilGrants(contextID)) ?? [];
      if (activeContextID.current !== contextID) return;
      setGrants(updated);
      setGrantError("");
    } catch (reason) {
      setGrantError(String(reason));
    } finally {
      setGrantBusy(false);
    }
  }
  function updatePolicy(patch: Partial<ContextPolicy>) {
    if (!contextDraft) return;
    setContextDraft({
      ...contextDraft,
      policy: { ...contextDraft.policy, ...patch },
    });
  }
  function updatePermissions(patch: Partial<ContextPolicy["permissions"]>) {
    if (!contextDraft) return;
    setContextDraft({
      ...contextDraft,
      policy: {
        ...contextDraft.policy,
        permissions: { ...contextDraft.policy.permissions, ...patch },
      },
    });
  }
  return {
    contextDraft,
    contextDirty,
    contextBusy,
    contextError,
    discardContextEdits,
    grants,
    grantForm,
    setGrantForm,
    grantBusy,
    grantError,
    refreshVisible,
    saveContext,
    saveGrant,
    revokeGrant,
    updatePolicy,
    updatePermissions,
  };
}
export type LocalContextState = ReturnType<typeof useLocalContext>;
