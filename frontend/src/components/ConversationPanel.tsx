import { useEffect, useRef, useState } from "react";
import type { Workspace } from "../hooks/useWorkspace";
import type { ConversationState } from "../hooks/useConversation";
import type { Configs } from "../hooks/useConfigs";
import type { RuntimeSettings } from "../hooks/useRuntimeSettings";
import { TaskProgress } from "./TaskProgress";
import { ToolApprovals } from "./ToolApprovals";
import { Transcript } from "./Transcript";
import { SearchRecords } from "./SearchRecords";
import { ConversationProfile } from "./ConversationProfile";
import { ErrorNotice, stateLabel } from "../shared/errors";
import { useDialog } from "../shared/dialogs";
import { StatusService } from "../../bindings/icetudio";
function Thumb({ file }: { file: File }) {
  const [url, setUrl] = useState("");
  useEffect(() => {
    if (!file.type.startsWith("image/")) return;
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);
  return url ? (
    <img className="attachment-thumb" src={url} alt={file.name} />
  ) : (
    <span aria-hidden>▤</span>
  );
}
export function ConversationPanel({
  workspace: w,
  conversation: c,
  configState: p,
  runtime: r,
  openModel,
  openAssistants,
}: {
  workspace: Workspace;
  conversation: ConversationState;
  configState: Configs;
  runtime: RuntimeSettings;
  openModel: () => void;
  openAssistants: () => void;
}) {
  const activeSession = useRef(w.selected);
  activeSession.current = w.selected;
  const dialog = useDialog();
  const form = useRef<HTMLFormElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const [folderBusy, setFolderBusy] = useState(false);
  const [folderError, setFolderError] = useState("");
  const [dragging, setDragging] = useState(false);
  const run = c.runs[0];
  const continuation =
    !c.activeRun &&
    c.taskGraph?.workflow === "fixed" &&
    c.taskGraph.rootRunId === run?.id &&
    ["waiting", "interrupted"].includes(run?.status ?? "") &&
    (c.taskGraph.tasks ?? []).every((t) =>
      ["pending", "succeeded"].includes(t.state),
    );
  const disabled =
    c.sending ||
    c.loadingSession ||
    c.configSwitchBusy ||
    w.creating ||
    !!c.activeRun ||
    w.activeConversation?.kind === "delegation" ||
    !w.selected ||
    !r.status?.storageReady ||
    (!r.status.runtimeReady && !continuation) ||
    (!continuation && w.activeConversation?.configArchived) ||
    (!c.prompt.trim() &&
      !c.attachments.length &&
      (!continuation || !!c.taskGraph?.waitingTaskId));
  async function chooseFolder() {
    if (!w.selected) return;
    setFolderBusy(true);
    try {
      const session = w.selected;
      const folder = await StatusService.PickFolder();
      if (!folder) return;
      if (
        !(await dialog.confirm({
          title: "允许访问这个文件夹？",
          body: (
            <>
              {" "}
              <p>{folder}</p>
              <p>
                本对话可在你批准具体操作后读取或修改其中的文件。助手仍需遵守现有权限限制。
              </p>
            </>
          ),
          confirm: "允许并添加到消息",
        }))
      )
        return;
      const context = await StatusService.GetLocalContext(w.contextID);
      await StatusService.UpdateContextPolicy(context.id, {
        ...context.policy,
        permissions: {
          ...context.policy.permissions,
          workspacePaths: [
            ...new Set([
              ...(context.policy.permissions.workspacePaths ?? []),
              folder,
            ]),
          ],
        },
      });
      if (session === activeSession.current)
        c.setPrompt(
          c.prompt +
            (c.prompt ? "\n" : "") +
            "请查看这个文件夹：" +
            folder,
        );
      setFolderError("");
    } catch (e) {
      setFolderError(String(e));
    } finally {
      setFolderBusy(false);
    }
  }
  return (
    <section className="chat-page">
      <header className="chat-header">
        <ConversationProfile
          workspace={w}
          conversation={c}
          configState={p}
          openAssistants={openAssistants}
        />
        <button className="model-switch secondary" onClick={openModel}>
          {r.status?.model.name || "连接模型"} ▾
        </button>
      </header>
      <div className="chat-scroll">
        <div className="chat-body">
          <Transcript conversation={c} />
          <ToolApprovals conversation={c} />
          <SearchRecords conversation={c} />
          <TaskProgress conversation={c} />
          <ErrorNotice error={c.taskError || folderError} />
          {run && ["failed", "blocked"].includes(run.status) && (
            <ErrorNotice error={run.error} />
          )}
        </div>
      </div>
      <div className="composer-wrap">
        {w.creating && (
          <p className="hint" role="status">
            正在创建新对话…
          </p>
        )}
        {c.configSwitchBusy && (
          <p className="hint" role="status">
            正在切换助手…
          </p>
        )}
        {c.loadingSession && <p className="hint">正在打开对话…</p>}
        {!w.selected && w.loaded && (
          <p className="hint">
            点击“新对话”开始；如果未能创建，可查看上方错误后重试。
          </p>
        )}
        {r.status?.storageReady && !r.status?.runtimeReady && (
          <div className="setup-banner">
            连接一个模型，就可以开始聊天。
            <button onClick={openModel}>连接模型</button>
          </div>
        )}
        {w.activeConversation?.configArchived && (
          <p className="hint">这个助手已删除，请在顶部选择另一个助手继续。</p>
        )}
        {continuation && (
          <div className="waiting-banner">
            <strong>
              {c.taskGraph?.waitingPrompt || "此前工作已中断，可以继续"}
            </strong>
            <p>补充后会接着原来的进度处理。</p>
          </div>
        )}
        <form
          ref={form}
          className={`composer ${dragging ? "dragging" : ""}`}
          onSubmit={(e) => {
            if (continuation) {
              e.preventDefault();
              void c.resumeTask();
            } else void c.sendTask(e);
          }}
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            if (!c.activeRun) c.addAttachments(e.dataTransfer.files);
          }}
        >
          {!!c.attachments.length && (
            <ul className="attachment-list draft-attachments">
              {c.attachments.map((file, i) => (
                <li key={i}>
                  <Thumb file={file} />
                  <span>{file.name}</span>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`移除 ${file.name}`}
                    onClick={() => c.removeAttachment(i)}
                  >
                    ×
                  </button>
                </li>
              ))}
            </ul>
          )}
          <textarea
            aria-label="发送消息"
            value={c.prompt}
            disabled={c.sending}
            placeholder={
              dragging ? "放开以上传文件" : "发消息，或粘贴网页链接…"
            }
            rows={3}
            onChange={(e) => c.setPrompt(e.target.value)}
            onPaste={(e) => {
              const images = Array.from(e.clipboardData.files).filter((f) =>
                f.type.startsWith("image/"),
              );
              if (images.length && !c.activeRun) {
                e.preventDefault();
                c.addAttachments(images);
              }
            }}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing &&
                e.keyCode !== 229
              ) {
                e.preventDefault();
                if (!disabled) form.current?.requestSubmit();
              }
            }}
          />
          <div className="composer-toolbar">
            <input
              hidden
              ref={input}
              aria-label="添加对话附件"
              type="file"
              multiple
              accept="image/png,image/jpeg,image/webp,image/gif,.pdf,.docx,.txt,.md,.csv,.json,.yaml,.yml,.xml,.html,.log,.py,.js,.ts,.go"
              onChange={(e) => {
                c.addAttachments(e.target.files);
                e.target.value = "";
              }}
            />
            <button
              type="button"
              className="secondary"
              disabled={!!c.activeRun || c.sending}
              onClick={() => input.current?.click()}
              title="添加图片、PDF、文档或文本"
            >
              ＋ 附件
            </button>
            <button
              type="button"
              className="secondary"
              disabled={!w.selected || folderBusy || !!c.activeRun || c.sending}
              onClick={chooseFolder}
            >
              选择文件夹
            </button>
            <label className="search-toggle">
              <input
                type="checkbox"
                checked={w.activeConversation?.options.webSearch ?? false}
                disabled={!w.selected || c.searchBusy || c.sending}
                onChange={(e) => c.toggleSearch(e.target.checked)}
              />
              联网
            </label>
            <span className="toolbar-spacer" />
            {c.activeRun ? (
              <button type="button" onClick={c.cancelTask}>
                停止
              </button>
            ) : (
              <button type="submit" disabled={disabled}>
                {c.sending ? "发送中…" : continuation ? "继续处理" : "发送 ↑"}
              </button>
            )}
          </div>
        </form>
        <div className="composer-caption">
          <span>
            {c.activeRun
              ? stateLabel(run?.status ?? "running")
              : "Enter 发送 · Shift+Enter 换行"}
          </span>
          <details>
            <summary>附件帮助</summary>
            <p>
              支持图片、PDF、DOCX
              和文本。最多8个，单个10MiB，总计20MiB；文本和文档提取正文最多256KiB。
            </p>
          </details>
        </div>
        {w.activeConversation?.options.webSearch &&
          c.searchAvailability?.available === false && (
            <p className="hint">
              此模型暂不支持联网。发送时可选择关闭联网继续聊天，或
              <button className="text-button" onClick={openModel}>
                更换模型
              </button>
              。
            </p>
          )}
        {continuation && (
          <button
            className="text-button"
            onClick={async () => {
              if (
                await dialog.confirm({
                  title: "重新开始这项工作？",
                  body: "这会从头执行，可能重复此前的文件修改。通常建议直接继续原任务。",
                  confirm: "开始新任务",
                })
              ) {
                const evt = {
                  preventDefault() {},
                } as React.FormEvent<HTMLFormElement>;
                void c.sendTask(evt);
              }
            }}
          >
            从头开始新任务
          </button>
        )}
      </div>
    </section>
  );
}
