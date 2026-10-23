import { useEffect, useState } from "react";
import { useSendConsent } from "../shared/consent";
import { useDialog } from "../shared/dialogs";
import { StatusService } from "../../bindings/icetudio";
import type {
  SkillDraft,
  SkillProposal,
  Run,
} from "../../bindings/icetudio/internal/storage";

export function useSkillDrafts(sessionID: string | null) {
  const ensureConsent = useSendConsent();
  const dialog = useDialog();
  const [drafts, setDrafts] = useState<SkillDraft[]>([]);
  const [request, setRequest] = useState("");
  const [source, setSource] = useState("");
  const [evidenceIDs, setEvidenceIDs] = useState("");
  const [selected, chooseSelected] = useState("");
  // The proposal is edited as a typed object; raw JSON is only an advanced
  // view. This keeps broken JSON from silently disabling the friendly fields.
  const [proposal, setProposal] = useState<SkillProposal | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const draft = drafts.find((item) => item.id === selected);
  async function setSelected(id: string) {
    if (
      draft &&
      proposal &&
      JSON.stringify(proposal) !== JSON.stringify(draft.proposal) &&
      !(await dialog.confirm({
        title: "放弃尚未保存的建议修改？",
        body: "先保存修改可以继续保留它。",
        confirm: "放弃并切换",
        danger: true,
      }))
    )
      return;
    chooseSelected(id);
  }
  async function refresh() {
    const items = await StatusService.ListSkillDrafts();
    setDrafts(items ?? []);
    return items ?? [];
  }
  useEffect(() => {
    refresh().catch((e) => setError(String(e)));
  }, []);
  useEffect(() => {
    setEvidenceIDs("");
  }, [sessionID]);
  useEffect(() => {
    setProposal(draft ? structuredClone(draft.proposal) : null);
  }, [draft?.id, draft?.revision]);
  useEffect(() => {
    if (!run || run.status !== "running") return;
    let current = true;
    let querying = false;
    const timer = window.setInterval(async () => {
      if (querying) return;
      querying = true;
      try {
        const next = await StatusService.GetRun(run.id);
        if (!current || next.status === "running") return;
        const items = await StatusService.ListSkillDrafts();
        if (!current) return;
        setDrafts(items ?? []);
        setSelected(
          (items ?? []).find((item) => item.runId === next.id)?.id ?? "",
        );
        setRun(next);
        if (next.status !== "completed") setError(next.error || next.status);
      } catch (e) {
        if (current) setError(String(e));
      } finally {
        querying = false;
      }
    }, 700);
    return () => {
      current = false;
      window.clearInterval(timer);
    };
  }, [run?.id, run?.status]);
  async function generate() {
    if (!sessionID) return;
    setBusy(true);
    setError("");
    try {
      if (!(await ensureConsent(sessionID, false, "improver"))) return;
      setRun(
        await StatusService.StartSkillDraft(
          sessionID,
          source.trim(),
          request,
          evidenceIDs.split(/[\s,]+/).filter(Boolean),
        ),
      );
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  async function act(action: "save" | "apply" | "discard" | "withdraw") {
    if (!draft) return;
    setBusy(true);
    setError("");
    try {
      if (action === "save") {
        if (!proposal) return;
        if (!proposal.content.trim()) {
          setError("请先填写工作方法的内容。");
          return;
        }
        await StatusService.EditSkillDraft(draft.id, draft.revision, proposal);
      } else if (action === "apply") {
        await StatusService.ApplySkillDraft(draft.id, draft.revision);
      } else if (action === "withdraw") {
        await StatusService.WithdrawSkillDraft(draft.id, draft.revision);
      } else {
        await StatusService.DiscardSkillDraft(draft.id, draft.revision);
      }
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  async function cancel() {
    if (!run) return;
    try {
      await StatusService.CancelTask(run.id);
    } catch (e) {
      setError(String(e));
    }
  }
  return {
    drafts,
    draft,
    request,
    setRequest,
    source,
    setSource,
    evidenceIDs,
    setEvidenceIDs,
    selected,
    setSelected,
    proposal,
    setProposal,
    run,
    busy,
    error,
    generate,
    act,
    cancel,
    refresh,
  };
}
export type SkillDrafts = ReturnType<typeof useSkillDrafts>;
