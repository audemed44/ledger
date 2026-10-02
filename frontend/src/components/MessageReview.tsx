import { useEffect, useRef } from "preact/hooks";
import { ArrowRight, FileText } from "lucide-preact";
import { dateLabel } from "../lib";
import type { Message } from "../types";
import { PDFReview } from "./PDFReview";

export function MessageReview({
  message,
  close,
  createParser,
  imported,
}: {
  message: Message;
  close: () => void;
  createParser: () => void;
  imported?: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog
      ref={dialog}
      class="editor message-review"
      aria-labelledby="message-title"
      onCancel={close}
    >
      <div class="dialog-head">
        <div>
          <div class="eyebrow">IMPORTED EMAIL</div>
          <h2 id="message-title">{message.subject || "(No subject)"}</h2>
        </div>
        <button class="btn" onClick={close}>
          Close
        </button>
      </div>
      <div class="message-content">
        <div class="message-meta">
          <span>{message.sender || "Unknown sender"}</span>
          <time dateTime={message.date}>{dateLabel(message.date)}</time>
        </div>
        <div class="message-status">
          <span class="chip">
            {message.has_pdf
              ? "PDF statement"
              : message.can_parse
                ? "Awaiting alert parser"
                : "Needs attention"}
          </span>
          <span class="hint">
            {message.body_format === "html"
              ? "Text extracted from HTML · images and links are not loaded"
              : message.body_format === "plain"
                ? "Plain-text email"
                : ""}
          </span>
        </div>
        {message.content_error && (
          <div class="notice error" role="alert">
            {message.content_error}
          </div>
        )}
        {message.has_pdf && <PDFReview message={message} imported={imported} />}
        {message.body?.trim() ? (
          <label class="field">
            <span class="eyebrow">{message.has_pdf ? "Covering email text" : "Email text"}</span>
            <textarea class="mono message-text" readOnly value={message.body} spellcheck={false} />
          </label>
        ) : (
          <div class="empty">
            <h3>No readable email text</h3>
            <p>
              {message.has_pdf
                ? "This message may contain only its statement attachment. The original email and attachment are archived."
                : "The email has been archived, but readable text could not be extracted. Retry the backlog after restoring a missing archive or updating extraction support."}
            </p>
          </div>
        )}
        {!!message.attachments?.length && (
          <section class="message-attachments" aria-label="Attachments">
            <h3>Attachments</h3>
            {message.attachments.map((attachment, i) => (
              <div key={i}>
                <FileText size={16} />
                <span>
                  {attachment.name}
                  <span class="hint">
                    {attachment.content_type || "Unknown file type"} · archived
                  </span>
                </span>
              </div>
            ))}
          </section>
        )}
      </div>
      <div class="dialog-foot">
        <p class="hint">
          {message.can_parse
            ? "Create a rule for this bank’s alert format. Preview it before importing transactions."
            : "Your original email remains safely archived on the server."}
        </p>
        {message.can_parse && (
          <button class="btn primary" onClick={createParser}>
            Create alert parser <ArrowRight size={15} />
          </button>
        )}
      </div>
    </dialog>
  );
}
