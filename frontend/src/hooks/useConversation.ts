import { useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ProjectService, StatusService, type AppStatus } from "../../bindings/icetudio";
import type { SendTarget } from "./useProjects";
import type {
  SearchRequest,
  TaskReview,
  TaskRetrospective,
  TaskGraph,
  Message,
  Run,
  SessionSummary,
  ToolCall,
  WorkerTrace,
} from "../../bindings/icetudio/internal/storage";
import type {
  SearchAvailability,
  SessionPlanView,
} from "../../bindings/icetudio/internal/app";
import type { Workspace } from "./useWorkspace";
import { useDialog } from "../shared/dialogs";
import { useSendConsent } from "../shared/consent";
import {
  clearDraftFiles,
  loadDraftFiles,
  saveDraftFiles,
} from "../shared/draftFiles";
export function useConversation(
  workspace: Workspace,
  status: AppStatus | null,
  sendTarget?: SendTarget | null,
) {
  const {
    contextID,
    selected,
    activeConversation,
    setSessions,
    openConversation,
    refreshConversations,
    createSession,
  } = workspace;
  const dialog = useDialog();
  const ensureConsent = useSendConsent();
  const draftCache = useRef(
    new Map<string, { prompt: string; attachments: File[] }>(),
  );
  const [messages, setMessages] = useState<Message[]>([]);
  const [hasEarlier, setHasEarlier] = useState(false);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const earliestLoaded = useRef(0);
  async function fetchMessages(id: string) {
    const page = await StatusService.ListChatMessages(id, 0, 80);
    if (activeSessionID.current === id && earliestLoaded.current === 0) {
      earliestLoaded.current = page.beforeSeq;
      setHasEarlier(page.hasMore);
    }
    return page.messages ?? [];
  }
  function mergeMessages(next: Message[]) {
    setMessages((current) =>
      next.length
        ? [...current.filter((m) => m.seq < next[0].seq), ...next]
        : current,
    );
  }
  async function loadEarlier() {
    if (!selected || loadingEarlier) return;
    const id = selected;
    setLoadingEarlier(true);
    try {
      const page = await StatusService.ListChatMessages(
        id,
        earliestLoaded.current,
        80,
      );
      if (activeSessionID.current === id) {
        setMessages((current) => [...(page.messages ?? []), ...current]);
        earliestLoaded.current = page.beforeSeq;
        setHasEarlier(page.hasMore);
      }
    } catch (e) {
      setTaskError(String(e));
    } finally {
      setLoadingEarlier(false);
    }
  }
  const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
  // The full call history of this session, so the transcript's task details
  // can show completed tool activity from the authoritative tool_calls rows.
  const [toolHistory, setToolHistory] = useState<ToolCall[]>([]);
  const [workerTraces, setWorkerTraces] = useState<WorkerTrace[]>([]);
  const [workerBusy, setWorkerBusy] = useState(false);
  const [traceError, setTraceError] = useState("");
  const [approvalBusy, setApprovalBusy] = useState(false);
  const [runs, setRuns] = useState<Run[]>([]);
  const [activeRun, setActiveRun] = useState<string | null>(null);
  const [prompt, setPrompt] = useState("");
  const [attachments, setAttachments] = useState<File[]>([]);
  // Drafts survive restarts: text lives in localStorage, attachments in
  // IndexedDB, with the in-memory cache serving instant switches. The
  // composer of the draft state (no selected conversation yet) persists
  // under the "new" key the same way.
  const lastSavedFiles = useRef(new Map<string, File[]>());
  useEffect(() => {
    const key = selected ?? "new";
    const cached = draftCache.current.get(key);
    let text = "";
    try {
      text = localStorage.getItem("icetudio.draft." + key) ?? "";
    } catch {}
    setPrompt(cached?.prompt ?? text);
    setAttachments(cached?.attachments ?? []);
    if (!draftCache.current.has(key))
      void loadDraftFiles(key).then((files) => {
        if (!files.length || activeSessionID.current !== selected) return;
        const entry = draftCache.current.get(key);
        if (entry?.attachments.length) return;
        lastSavedFiles.current.set(key, files);
        draftCache.current.set(key, {
          prompt: entry?.prompt ?? text,
          attachments: files,
        });
        setAttachments(files);
      });
  }, [selected]);
  function updateDraft(text: string, files: File[]) {
    const key = selected ?? "new";
    draftCache.current.set(key, { prompt: text, attachments: files });
    try {
      localStorage.setItem("icetudio.draft." + key, text);
    } catch {}
    if (lastSavedFiles.current.get(key) !== files) {
      lastSavedFiles.current.set(key, files);
      void saveDraftFiles(key, files);
    }
  }
  function changePrompt(value: string) {
    setPrompt(value);
    updateDraft(value, attachments);
  }
  function addAttachments(files: FileList | File[] | null) {
    if (!files) return;
    const next = [...attachments, ...Array.from(files)];
    if (
      next.length > 8 ||
      next.some((f) => f.size > 10 * 1024 * 1024) ||
      next.reduce((n, f) => n + f.size, 0) > 20 * 1024 * 1024
    ) {
      setTaskError("最多8个附件，单个10MiB，总计20MiB。");
      return;
    }
    setAttachments(next);
    updateDraft(prompt, next);
    setTaskError("");
  }
  function removeAttachment(index: number) {
    const next = attachments.filter((_, i) => i !== index);
    setAttachments(next);
    updateDraft(prompt, next);
  }
  const [taskError, setTaskError] = useState("");
  const [searchAvailability, setSearchAvailability] =
    useState<SearchAvailability | null>(null);
  const [searchRecords, setSearchRecords] = useState<SearchRequest[]>([]);
  const [searchBusy, setSearchBusy] = useState(false);
  const [sending, setSending] = useState(false);
  const [loadingSession, setLoadingSession] = useState(false);
  const [streamText, setStreamText] = useState("");
  const [sessionSummary, setSessionSummary] = useState<SessionSummary | null>(
    null,
  );
  const [sessionPlan, setSessionPlan] = useState<SessionPlanView | null>(null);
  const [reviews, setReviews] = useState<TaskReview[]>([]);
  const [retrospectives, setRetrospectives] = useState<TaskRetrospective[]>([]);
  const [reviewBusy, setReviewBusy] = useState(false);
  const [taskGraph, setTaskGraph] = useState<TaskGraph | null>(null);
  const [childRuns, setChildRuns] = useState<Run[]>([]);
  function refreshSessionKnowledge(id: string) {
    const current = () =>
      activeContextID.current === contextID && activeSessionID.current === id;
    StatusService.ListConversationSearch(id)
      .then((value) => {
        if (current()) setSearchRecords(value ?? []);
      })
      .catch(() => {});
    StatusService.GetSessionSummary(id)
      .then((value) => {
        if (current()) setSessionSummary(value);
      })
      .catch(() => {
        if (current()) setSessionSummary(null);
      });
    StatusService.GetSessionPlan(id)
      .then((value) => {
        if (current()) setSessionPlan(value);
      })
      .catch(() => {
        if (current()) setSessionPlan(null);
      });
  }
  const activeContextID = useRef(contextID);
  activeContextID.current = contextID;
  const activeSessionID = useRef(selected);
  activeSessionID.current = selected;
  useEffect(() => {
    if (!selected) {
      setMessages([]);
      setRuns([]);
      setToolCalls([]);
      setToolHistory([]);
      setWorkerTraces([]);
      setTraceError("");
      setActiveRun(null);
      setStreamText("");
      setChildRuns([]);
      setTaskGraph(null);
      setReviews([]);
      setRetrospectives([]);
      setSessionSummary(null);
      setSessionPlan(null);
      setSearchRecords([]);
      setTaskError("");
      setHasEarlier(false);
      setLoadingSession(false);
      return;
    }
    let cancelled = false;
    setActiveRun(null);
    setStreamText("");
    setMessages([]);
    earliestLoaded.current = 0;
    setHasEarlier(false);
    setRuns([]);
    setToolCalls([]);
    setToolHistory([]);
    setWorkerTraces([]);
    setTraceError("");
    setChildRuns([]);
    setSessionSummary(null);
    setSessionPlan(null);
    setSearchRecords([]);
    setTaskError("");
    setLoadingSession(true);
    setTaskGraph(null);
    setReviews([]);
    setRetrospectives([]);
    Promise.all([
      fetchMessages(selected),
      StatusService.ListRuns(selected),
      StatusService.PendingApprovals(contextID),
      StatusService.ListToolCalls(selected),
    ])
      .then(([history, previousRuns, calls, historyCalls]) => {
        if (cancelled) return;
        setMessages(history ?? []);
        setRuns(previousRuns ?? []);
        setToolCalls(calls ?? []);
        setToolHistory(historyCalls ?? []);
        setActiveRun(
          (previousRuns ?? []).find((item) => item.status === "running")?.id ??
            null,
        );
        setLoadingSession(false);
        refreshChildRuns((previousRuns ?? [])[0]?.id);
      })
      .catch((reason: unknown) => {
        if (!cancelled) {
          setTaskError(String(reason));
          setLoadingSession(false);
        }
      });
    refreshSessionKnowledge(selected);
    return () => {
      cancelled = true;
    };
  }, [contextID, selected]);
  useEffect(() => {
    if (!activeRun || !selected) return;
    let cancelled = false;
    // Reconnect: pick up the streamed-but-uncommitted text so a page reload
    // or reconnect keeps the live transcript continuous.
    StatusService.PendingStreamText(activeRun)
      .then((value) => {
        if (!cancelled) setStreamText(value ?? "");
      })
      .catch(() => {});
    let querying = false;
    const timer = window.setInterval(async () => {
      if (querying) return;
      querying = true;
      try {
        const [run, history, calls, pending, historyCalls] = await Promise.all([
          StatusService.GetRun(activeRun),
          fetchMessages(selected),
          StatusService.PendingApprovals(contextID),
          StatusService.PendingStreamText(activeRun),
          StatusService.ListToolCalls(selected),
        ]);
        if (cancelled) return;
        mergeMessages(history ?? []);
        setToolCalls(calls ?? []);
        setToolHistory(historyCalls ?? []);
        setStreamText(pending ?? "");
        setRuns((current) => [
          run,
          ...current.filter((item) => item.id !== run.id),
        ]);
        refreshChildRuns(run.id);
        refreshSessionKnowledge(selected);
        if (run.status !== "running") setActiveRun(null);
      } catch (reason) {
        if (!cancelled) setTaskError(String(reason));
      } finally {
        querying = false;
      }
    }, 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [activeRun, contextID, selected]);
  useEffect(() => {
    if (!activeRun) return;
    const off = Events.On("icetudio:service-event", (event) => {
      const detail =
        (event.data as {
          type?: string;
          runId?: string;
          detail?: string;
        }) ?? {};
      if (
        detail.type === "stream.delta" &&
        detail.runId === activeRun &&
        detail.detail
      ) {
        setStreamText((current) => current + detail.detail);
      }
    });
    return () => {
      off();
    };
  }, [activeRun]);
  const modelKey = status?.model
    ? `${status.model.provider}/${status.model.baseUrl}/${status.model.name}`
    : "";
  useEffect(() => {
    setSearchAvailability(null);
    if (!selected) return;
    let cancelled = false;
    StatusService.ConversationSearchAvailability(selected)
      .then((value) => {
        if (!cancelled) setSearchAvailability(value);
      })
      .catch((reason) => {
        if (!cancelled) setTaskError(String(reason));
      });
    return () => {
      cancelled = true;
    };
  }, [selected, activeConversation?.configId, modelKey]);
  async function toggleSearch(enabled: boolean) {
    if (!selected || !activeConversation) return;
    const id = selected;
    setSearchBusy(true);
    try {
      const conversation = await StatusService.SetConversationOptions(id, {
        webSearch: enabled,
      });
      setSessions((current) =>
        current.map((item) => (item.id === id ? conversation : item)),
      );
      if (activeSessionID.current === id) setTaskError("");
    } catch (reason) {
      if (activeSessionID.current === id) setTaskError(String(reason));
    } finally {
      setSearchBusy(false);
    }
  }
  useEffect(() => {
    if (!selected) return;
    const off = Events.On("icetudio:service-event", (event) => {
      const detail =
        (event.data as {
          type?: string;
          sessionId?: string;
        }) ?? {};
      if (
        detail.type === "conversation.updated" &&
        detail.sessionId === selected
      ) {
        StatusService.GetConversation(selected)
          .then((conversation) =>
            setSessions((current) =>
              current.map((item) =>
                item.id === conversation.id ? conversation : item,
              ),
            ),
          )
          .catch(() => {});
      }
    });
    return () => off();
  }, [selected]);
  const pendingCalls = toolCalls.filter((item) => item.state === "pending");
  const unknownCalls = toolCalls.filter(
    (item) => item.state === "execution_unknown",
  );
  // Approval requests can also come from delegated (hidden) sessions, whose
  // runs never become this session's activeRun. The backend publishes a run
  // event whenever a run changes, so refresh approvals on any run event and
  // resync the run list when it concerns this session.
  useEffect(() => {
    if (!selected) return;
    const id = selected;
    const off = Events.On("icetudio:service-event", (event) => {
      const detail =
        (event.data as { type?: string; sessionId?: string }) ?? {};
      if (detail.type !== "run.started" && detail.type !== "run.updated")
        return;
      refreshCalls();
      if (detail.sessionId !== id) return;
      StatusService.ListRuns(id)
        .then((items) => {
          if (activeSessionID.current !== id) return;
          const list = items ?? [];
          setRuns(list);
          const running = list.find((item) => item.status === "running");
          if (running) setActiveRun((current) => current ?? running.id);
        })
        .catch(() => {});
    });
    return () => off();
  }, [selected, contextID]);
  // The fast poll stops with activeRun; keep a slow one while any call still
  // waits on the user so follow-up requests (e.g. from delegated work) keep
  // surfacing without requiring the user to send another message.
  useEffect(() => {
    if (!selected || activeRun) return;
    if (!pendingCalls.length && !unknownCalls.length) return;
    const timer = window.setInterval(() => {
      refreshCalls();
    }, 3000);
    return () => window.clearInterval(timer);
  }, [selected, activeRun, pendingCalls.length, unknownCalls.length]);
  async function refreshCalls() {
    if (!selected) return;
    const id = selected;
    const ctx = contextID;
    try {
      const calls = (await StatusService.PendingApprovals(ctx)) ?? [];
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setToolCalls(calls);
    } catch (reason) {
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError(String(reason));
    }
  }
  async function approveCall(call: ToolCall, previewHash?: string) {
    const id = selected;
    const ctx = contextID;
    setApprovalBusy(true);
    try {
      if (previewHash) await ProjectService.ApproveProjectEditPreview(call.id,previewHash);
      else await StatusService.ApproveToolCall(call.id);
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError("");
      await refreshCalls();
    } catch (reason) {
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError(String(reason));
    } finally {
      setApprovalBusy(false);
    }
  }
  async function denyCall(call: ToolCall) {
    const note = await dialog.ask({
      title: "拒绝这次操作",
      body: "助手会收到你的决定，并尝试其他可行方法。",
      label: "原因（可选）",
      confirm: "拒绝操作",
    });
    if (note === null) return;
    const id = selected;
    const ctx = contextID;
    setApprovalBusy(true);
    try {
      await StatusService.DenyToolCall(call.id, note);
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError("");
      await refreshCalls();
    } catch (reason) {
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError(String(reason));
    } finally {
      setApprovalBusy(false);
    }
  }
  async function resolveUnknown(call: ToolCall, asSuccess: boolean) {
    const note = await dialog.ask({
      title: asSuccess ? "记录已核实的结果" : "记录未完成的操作",
      body: "只记录你已实际核实的情况。无法确认时选择“暂不确定”，助手将保持暂停，不会重做可能已经生效的操作。",
      label: "你核对到了什么？",
      confirm: "记录核对结论",
      cancel: "暂不确定",
    });
    if (!note?.trim()) return;
    let result = "";
    if (asSuccess) {
      result =
        (await dialog.ask({
          title: "保存实际结果",
          body: "填写实际看到的结果，供助手继续判断。不要猜测工具输出。",
          label: "实际结果",
          confirm: "保存",
          cancel: "暂不确定",
        })) ?? "";
      if (!result.trim()) return;
    }
    const id = selected;
    const ctx = contextID;
    setApprovalBusy(true);
    try {
      await StatusService.ResolveToolCall(
        call.id,
        asSuccess ? "succeeded" : "failed",
        result,
        note,
      );
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError("");
      await refreshCalls();
    } catch (reason) {
      if (activeSessionID.current === id && activeContextID.current === ctx)
        setTaskError(String(reason));
    } finally {
      setApprovalBusy(false);
    }
  }
  async function clearPlan() {
    if (!selected) return;
    try {
      await StatusService.ClearSessionPlan(selected);
      setSessionPlan(null);
      setTaskError("");
    } catch (reason) {
      setTaskError(String(reason));
    }
  }
  async function refreshChildRuns(latestRunID: string | undefined) {
    if (!latestRunID) {
      setTaskGraph(null);
      setReviews([]);
      setRetrospectives([]);
      setChildRuns([]);
      return;
    }
    try {
      const sessionID = activeSessionID.current;
      const [items, graph, reviewItems, retrospectiveItems] = await Promise.all(
        [
          StatusService.ListChildRuns(latestRunID),
          StatusService.GetTaskGraph(latestRunID).catch(() => null),
          StatusService.ListTaskReviews(latestRunID).catch(() => []),
          StatusService.ListTaskRetrospectives(latestRunID).catch(() => []),
        ],
      );
      if (
        activeContextID.current === contextID &&
        activeSessionID.current === sessionID
      ) {
        setChildRuns(items ?? []);
        setTaskGraph(graph);
        setReviews(reviewItems ?? []);
        setRetrospectives(retrospectiveItems ?? []);
      }
    } catch {
      setChildRuns([]);
    }
  }
  async function openChildSession(child: Run) {
    try {
      const item = await StatusService.GetConversation(child.sessionId);
      setSessions((current) =>
        current.some((c) => c.id === item.id) ? current : [item, ...current],
      );
      openConversation(item);
    } catch (reason) {
      setTaskError(String(reason));
    }
  }
  async function sendTask(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitTask(false);
  }
  async function resumeTask() {
    await submitTask(true);
  }
  async function submitTask(continuation: boolean) {
    if (sending) return;
    if (continuation && !selected) return;
    if (!continuation && !prompt.trim() && !attachments.length) return;
    setSending(true);
    setStreamText("");
    let sessionID = selected;
    try {
      if (!sessionID) {
        // Sending from the composer-ready state creates the conversation;
        // creation failures surface here like any other send failure.
        const created = await createSession();
        sessionID = created.id;
        activeSessionID.current = sessionID;
        activeContextID.current = created.contextId;
        // The draft-mode draft is consumed by this send: carry it under the
        // new conversation's key so a failed send keeps the text, and
        // returning to the composer-ready state starts empty.
        draftCache.current.set(sessionID, { prompt, attachments });
        lastSavedFiles.current.set(sessionID, attachments);
        void saveDraftFiles(sessionID, attachments);
        draftCache.current.delete("new");
        lastSavedFiles.current.delete("new");
        void clearDraftFiles("new");
        try {
          localStorage.removeItem("icetudio.draft.new");
        } catch {}
      }
      if (
        !continuation &&
        activeConversation?.options?.webSearch &&
        searchAvailability?.available !== true
      ) {
        if (
          !(await dialog.confirm({
            title: "当前模型暂不支持联网",
            body: "可以关闭联网，继续普通聊天；也可以到模型设置选择支持联网的模型。",
            confirm: "关闭联网并发送",
            cancel: "返回修改",
          }))
        )
          return;
        await StatusService.SetConversationOptions(sessionID, {
          webSearch: false,
        });
        await refreshConversations();
      }
      if (
        !continuation &&
        !(await ensureConsent(sessionID, attachments.length > 0))
      )
        return;
      const uploads = await Promise.all(
        attachments.map(
          (file) =>
            new Promise<{ name: string; data: string }>((resolve, reject) => {
              const reader = new FileReader();
              reader.onerror = () =>
                reject(new Error(`读取 ${file.name} 失败`));
              reader.onload = () =>
                resolve({
                  name: file.name,
                  data: String(reader.result).split(",")[1],
                });
              reader.readAsDataURL(file);
            }),
        ),
      );
      const rootRunID = taskGraph?.rootRunId ?? runs[0]?.id ?? "";
      if (continuation && !rootRunID)
        throw new Error("找不到可以继续的原任务，请刷新对话后再试。");
      // 工程内对话（docs/p3-agent-scope.md §5）：待输入态发送时绑定编辑器
      // 当前工程；编辑器目标片段与绑定工程一致时携带片段范围，由服务端冻结。
      if (!continuation && !activeConversation && sendTarget?.projectID) {
        await ProjectService.SetConversationProject(sessionID, sendTarget.projectID);
        await refreshConversations();
      }
      const scoped =
        sendTarget &&
        sendTarget.clips.length > 0 &&
        sendTarget.projectID ===
          (activeConversation?.projectId ?? sendTarget.projectID)
          ? { clips: sendTarget.clips }
          : undefined;
      const run = continuation
        ? await StatusService.ContinueConversationTask(
            rootRunID,
            taskGraph?.waitingTaskId ?? "",
            prompt,
            uploads,
          )
        : await StatusService.StartConversationTask(sessionID, prompt, scoped ?? null, uploads);
      draftCache.current.delete(sessionID);
      lastSavedFiles.current.delete(sessionID);
      try {
        localStorage.removeItem("icetudio.draft." + sessionID);
      } catch {}
      void clearDraftFiles(sessionID);
      if (activeSessionID.current !== sessionID) return;
      setAttachments([]);
      setPrompt("");
      setTaskError("");
      setRuns((current) => [
        run,
        ...current.filter((item) => item.id !== run.id),
      ]);
      setActiveRun(run.id);
      await refreshConversations();
      const history = await fetchMessages(sessionID);
      if (activeSessionID.current === sessionID) mergeMessages(history ?? []);
    } catch (reason) {
      if (activeSessionID.current === sessionID) setTaskError(String(reason));
    } finally {
      setSending(false);
    }
  }
  // The developer work-history view: what the delegated workers actually did
  // inside the root task, from the same store the model-side trace tools use.
  async function loadWorkerTraces() {
    if (!selected) return;
    const id = selected;
    const root = taskGraph?.rootRunId ?? runs[0]?.id ?? "";
    if (!root) {
      setTraceError("还没有已开始的任务。");
      return;
    }
    setWorkerBusy(true);
    try {
      const items = await StatusService.ListWorkerTraces(root, "");
      if (activeSessionID.current === id) {
        setWorkerTraces(items ?? []);
        setTraceError("");
      }
    } catch (reason) {
      if (activeSessionID.current === id) setTraceError(String(reason));
    } finally {
      setWorkerBusy(false);
    }
  }
  async function cancelTask() {
    if (!activeRun) return;
    try {
      await StatusService.CancelTask(activeRun);
      setTaskError("");
    } catch (reason) {
      setTaskError(String(reason));
    }
  }
  async function inspectResult(retrospective: boolean) {
    if (!selected || !runs[0]) return;
    setReviewBusy(true);
    try {
      const sessionID = selected;
      const start = retrospective
        ? StatusService.StartConversationRetrospective
        : StatusService.StartConversationReview;
      const target =
        retrospectives[0]?.rootRunId ?? reviews[0]?.rootRunId ?? runs[0].id;
      const run = await start(sessionID, target);
      if (activeSessionID.current !== sessionID) return;
      setRuns((current) => [
        run,
        ...current.filter((item) => item.id !== run.id),
      ]);
      setActiveRun(run.id);
    } catch (reason) {
      setTaskError(String(reason));
    } finally {
      setReviewBusy(false);
    }
  }
  return {
    attachments,
    addAttachments,
    removeAttachment,
    messages,
    hasEarlier,
    loadingEarlier,
    loadEarlier,
    approvalBusy,
    runs,
    activeRun,
    prompt,
    setPrompt: changePrompt,
    taskError,
    searchAvailability,
    searchRecords,
    searchBusy,
    sending,
    loadingSession,
    streamText,
    sessionSummary,
    sessionPlan,
    reviews,
    reviewBusy,
    taskGraph,
    childRuns,
    toggleSearch,
    pendingCalls,
    unknownCalls,
    toolHistory,
    workerTraces,
    workerBusy,
    traceError,
    loadWorkerTraces,
    approveCall,
    denyCall,
    resolveUnknown,
    clearPlan,
    openChildSession,
    sendTask,
    resumeTask,
    cancelTask,
    reviewResult: () => inspectResult(false),
    retrospectResult: () => inspectResult(true),
    retrospectives,
  };
}
export type ConversationState = ReturnType<typeof useConversation>;
