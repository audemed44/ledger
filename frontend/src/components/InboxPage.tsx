import { useEffect, useRef, useState } from "preact/hooks";
import { ArrowRight, Mail, RefreshCw, Upload } from "lucide-preact";
import { api, upload } from "../api";
import { dateLabel } from "../lib";
import type { Message } from "../types";
import { Empty, ErrorNote, Section } from "./ui";

export function InboxPage({
  version,
  refresh,
  edit,
  parserCount,
}: {
  parserCount: number;
  version: number;
  refresh: () => void;
  edit: (m: Message) => void;
}) {
  const [kind, setKind] = useState("all");
  const [rows, setRows] = useState<Message[] | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState("");
  // Background refreshes keep the list on screen, so the page doesn't jump;
  // only a new filter starts from "Loading".
  const shownKind = useRef(kind);
  useEffect(() => {
    let live = true;
    if (shownKind.current !== kind) {
      shownKind.current = kind;
      setRows(null);
    }
    setError("");
    api<Message[]>(kind === "all" ? "messages" : `messages?kind=${kind}`)
      .then((rows) => {
        if (live) setRows(rows);
      })
      .catch((e) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
    };
  }, [version, kind]);
  return (
    <section>
      <Section index="01" title="Needs review">
        <button
          class="btn"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError("");
            try {
              const result = await api<{ processed: number }>("reprocess", {});
              setNotice(`Retried ${result.processed} messages.`);
              refresh();
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <RefreshCw size={14} class={busy ? "spin" : ""} /> Retry backlog
        </button>
        <label class={"btn" + (busy ? " disabled" : "")}>
          <Upload size={14} /> Upload PDF
          <input
            type="file"
            accept=".pdf,application/pdf"
            class="visually-hidden"
            disabled={busy}
            onChange={async (e) => {
              const file = e.currentTarget.files?.[0];
              e.currentTarget.value = "";
              if (!file) return;
              setBusy(true);
              setError("");
              try {
                const result = await upload<{ id: number; state: string; message: string }>(
                  "statements/upload",
                  file,
                );
                setNotice(result.message);
                refresh();
                if (result.state === "queued") edit(await api<Message>("messages/" + result.id));
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          />
        </label>
      </Section>
      <p class="hint">
        For banks that only email a download link: download the statement, then upload it here or
        send it from Foyer’s Drop.
      </p>
      <label class="field">
        <span class="eyebrow">Inbox filter</span>
        <select value={kind} onChange={(e) => setKind(e.currentTarget.value)}>
          <option value="all">All emails</option>
          <option value="pdf">PDF statements</option>
          <option value="text">Text/HTML alerts</option>
          <option value="dismissed">Dismissed</option>
        </select>
      </label>
      <p class="hint">
        Newest email date first. Emails with PDFs appear under PDF statements even when they also
        contain covering text.
      </p>
      {parserCount === 0 && (
        <div class="notice">
          Mail is arriving, but no alert parsers have been configured yet. Open Review to read an
          email, then choose Create alert parser. PDF statements use a separate parser.
        </div>
      )}
      <ErrorNote error={error} />
      {notice && (
        <p class="notice" role="status">
          {notice}
        </p>
      )}
      {rows === null ? (
        <p class="loading">Loading queue…</p>
      ) : rows.length ? (
        <div class="queue-list">
          {rows.map((m) => (
            <article class="queue-row">
              <div class="queue-icon">
                <Mail size={20} />
              </div>
              <div>
                <h3>{m.subject || "(No subject)"}</h3>
                <div class="hint">
                  {m.sender} · {dateLabel(m.date)}
                </div>
                <p class="queue-reason">{m.reason || "No matching alert parser"}</p>
              </div>
              <div class="queue-actions">
                <button
                  class="btn"
                  disabled={busy}
                  onClick={async () => {
                    const dismissed = m.state !== "dismissed";
                    setError("");
                    try {
                      await api(`messages/${m.id}/dismiss`, { dismissed });
                      setNotice(
                        dismissed ? `Dismissed “${m.subject}”.` : `Restored “${m.subject}”.`,
                      );
                      refresh();
                    } catch (e) {
                      setError((e as Error).message);
                    }
                  }}
                >
                  {m.state === "dismissed" ? "Restore" : "Dismiss"}
                </button>
                <button
                  class="btn"
                  onClick={async () => {
                    try {
                      edit(await api<Message>("messages/" + m.id));
                    } catch (e) {
                      setError((e as Error).message);
                    }
                  }}
                >
                  Review <ArrowRight size={14} />
                </button>
              </div>
            </article>
          ))}
          <p class="hint">
            Showing up to 200 matching queued messages, newest first. Retry backlog processes all
            queued mail.
          </p>
        </div>
      ) : (
        <Empty title="Nothing waiting in the wings.">
          Unmatched emails will appear here, with their original text safely archived.
        </Empty>
      )}
    </section>
  );
}
