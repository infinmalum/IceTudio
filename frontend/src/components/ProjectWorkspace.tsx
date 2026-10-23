import { ProjectEditor } from "./ProjectEditor";
import { useDialog } from "../shared/dialogs";
import { useState } from "react";
import type { useProjects } from "../hooks/useProjects";
import { MusicInputs } from "../hooks/useMusicInputs";

type Projects = ReturnType<typeof useProjects>;

// Project lifecycle lives in the left sidebar; the center stage is editor-only.
export function ProjectWorkspace({
  projects,
}: {
  projects: Projects;
}) {
  const p = projects;
  const dialog = useDialog();
  const [draftBusy, setDraftBusy] = useState(false);
  const savedDraft = p.timeMapDrafts.snapshot;
  const active = p.active;
  return (
    <MusicInputs.Provider value={p.inputs}>
      <header className="project-header">
        <div>
          <p className="eyebrow">音乐工程</p>
          <h1>{active ? active.title : "未选择工程"}</h1>
          {active ? (
            <p className="project-meta">
              修订 {active.revision} · PPQ {active.ppq} · 更新于{" "}
              {active.updatedAt.slice(0, 19).replace("T", " ")}
            </p>
          ) : (
            <p className="project-meta">新建或导入一个工程开始工作</p>
          )}
        </div>
        <span className="project-badge">
          MIDI 编辑 · 原生试听
        </span>
      </header>
      <div className="project-canvas">
        <div className="project-strip">
          <div className="project-actions">
            <button
              className="secondary"
              onClick={p.exportMIDI}
              disabled={p.busy || !active}
            >
              导出 MIDI
            </button>
            <button
              className="secondary"
              onClick={p.rename}
              disabled={p.busy || !active}
            >
              重命名
            </button>
            <button
              className="secondary"
              onClick={() => p.shift("undo")}
              disabled={p.busy || !active}
            >
              撤销
            </button>
            <button
              className="secondary"
              onClick={() => p.shift("redo")}
              disabled={p.busy || !active}
            >
              重做
            </button>
          </div>
          {p.error ? (
            <p className="project-error" role="alert">
              {p.error}
            </p>
          ) : null}
        </div>
        <div className="project-save" role="status">{p.saveStatus || (active ? "工程自动保存" : "新建或导入 MIDI 开始编辑")}</div>
        {!p.recoveryReady && <div role="alert">{p.recoveryError || "正在检查工程恢复记录…"}<button className="secondary" onClick={() => void p.checkRecovery()}>重试恢复检查</button></div>}
        {p.pending && <button className="secondary" disabled={!p.recoveryReady} onClick={p.resolvePending}>查询保存结果</button>}
        {p.inputs.error && <p role="alert">{p.inputs.error}<button onClick={p.inputs.retry}>重试音乐输入保存/读取</button></p>}
        {p.inputs.state.entries.length > 0 && <section aria-label="已保留音乐输入">
          <p>以下输入尚未应用，恢复后仍须确认；原工程与修订保持不变。</p>
          {p.inputs.state.entries.map(entry => <div key={entry.key}>
            <span>「{entry.title}」· {entry.label} · 基准修订 {entry.target.revision}</span>
            <button className="secondary" disabled={p.busy || !p.projects.some(item => item.id === entry.target.id)} onClick={() => { p.select(entry.target.id); p.inputs.restore(entry); }}>恢复输入</button>
            {!p.projects.some(item => item.id === entry.target.id) && <span>原工程已删除；草稿仍保留。</span>}
            <button className="secondary" disabled={p.busy} onClick={() => void dialog.confirm({ title: "丢弃这项音乐输入？", body: `只丢弃「${entry.label}」尚未应用的值，不改变工程。`, confirm: "丢弃输入", cancel: "保留" }).then(ok => { if (ok) p.inputs.clear(entry.key); })}>丢弃输入</button>
          </div>)}
        </section>}
        {!p.timeMapDrafts.ready && <div role="alert">{p.timeMapDrafts.error || "正在检查时间图草稿…"}<button className="secondary" onClick={() => void p.timeMapDrafts.check()}>重试草稿检查</button></div>}
        {p.timeMapDrafts.ready && p.timeMapDrafts.error && <div role="alert">{p.timeMapDrafts.error}<button className="secondary" disabled={p.timeMapDrafts.writing} onClick={() => void p.timeMapDrafts.retry().catch(p.reportError)}>重试保留草稿</button></div>}
        {savedDraft && <section className="map-draft-recovery" aria-label="已保留时间图草稿">
          <p>工程「{savedDraft.title || "未命名工程"}」有已保留的时间图草稿 · 基准修订 {savedDraft.baseRevision}。恢复不会自动应用；如有待确认操作，请先查询原保存结果。</p>
          {p.projects.some(item => item.id === savedDraft.projectID)
            ? <button className="secondary" disabled={p.busy || draftBusy || p.timeMapDrafts.writing} onClick={() => p.select(savedDraft.projectID)}>前往草稿工程</button>
            : <p role="alert">原工程已删除，无法恢复到该工程；记录仍保留，可明确丢弃。</p>}
          <button className="secondary" disabled={draftBusy || p.busy || p.timeMapDrafts.writing} onClick={() => {
            setDraftBusy(true);
            void (async () => {
              if (await dialog.confirm({ title: "丢弃已保留的时间图草稿？", body: `仅丢弃工程「${savedDraft.title}」尚未应用的时间图输入，不删除工程或编辑历史。`, confirm: "丢弃草稿", cancel: "保留草稿", danger: true })) await p.timeMapDrafts.clear();
            })().catch(p.reportError).finally(() => setDraftBusy(false));
          }}>丢弃已保留草稿</button>
        </section>}
        {active ? <>
          <button className="secondary project-reload" onClick={p.reload}>刷新工程</button>
          <ProjectEditor key={active.id} project={active} projects={p}/>
        </> : <div className="project-empty">
          <div className="project-symbol" aria-hidden>♫</div>
          <h2>{p.activeID ? "正在读取工程…" : "从一个片段开始创作"}</h2>
          <p>从左侧新建工程后添加音轨，在钢琴卷帘中编辑音符；也可以导入 MIDI 继续编曲。编辑自动保存，支持撤销/重做。选择工程后可用原生播放与基础音色试听。</p>
        </div>}

      </div>
    </MusicInputs.Provider>
  );
}
