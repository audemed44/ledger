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
    if (!marks.length || missingFields(marks, parser.description).length) return;
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
  }, [JSON.stringify(marks), body, subject, !!parser.description?.trim()]);
  const missing = missingFields(marks, parser.description);
  function update<K extends keyof Parser>(key: K, value: Parser[K]) {
    setParser((p) => ({ ...p, [key]: value }));
    setSavedID(0);
  }
  // A saved parser's emails can be re-read, so a fix (say, debit to
  // credit) also corrects what it already recorded.
  const [handled, setHandled] = useState<{ emails: number; confirmed: number } | null>(null),
    [reread, setReread] = useState(true);
  useEffect(() => {
    if (!initial.id) return;
    api<{ emails: number; confirmed: number }>(`parsers/${initial.id}/handled`)
      .then(setHandled)
      .catch(() => setHandled(null));
  }, [initial.id]);
  const rereading = reread && !!handled?.emails;
  // Parsers for the same sender: a new email's wording can join one, and
  // a saved parser can absorb another.
  const [siblings, setSiblings] = useState<Parser[]>([]),
    [target, setTarget] = useState(0),
    [mergeFrom, setMergeFrom] = useState(0);
  useEffect(() => {
    api<Parser[]>("parsers")
      .then((all) =>
        setSiblings(
          all.filter(
            (p) =>
              p.id !== initial.id &&
              p.direction !== "ignore" &&
              p.sender.toLowerCase() === (message?.sender || initial.sender).toLowerCase(),
          ),
        ),
      )
      .catch(() => setSiblings([]));
  }, [initial.id]);
  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      await api("reprocess", {});
      saved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function save() {
    if (target) {
      // Another wording of an existing parser.
      return run(() =>
        api(`parsers/${target}/wordings`, {
          pattern: parser.pattern,
          date_layout: parser.date_layout,
          account_kind: parser.account_kind,
        }),
      );
    }
    setBusy(true);
    setError("");
    try {
      const result = await api<Parser>(rereading ? "parsers?reread=1" : "parsers", parser);
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
          {!initial.id && siblings.length > 0 && (
            <Field
              label="Save as"
              hint="The bank words the same alert in several ways? Add this wording to its parser instead of making another."
            >
              <select value={target} onChange={(e) => setTarget(Number(e.currentTarget.value))}>
                <option value={0}>A new parser</option>
                {siblings.map((p) => (
                  <option value={p.id} key={p.id}>
                    Another wording of “{p.name}”
                  </option>
                ))}
              </select>
            </Field>
          )}
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
                <option value="debit">Debit card (recorded on its bank account)</option>
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
          <Field
            label="Description when the email names no merchant"
            hint="For alerts like “Payment received on your card”: leave Merchant untagged. A statement line that confirms the transaction replaces it."
          >
            <input
              value={parser.description || ""}
              maxLength={100}
              placeholder="Payment received"
              onInput={(e) => update("description", e.currentTarget.value)}
            />
          </Field>
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
          {!!parser.wordings?.length && (
            <fieldset class="field triggers">
              <legend class="eyebrow">Other wordings ({parser.wordings.length})</legend>
              {parser.wordings.map((w, i) => (
                <div class="trigger" key={i}>
                  <span class="mono">
                    {w.pattern.length > 90 ? w.pattern.slice(0, 90) + "…" : w.pattern}
                    <span class="hint">
                      Date {w.date_layout}
                      {w.account_kind && w.account_kind !== parser.account_kind
                        ? ` · ${w.account_kind === "bank" ? "bank account" : w.account_kind === "debit" ? "debit card" : w.account_kind}`
                        : ""}
                    </span>
                  </span>
                  <button
                    type="button"
                    class="btn"
                    onClick={() =>
                      update(
                        "wordings",
                        (parser.wordings || []).filter((_, j) => j !== i),
                      )
                    }
                  >
                    Remove
                  </button>
                </div>
              ))}
            </fieldset>
          )}
          {!!initial.id && siblings.length > 0 && (
            <div class="merge">
              <Field
                label="Merge another parser into this one"
                hint="Its wordings move here and it's deleted; transactions stay. Same sender, issuer and direction only."
              >
                <select
                  value={mergeFrom}
                  onChange={(e) => setMergeFrom(Number(e.currentTarget.value))}
                >
                  <option value={0}>Choose a parser</option>
                  {siblings.map((p) => (
                    <option value={p.id} key={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </Field>
              <button
                type="button"
                class="btn"
                disabled={busy || !mergeFrom}
                onClick={() => run(() => api(`parsers/${initial.id}/merge`, { from: mergeFrom }))}
              >
                Merge
              </button>
            </div>
          )}
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
                      : preview.transaction.account_kind === "debit"
                        ? "Debit card"
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
          {handled && handled.emails > 0 ? (
            <label class="check">
              <input
                type="checkbox"
                checked={reread}
                disabled={busy}
                onChange={(e) => setReread(e.currentTarget.checked)}
              />
              Re-read the {handled.emails} email{handled.emails === 1 ? "" : "s"} this parser
              already handled, so they follow this change
              {handled.confirmed > 0 &&
                ` (${handled.confirmed} confirmed by a statement stay as the statement has them)`}
            </label>
          ) : (
            <span class="hint">
              Saving retries all queued mail. Previously imported transactions stay unchanged.
            </span>
          )}
        </div>
        {parser.id > 0 && (
          <button
            class="btn"
            disabled={busy}
            onClick={async () => {
              if (
                !window.confirm(
                  rereading
                    ? `Delete parser “${parser.name}” and undo what it recorded? Its emails go back to the inbox (or to another parser that matches); transactions a statement confirmed are kept.`
                    : `Delete parser “${parser.name}”? Imported transactions and emails will be kept.`,
                )
              )
                return;
              setBusy(true);
              setError("");
              try {
                await api(
                  `parsers/${parser.id}${rereading ? "?reread=1" : ""}`,
                  undefined,
                  "DELETE",
                );
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
