import { useEffect, useState } from "react";
import type { SkillDrafts as DraftState } from "../hooks/useSkillDrafts";
import { TaskEvidence, type EvidenceOption } from "./TaskEvidence";
import { ErrorNotice, stateLabel } from "../shared/errors";
export function SkillDrafts({
  state: s,
  hasConversation,
  tasks = [],
  useMethod,
  skillNames = [],
}: {
  state: DraftState;
  hasConversation: boolean;
  tasks?: EvidenceOption[];
  useMethod?: (name: string) => Promise<void>;
  skillNames?: string[];
}) {
  const d = s.draft;
  const proposal = s.proposal;
  const unsaved =
    !!d &&
    !!proposal &&
    JSON.stringify(proposal) !== JSON.stringify(d.proposal);
  const header =
    proposal?.content.match(/^---\r?\n[\s\S]*?\r?\n---\r?\n/)?.[0] ?? "";
  const [devDraft, setDevDraft] = useState<string | null>(null);
  const [evaluationOutcome, setEvaluationOutcome] = useState("worked");
  const [evaluationNote, setEvaluationNote] = useState("");
  useEffect(() => {
    setDevDraft(null);
    setEvaluationNote("");
  }, [proposal]);
  useEffect(() => {
    setEvaluationOutcome("worked");
    setEvaluationNote("");
    void s.loadEvaluations(d?.published.name ?? "");
  }, [d?.id, d?.state, d?.published?.name]);
  const feedbackQuote = (text: string) =>
    text.length > 80 ? text.slice(0, 80) + "…" : text;
  return (
    <section className="session-card">
      <h2>教助手一种工作方法</h2>
      <p>
        例如固定的报告写法、资料检查步骤或你的表达习惯。生成后由你检查并保存。
      </p>
      <label>
        希望助手学会什么？
        <textarea
          rows={3}
          value={s.request}
          onChange={(e) => s.setRequest(e.target.value)}
        />
      </label>
      <TaskEvidence
        options={tasks}
        value={s.evidenceIDs}
        onChange={s.setEvidenceIDs}
      />
      {!!s.feedback.length && (
        <fieldset className="feedback-picker">
          <legend>本对话的反馈记录（原话）</legend>
          {s.feedback.map((n) => (
            <div key={n.id} className="feedback-row">
              {n.state === "recorded" ? (
                <label>
                  <input
                    type="checkbox"
                    checked={s.feedbackIDs.includes(n.id)}
                    onChange={(e) =>
                      s.setFeedbackIDs(
                        e.target.checked
                          ? [...s.feedbackIDs, n.id]
                          : s.feedbackIDs.filter((id) => id !== n.id),
                      )
                    }
                  />{" "}
                  {feedbackQuote(n.quote)}
                  {n.note && <span className="hint">（{n.note}）</span>}
                </label>
              ) : (
                <span className="hint">已取消：{feedbackQuote(n.quote)}</span>
              )}
              {n.state === "recorded" && (
                <button
                  className="secondary"
                  onClick={() => s.dismissFeedback(n.id)}
                >
                  取消记录
                </button>
              )}
            </div>
          ))}
        </fieldset>
      )}
      <button
        disabled={
          !hasConversation ||
          s.busy ||
          s.run?.status === "running" ||
          !s.request.trim()
        }
        onClick={s.generate}
      >
        {s.run?.status === "running" ? "正在准备…" : "生成工作方法"}
      </button>
      {!hasConversation && <p>先开始一个对话，选择要使用的模型。</p>}
      {s.run?.status === "running" && (
        <button className="secondary" onClick={s.cancel}>
          停止
        </button>
      )}
      <label>
        已保存的建议
        <select
          value={s.selected}
          onChange={(e) => s.setSelected(e.target.value)}
        >
          <option value="">选择建议</option>
          {s.drafts.map((v) => (
            <option key={v.id} value={v.id}>
              {v.proposal.summary} · {stateLabel(v.state)}
            </option>
          ))}
        </select>
      </label>
      {d && proposal && d.state === "declined" && (
        <>
          <p>
            一次性要求，未生成 Skill：{proposal.summary}
          </p>
          <ul>
            {(proposal.reasons ?? []).map((reason, i) => (
              <li key={i}>{reason}</li>
            ))}
          </ul>
          <p className="hint">
            原话保留在反馈记录里，可继续作为当前对话的具体要求使用。
          </p>
          {!!d.feedback?.length && (
            <p className="hint">
              依据反馈：{d.feedback.map((n) => feedbackQuote(n.quote)).join("；")}
            </p>
          )}
        </>
      )}
      {d && proposal && d.state !== "declined" && (
        <>
          <p>{proposal.summary}</p>
          <label>
            方法简介
            <textarea
              rows={2}
              disabled={d.state !== "draft"}
              value={proposal.summary}
              onChange={(e) =>
                s.setProposal({ ...proposal, summary: e.target.value })
              }
            />
          </label>
          <label>
            具体步骤
            <textarea
              rows={8}
              disabled={d.state !== "draft"}
              value={proposal.content.slice(header.length)}
              onChange={(e) =>
                s.setProposal({
                  ...proposal,
                  content: header + e.target.value,
                })
              }
            />
          </label>
          <p>不会自动执行这些步骤。采用前请检查私人信息和所需服务。</p>
          <details className="developer-settings">
            <summary>开发者设置 · 来源与完整内容</summary>
            <label>
              改进现有 Skill（留空新建）
              <select
                value={s.source}
                onChange={(e) => s.setSource(e.target.value)}
              >
                <option value="">新建</option>
                {skillNames.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
            <textarea
              rows={12}
              value={devDraft ?? JSON.stringify(proposal, null, 2)}
              onChange={(e) => {
                const text = e.target.value;
                setDevDraft(text);
                try {
                  s.setProposal(JSON.parse(text));
                } catch {
                  // Incomplete JSON just waits for the next keystroke; the
                  // typed proposal keeps its last valid value meanwhile.
                }
              }}
            />
            {devDraft !== null && !devDraft.trim() && (
              <p className="hint">内容为空，修改暂未生效。</p>
            )}
            <pre>
              {JSON.stringify(
                {
                  source: d.source,
                  impact: d.impact,
                  evidence: d.proposal.evidenceRefs,
                },
                null,
                2,
              )}
            </pre>
          </details>
          <div className="actions">
            {d.state === "draft" && (
              <>
                <button
                  className="secondary"
                  disabled={s.busy || !unsaved}
                  onClick={() => s.act("save")}
                >
                  保存修改
                </button>
                <button
                  disabled={s.busy || unsaved}
                  onClick={() => s.act("apply")}
                >
                  保存工作方法
                </button>
                <button className="secondary" onClick={() => s.act("discard")}>
                  丢弃
                </button>
              </>
            )}
            {d.state === "applied" && (
              <>
                <button onClick={() => useMethod?.(d.published.name)}>
                  用于正在编辑的助手
                </button>
                <button className="secondary" onClick={() => s.act("withdraw")}>
                  撤销此方法
                </button>
              </>
            )}
          </div>
          {d.state === "applied" && (
            <fieldset className="evaluation-recorder">
              <legend>音乐任务效果评估（由你实测后记录）</legend>
              {!!s.evaluations.length && (
                <p className="hint">
                  最近评估：
                  {
                    s.evaluations[0].outcome === "worked"
                      ? "有效"
                      : s.evaluations[0].outcome === "partial"
                        ? "部分有效"
                        : "未达预期"
                  }
                  {s.evaluations[0].note ? ` · ${s.evaluations[0].note}` : ""}（共 {s.evaluations.length} 次）
                </p>
              )}
              {!s.evaluations.length && (
                <p className="hint">未验证：还没有评估记录；修改方法不代表质量已提升。</p>
              )}
              <div className="actions">
                <select
                  value={evaluationOutcome}
                  onChange={(e) => setEvaluationOutcome(e.target.value)}
                >
                  <option value="worked">有效</option>
                  <option value="partial">部分有效</option>
                  <option value="failed">未达预期</option>
                </select>
                <input
                  type="text"
                  placeholder="评估备注（可选）"
                  value={evaluationNote}
                  onChange={(e) => setEvaluationNote(e.target.value)}
                />
                <button
                  className="secondary"
                  disabled={s.busy}
                  onClick={() =>
                    s.recordEvaluation(
                      d.published.name,
                      evaluationOutcome,
                      evaluationNote,
                    )
                  }
                >
                  记录本次评估
                </button>
              </div>
            </fieldset>
          )}
        </>
      )}
      <ErrorNotice error={s.error} />
    </section>
  );
}
