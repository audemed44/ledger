import { useEffect, useRef, useState } from "preact/hooks";
import { ArrowRight, Check, Pencil, SlidersHorizontal } from "lucide-preact";
import { api } from "../api";
import { dateLabel, money, syntheticAlert } from "../lib";
import type { Example, Mark, Message, Parser, Preview } from "../types";
import { ExampleTagger, missingFields } from "./ExampleTagger";
import { ErrorNote, Field } from "./ui";

export function AlertParserEditor({
  initial,
  message,
  close,
  saved,
}: {
  initial: Parser;
  message?: Message;
  close: () => void;
  saved: () => void;
}) {
  const [parser, setParser] = useState({ ...initial }),
    [body, setBody] = useState(message?.body || ""),
    [sender, setSender] = useState(message?.sender || initial.sender),
    [subject, setSubject] = useState(message?.subject || ""),
    [preview, setPreview] = useState<Preview | null>(null),
    [previewError, setPreviewError] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [savedID, setSavedID] = useState(0),
    [marks, setMarks] = useState<Mark[]>([]),
    [exampleError, setExampleError] = useState(""),
    [writingSample, setWritingSample] = useState(!message && !initial.pattern),
    [advanced, setAdvanced] = useState(!!initial.pattern);
  // The subject pattern last written from the example; one the user typed
  // isn't replaced.
  const writtenSubject = useRef("");
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  const input = JSON.stringify({
    parser,
    message_id: message?.id || 0,
    sender,
    subject,
    body,
  });
  useEffect(() => {
    setPreview(null);
    setPreviewError("");
    if (!body || !parser.pattern) return;
    let live = true;
    const timer = setTimeout(
      () =>
        api<Preview>("parsers/preview", JSON.parse(input))
          .then((p) => {
            if (live) setPreview(p);
          })
          .catch((e) => {
            if (live) setPreviewError(e.message);
          }),
      400,
    );
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [input]);
  // Tagging every required field writes the body pattern and date layout.
  useEffect(() => {
    setExampleError("");
    if (!marks.length || missingFields(marks).length) return;
    let live = true;
    api<Example>("parsers/from-example", { subject, body, marks })
      .then((e) => {
        if (!live) return;
        setParser((p) => {
          const keepSubject = p.subject && p.subject !== writtenSubject.current;
          writtenSubject.current = keepSubject ? writtenSubject.current : e.subject;
          return {
            ...p,
            pattern: e.pattern,
            date_layout: e.date_layout || p.date_layout,
            subject: keepSubject ? p.subject : e.subject,
          };
        });
        setSavedID(0);
      })
      .catch((e) => {
        if (live) setExampleError(e.message);
      });
    return () => {
      live = false;
    };
  }, [JSON.stringify(marks), body, subject]);
  const missing = missingFields(marks);
  function update<K extends keyof Parser>(key: K, value: Parser[K]) {
    setParser((p) => ({ ...p, [key]: value }));
    setSavedID(0);
  }
  async function save() {
    setBusy(true);
    setError("");
    try {
      const result = await api<Parser>("parsers", parser);
      setSavedID(result.id);
      setParser(result);
      await api("reprocess", {});
      saved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <dialog
      ref={dialog}
      onCancel={(e) => {
        if (busy) e.preventDefault();
        else close();
      }}
      class="editor"
    >
      <div class="dialog-head">
        <div>
          <div class="eyebrow">ALERT PARSER</div>
          <h2>{initial.id ? "Edit the rules." : "Teach your ledger."}</h2>
        </div>
        <button class="btn" onClick={close} disabled={busy}>
          Close
        </button>
      </div>
      <div class="editor-body">
        <div class="editor-fields">
          <div class="two-col">
            <Field label="Parser name">
              <input
                value={parser.name}
                onInput={(e) => update("name", e.currentTarget.value)}
                placeholder="Your bank"
              />
            </Field>
            <Field label="Exact sender address">
              <input
                value={parser.sender}
                onInput={(e) => update("sender", e.currentTarget.value)}
                placeholder="alerts@your-bank.com"
              />
            </Field>
          </div>
          <div class="two-col">
            <Field
              label="Issuer / bank"
              hint="Use the name statements use, so they match: HDFC, ICICI, Axis, IDFC or SBI."
            >
              <input
                value={parser.issuer || ""}
                placeholder="HDFC"
                onInput={(e) => update("issuer", e.currentTarget.value)}
              />
            </Field>
            <Field label="Account type">
              <select
                value={parser.account_kind || "unknown"}
                onChange={(e) => update("account_kind", e.currentTarget.value)}
              >
                <option value="card">Credit card</option>
                <option value="bank">Bank account</option>
                <option value="unknown">Unknown (legacy rule)</option>
              </select>
            </Field>
          </div>
          <Field
            label="Subject pattern"
            hint="Filled in from the example; numbers and months may change. Blank matches any subject."
          >
            <input
              class="mono"
              value={parser.subject}
              onInput={(e) => update("subject", e.currentTarget.value)}
              placeholder="(?i)purchase alert"
            />
          </Field>
          <div class="two-col">
            <Field label="Timezone">
              <input
                value={parser.timezone}
                onInput={(e) => update("timezone", e.currentTarget.value)}
              />
            </Field>
            <Field label="Default currency" hint="Used unless you tag a currency code.">
              <select
                value={parser.currency}
                onChange={(e) => update("currency", e.currentTarget.value)}
              >
                {["INR", "USD", "EUR", "GBP", "AUD", "CAD", "SGD", "AED", "CHF", "HKD"].map((c) => (
                  <option>{c}</option>
                ))}
              </select>
            </Field>
            <Field label="Default direction">
              <select
                value={parser.direction}
                onChange={(e) => update("direction", e.currentTarget.value)}
              >
                <option value="debit">Debit / purchase</option>
                <option value="credit">Credit / refund</option>
                <option value="ignore">Ignore (e.g. declined)</option>
              </select>
            </Field>
          </div>
          <details
            class="advanced"
            open={advanced}
            onToggle={(e) => setAdvanced(e.currentTarget.open)}
          >
            <summary>Advanced: pattern and date layout</summary>
            <Field
              label="Body pattern"
              hint="Written from your tags. A Go regular expression with named groups amount, merchant, account (last 4) and date; optionally currency, direction and reference."
            >
              <textarea
                class="mono pattern"
                value={parser.pattern}
                onInput={(e) => update("pattern", e.currentTarget.value)}
                spellcheck={false}
                placeholder="(?P<amount>...)"
              />
            </Field>
            <Field
              label="Date layout"
              hint="Go layout, worked out from the tagged date: 2-Jan-2006, 2006-1-2"
            >
              <input
                class="mono"
                value={parser.date_layout}
                onInput={(e) => update("date_layout", e.currentTarget.value)}
              />
            </Field>
          </details>
          <label class="check">
            <input
              type="checkbox"
              checked={parser.enabled}
              onChange={(e) => update("enabled", e.currentTarget.checked)}
            />{" "}
            Parser enabled
          </label>
        </div>
        <div class="editor-sample">
          <div class="eyebrow">
            <SlidersHorizontal size={14} /> SAMPLE & LIVE PREVIEW
          </div>
          {!message && (
            <div class="two-col">
              <Field label="Sample sender">
                <input value={sender} onInput={(e) => setSender(e.currentTarget.value)} />
              </Field>
              <Field label="Sample subject">
                <input value={subject} onInput={(e) => setSubject(e.currentTarget.value)} />
              </Field>
            </div>
          )}
          {message && (
            <div>
              <h3>{message.subject}</h3>
              <p class="hint">{message.sender}</p>
            </div>
          )}
          {writingSample ? (
            <>
              <Field label="Sample email text">
                <textarea
                  class="mono sample"
                  value={body}
                  onInput={(e) => {
                    setBody(e.currentTarget.value);
                    setMarks([]);
                  }}
                  spellcheck={false}
                  placeholder="Paste the text of an alert email, or use the synthetic example."
                />
              </Field>
              <div class="actions">
                <button class="btn" disabled={!body.trim()} onClick={() => setWritingSample(false)}>
                  Tag fields in this text <ArrowRight size={13} />
                </button>
                <button
                  class="text-link"
                  onClick={() => {
                    setBody(syntheticAlert.body);
                    setSender(syntheticAlert.sender);
                    setSubject(syntheticAlert.subject);
                    setMarks([]);
                    setWritingSample(false);
                    setParser((p) => ({
                      ...p,
                      name: p.name || "Example Bank",
                      sender: syntheticAlert.sender,
                    }));
                  }}
                >
                  Use synthetic example <ArrowRight size={13} />
                </button>
              </div>
            </>
          ) : body ? (
            <>
              <ExampleTagger body={body} marks={marks} setMarks={setMarks} />
              {!message && (
                <button class="text-link" onClick={() => setWritingSample(true)}>
                  <Pencil size={13} /> Change the sample text
                </button>
              )}
              {exampleError ? (
                <p class="error-text" role="alert">
                  {exampleError}
                </p>
              ) : (
                marks.length > 0 &&
                missing.length > 0 && (
                  <p class="hint">Still to tag: {missing.map((f) => f.label).join(", ")}.</p>
                )
              )}
            </>
          ) : (
            <p class="muted">
              No readable text was extracted from this email, so there is nothing to tag.
            </p>
          )}
          <div class="preview">
            <div class="eyebrow">EXTRACTED TRANSACTION</div>
            {previewError ? (
              <p class="error-text" role="status">
                {previewError}
              </p>
            ) : preview?.transaction ? (
              <>
                <div class="preview-amount">
                  {money(preview.transaction.amount, preview.transaction.currency)}
                </div>
                <h3>{preview.transaction.merchant}</h3>
                <p class="hint">
                  {preview.transaction.issuer} ·{" "}
                  {preview.transaction.account_kind === "bank"
                    ? "Bank account"
                    : preview.transaction.account_kind === "card"
                      ? "Credit card"
                      : "Account"}{" "}
                  · •• {preview.transaction.account}
                </p>
                <p class="hint">
                  {dateLabel(preview.transaction.date)} · {preview.transaction.direction}
                </p>
                <span class="chip accent">
                  <Check size={12} /> Ready to import
                </span>
              </>
            ) : (
              <p class="muted" role="status">
                {preview?.ignored
                  ? "This alert will be ignored and retained in the archive."
                  : preview && !preview.matched
                    ? "No match. Check the sender, subject and body pattern."
                    : "Tag the amount, merchant, card and date to see the transaction."}
              </p>
            )}
          </div>
          <p class="hint">
            Preview runs on your server. No mail is sent to external parsing services.
          </p>
        </div>
      </div>
      <div class="dialog-foot">
        <div>
          <ErrorNote error={error} />
          {savedID > 0 && error && (
            <span class="hint">
              Parser saved. Backlog retry failed; you can retry from the queue.
            </span>
          )}
          <span class="hint">
            Saving retries all queued mail. Previously imported transactions stay unchanged.
          </span>
        </div>
        {parser.id > 0 && (
          <button
            class="btn"
            disabled={busy}
            onClick={async () => {
              if (
                !window.confirm(
                  `Delete parser “${parser.name}”? Imported transactions and emails will be kept.`,
                )
              )
                return;
              setBusy(true);
              setError("");
              try {
                await api(`parsers/${parser.id}`, undefined, "DELETE");
                saved();
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            Delete alert parser
          </button>
        )}
        <button class="btn primary" disabled={busy} onClick={save}>
          {busy ? "Saving…" : "Save & retry backlog"}
          <ArrowRight size={15} />
        </button>
      </div>
    </dialog>
  );
}
