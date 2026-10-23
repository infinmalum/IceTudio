import { useEffect, useRef, useState } from "react";
import type { ConversationState } from "../hooks/useConversation";
import { StatusService } from "../../bindings/icetudio";
import { Markdown } from "./Markdown";
import { ErrorNotice } from "../shared/errors";
import { downloadBase64, bytesToBase64 } from "../shared/download";
export function Transcript({
  conversation: c,
}: {
  conversation: Pick<
    ConversationState,
    | "messages"
    | "searchRecords"
    | "streamText"
    | "hasEarlier"
    | "loadingEarlier"
    | "loadEarlier"
  >;
}) {
  useEffect(() => {
    follow.current = true;
    setAway(false);
  }, [c.messages[0]?.sessionId]);
  const end = useRef<HTMLDivElement>(null);
  const follow = useRef(true);
  const [away, setAway] = useState(false);
  const [copied, setCopied] = useState("");
  const copyTimer = useRef<number | null>(null);
  useEffect(
    () => () => {
      if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
    },
    [],
  );
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<{
    name: string;
    url?: string;
    text?: string;
    mediaType: string;
  } | null>(null);
  useEffect(() => {
    const pane = document.querySelector(".chat-scroll");
    if (!pane) return;
    const scroll = () => {
      follow.current =
        pane.scrollHeight - pane.scrollTop - pane.clientHeight < 120;
      setAway(!follow.current);
    };
    pane.addEventListener("scroll", scroll);
    return () => pane.removeEventListener("scroll", scroll);
  }, []);
  useEffect(() => {
    if (follow.current) end.current?.scrollIntoView({ block: "end" });
  }, [c.messages, c.streamText]);
  async function openAttachment(id: string, index: number) {
    try {
      const f = await StatusService.MessageAttachment(id, index);
      setPreview({
        name: f.name,
        url: f.data ? `data:${f.mediaType};base64,${f.data}` : undefined,
        text: f.text,
        mediaType: f.mediaType,
      });
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function saveAttachment(id: string, index: number) {
    try {
      const f = await StatusService.MessageAttachment(id, index);
      const data =
        f.data || bytesToBase64(new TextEncoder().encode(f.text ?? ""));
      downloadBase64(
        data,
        f.mediaType === "text/plain" && f.name.endsWith(".docx")
          ? f.name + ".txt"
          : f.name,
        f.mediaType,
      );
    } catch (e) {
      setError(String(e));
    }
  }
  const previewBox = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (!preview) return;
    const focus = document.activeElement as HTMLElement | null;
    previewBox.current?.showModal();
    return () => {
      previewBox.current?.close();
      focus?.focus();
    };
  }, [preview]);
  const visible = c.messages.filter(
    (m) =>
      m.role !== "tool" &&
      (m.content.trim() || m.attachments?.length) &&
      !m.toolCalls?.length,
  );
  return (
    <>
      <div
        className="message-list"
        aria-live="polite"
        aria-relevant="additions text"
      >
        {c.hasEarlier && (
          <button
            className="secondary"
            disabled={c.loadingEarlier}
            onClick={async () => {
              const pane = document.querySelector(".chat-scroll");
              const before = pane?.scrollHeight ?? 0;
              follow.current = false;
              await c.loadEarlier();
              requestAnimationFrame(() => {
                if (pane) pane.scrollTop += pane.scrollHeight - before;
              });
            }}
          >
            {c.loadingEarlier ? "正在加载…" : "查看更早的消息"}
          </button>
        )}
        {visible.length === 0 && !c.streamText && (
          <div className="welcome">
            <div className="welcome-symbol">✳</div>
            <h1>有什么可以帮你？</h1>
            <p>聊一个问题，上传资料，或告诉我你想完成的事。</p>
          </div>
        )}
        {visible.map((m) => (
          <article className={`message message-${m.role}`} key={m.id}>
            <div className="message-role">
              {m.role === "user" ? "你" : "IceTudio"}
            </div>
            {m.role === "assistant" ? (
              <Markdown text={m.content} />
            ) : (
              <p className="user-text">{m.content}</p>
            )}
            {!!m.attachments?.length && (
              <ul className="attachment-list">
                {m.attachments.map((f, i) => (
                  <li key={i}>
                    <button
                      className="attachment-chip"
                      onClick={() => openAttachment(m.id, i)}
                      title="打开附件"
                    >
                      {f.name}
                    </button>
                    <button
                      className="icon-button"
                      aria-label={`下载 ${f.name}`}
                      onClick={() => saveAttachment(m.id, i)}
                    >
                      ↓
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {m.role === "assistant" && (
              <button
                className="message-copy"
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(m.content);
                    setCopied(m.id);
                    if (copyTimer.current !== null)
                      window.clearTimeout(copyTimer.current);
                    copyTimer.current = window.setTimeout(
                      () => setCopied(""),
                      1500,
                    );
                  } catch (e) {
                    setError(String(e));
                  }
                }}
              >
                {copied === m.id ? "已复制" : "复制回复"}
              </button>
            )}
          </article>
        ))}
        {c.streamText && (
          <article className="message message-assistant streaming">
            <div className="message-role">IceTudio · 正在回复</div>
            <Markdown text={c.streamText} />
          </article>
        )}
        <ErrorNotice error={error} />
        <div ref={end} />
      </div>
      {away && (
        <button
          className="jump-latest secondary"
          onClick={() => {
            follow.current = true;
            setAway(false);
            end.current?.scrollIntoView({ block: "end", behavior: "smooth" });
          }}
        >
          ↓ 回到最新消息
        </button>
      )}
      {preview && (
        <dialog
          ref={previewBox}
          onCancel={(e) => {
            e.preventDefault();
            setPreview(null);
          }}
          className="attachment-preview"
        >
          <div className="actions">
            <h2>{preview.name}</h2>
            <button className="secondary" onClick={() => setPreview(null)}>
              关闭预览
            </button>
          </div>
          {preview.name.toLowerCase().endsWith(".docx") && (
            <p>这里显示文档提取的正文；下载会得到文本文件。</p>
          )}
          {preview.mediaType.startsWith("image/") ? (
            <img src={preview.url} alt={preview.name} />
          ) : preview.mediaType === "application/pdf" ? (
            <object data={preview.url} type="application/pdf">
              <p>此窗口无法预览 PDF，可下载后打开。</p>
            </object>
          ) : (
            <pre>{preview.text}</pre>
          )}
        </dialog>
      )}
    </>
  );
}
