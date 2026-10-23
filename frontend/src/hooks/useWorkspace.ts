import { useEffect, useRef, useState } from "react";
import { StatusService } from "../../bindings/icetudio";
import type { Conversation } from "../../bindings/icetudio/internal/storage";
import { useDialog } from "../shared/dialogs";
import { clearDraftFiles } from "../shared/draftFiles";
export function useWorkspace() {
  const dialog = useDialog();
  const [workspaceError, setWorkspaceError] = useState("");
  const [sessions, setSessions] = useState<Conversation[]>([]);
  const [reuseContext, setReuseContext] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [contextID, setContextID] = useState("");
  const creatingRef = useRef(false);
  const [query, setQuery] = useState("");
  const [loaded, setLoaded] = useState(false);
  const activeConversation = sessions.find((s) => s.id === selected) ?? null;
  function openConversation(item: Conversation) {
    setContextID(item.contextId);
    setSelected(item.id);
    try {
      localStorage.setItem("icetudio.lastConversation", item.id);
    } catch {}
  }
  // Composer-ready state: nothing is created until a message is actually
  // sent, so there are no empty placeholder conversations.
  function newDraft() {
    setContextID("");
    setSelected(null);
    try {
      localStorage.removeItem("icetudio.lastConversation");
    } catch {}
  }
  async function refreshConversations() {
    const items = (await StatusService.ListConversations()) ?? [];
    setSessions(items);
    return items;
  }
  // The service binds a new conversation to the built-in default config.
  // Throws so the sending flow can show the failure next to the composer.
  async function createSession() {
    if (creatingRef.current) throw new Error("正在创建对话，请稍候");
    creatingRef.current = true;
    try {
      const item = await StatusService.CreateConversation(
        "",
        reuseContext && selected ? selected : "",
        "",
      );
      setSessions((current) => [
        item,
        ...current.filter((s) => s.id !== item.id),
      ]);
      openConversation(item);
      setReuseContext(false);
      setWorkspaceError("");
      return item;
    } finally {
      creatingRef.current = false;
    }
  }
  async function renameConversation(item: Conversation) {
    const title = await dialog.ask({
      title: "重命名对话",
      label: "对话名称",
      initial: item.title,
      confirm: "保存",
      singleLine: true,
    });
    if (title === null) return;
    try {
      await StatusService.RenameConversation(item.id, title);
      await refreshConversations();
    } catch (e) {
      setWorkspaceError(String(e));
    }
  }
  async function deleteConversation(item: Conversation) {
    if (
      !(await dialog.confirm({
        title: "删除这个对话？",
        body: "将删除此对话、附件、执行记录和未采用的建议，无法恢复。执行配置与本机权限不会删除。",
        confirm: "删除对话",
        danger: true,
      }))
    )
      return;
    try {
      await StatusService.DeleteConversation(item.id);
      try {
        localStorage.removeItem("icetudio.draft." + item.id);
      } catch {}
      void clearDraftFiles(item.id);
      const items = await refreshConversations();
      if (selected === item.id) {
        // Fall back to a conversation the sidebar actually shows; opening a
        // hidden delegation session would strand the user in a read-only view.
        const next = items.find((i) => i.kind === "normal");
        if (next) openConversation(next);
        else {
          setSelected(null);
          setContextID("");
          try {
            localStorage.removeItem("icetudio.lastConversation");
          } catch {}
        }
      }
    } catch (e) {
      setWorkspaceError(String(e));
    }
  }
  useEffect(() => {
    let active = true;
    StatusService.ListConversations()
      .then((items) => {
        if (!active) return;
        setSessions(items ?? []);
        let lastID = "";
        try {
          lastID = localStorage.getItem("icetudio.lastConversation") ?? "";
        } catch {}
        const last = (items ?? []).find(
          (i) => i.id === lastID && i.kind === "normal",
        );
        const fallback = (items ?? []).find((i) => i.kind === "normal");
        if (last ?? fallback) openConversation((last ?? fallback)!);
      })
      .catch((e) => setWorkspaceError(String(e)))
      .finally(() => {
        if (active) setLoaded(true);
      });
    return () => {
      active = false;
    };
  }, []);
  return {
    loaded,
    workspaceError,
    sessions,
    setSessions,
    reuseContext,
    setReuseContext,
    selected,
    contextID,
    activeConversation,
    openConversation,
    newDraft,
    refreshConversations,
    createSession,
    query,
    setQuery,
    renameConversation,
    deleteConversation,
  };
}
export type Workspace = ReturnType<typeof useWorkspace>;
