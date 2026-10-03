import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { newStatementParser, passwordLabel } from "../lib";
import type { PDFConfig, StatementParser } from "../types";

export function StatementParserForm({
  initial,
  config,
  saved,
  cancel,
  deleted,
}: {
  initial: StatementParser;
  config: PDFConfig;
  saved: (p: StatementParser) => void;
  cancel: () => void;
  deleted?: () => void;
}) {
  const [parser, setParser] = useState(initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  return (
    <form
      class="statement-parser-form"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        try {
          saved(await api<StatementParser>("statement-parsers", parser));
        } catch (e) {
          setError((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <label class="field">
        <span class="eyebrow">PDF parser name</span>
        <input
          required
          maxLength={100}
          value={parser.name}
          onInput={(e) => setParser((p) => ({ ...p, name: e.currentTarget.value }))}
        />
      </label>
      <label class="field">
        <span class="eyebrow">Statement layout</span>
        <select
          value={parser.adapter}
          onChange={(e) => {
            const adapter = e.currentTarget.value;
            setParser((p) => ({
              ...p,
              adapter,
              // A name that's still another layout's default follows the layout.
              name: config.adapters.some((a) => a.name === p.name)
                ? config.adapters.find((a) => a.id === adapter)?.name || p.name
                : p.name,
            }));
          }}
        >
          {config.adapters.map((a) => (
            <option value={a.id} key={a.id}>
              {a.name}
            </option>
          ))}
        </select>
        <span class="hint">
          {config.adapters.find((a) => a.id === parser.adapter)?.description}. Lines must add up to
          the statement’s totals; anything the layout doesn’t recognise is flagged, not guessed.
        </span>
      </label>
      <label class="field">
        <span class="eyebrow">PDF password</span>
        <select
          value={parser.password_slot}
          onChange={(e) =>
            setParser((p) => ({
              ...p,
              password_slot: Number(e.currentTarget.value),
            }))
          }
        >
          <option value={0}>Try all configured passwords</option>
          {config.password_slots.map((slot) => (
            <option value={slot} key={slot}>
              {passwordLabel(slot)}
            </option>
          ))}
          {parser.password_slot > 0 && !config.password_slots.includes(parser.password_slot) && (
            <option value={parser.password_slot}>
              Password {parser.password_slot} (not configured)
            </option>
          )}
        </select>
        <span class="hint">
          {config.password_slots.length
            ? "Only slot numbers are shown. Password values stay on the server."
            : "No passwords configured. Set LEDGER_PDF_PASSWORDS in Hoist for encrypted PDFs."}
        </span>
      </label>
      <label class="field">
        <span class="eyebrow">Final balance tolerance (paise)</span>
        <input
          type="number"
          min={0}
          max={99}
          step={1}
          required
          value={parser.balance_tolerance_paise ?? 99}
          onInput={(e) =>
            setParser((p) => ({
              ...p,
              balance_tolerance_paise: Number(e.currentTarget.value),
            }))
          }
        />
        <span class="hint">
          99 paise = ₹0.99. Applies only to the final balance. Transaction row totals must still
          match exactly.
        </span>
      </label>
      {error && (
        <p class="error-text" role="alert">
          {error}
        </p>
      )}
      <fieldset class="field triggers">
        <legend class="eyebrow">Automatic imports</legend>
        {parser.triggers?.length ? (
          parser.triggers.map((t, i) => (
            <div class="trigger" key={i}>
              <span>
                {t.sender}
                <span class="hint mono">
                  Subject {t.subject || "(any)"} · File {t.filename || "(any)"}
                </span>
              </span>
              <button
                type="button"
                class="btn"
                aria-label={`Stop importing from ${t.sender} automatically`}
                onClick={() =>
                  setParser((p) => ({
                    ...p,
                    triggers: (p.triggers || []).filter((_, j) => j !== i),
                  }))
                }
              >
                Remove
              </button>
            </div>
          ))
        ) : (
          <span class="hint">
            None. Import a statement by hand with “Import future statements like this automatically”
            ticked to add one.
          </span>
        )}
      </fieldset>
      <p class="hint">
        A parser is reusable across statements. Saving this configuration does not import
        transactions.
      </p>
      <div class="actions">
        <button class="btn primary" disabled={busy}>
          Save parser configuration
        </button>
        {parser.id > 0 && deleted && (
          <button
            type="button"
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
                await api(`statement-parsers/${parser.id}`, undefined, "DELETE");
                deleted();
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            Delete PDF parser
          </button>
        )}
        <button type="button" class="btn" disabled={busy} onClick={cancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
export function StatementParsers() {
  const [parsers, setParsers] = useState<StatementParser[]>([]),
    [config, setConfig] = useState<PDFConfig | null>(null),
    [editing, setEditing] = useState<StatementParser | null>(null),
    [error, setError] = useState("");
  useEffect(() => {
    Promise.all([api<StatementParser[]>("statement-parsers"), api<PDFConfig>("pdf-config")])
      .then(([p, c]) => {
        setParsers(p);
        setConfig(c);
      })
      .catch((e) => setError(e.message));
  }, []);
  return (
    <section class="statement-section">
      <div class="section-head">
        <span class="section-index">02</span>
        <h2>PDF statement parsers</h2>
        <div class="spacer" />
        <button
          class="btn"
          disabled={!config}
          onClick={() => setEditing({ ...newStatementParser })}
        >
          New PDF parser
        </button>
      </div>
      <p class="hint">
        Named, issuer-specific layouts. Import the first statement by hand from the Inbox; after
        that, statements from the same sender with a similar subject and file name import by
        themselves when they validate. Anything that doesn’t stays in the Inbox.
      </p>
      {error && (
        <p class="notice error" role="alert">
          {error}
        </p>
      )}
      {editing && config ? (
        <StatementParserForm
          key={editing.id}
          initial={editing}
          config={config}
          cancel={() => setEditing(null)}
          deleted={() => {
            setParsers((list) => list.filter((p) => p.id !== editing.id));
            setEditing(null);
          }}
          saved={(p) => {
            setParsers((list) => [...list.filter((v) => v.id !== p.id), p]);
            setEditing(null);
          }}
        />
      ) : (
        <div class="parser-grid">
          {parsers.map((p) => (
            <button class="parser-card" key={p.id} onClick={() => setEditing(p)}>
              <div class="eyebrow">PDF STATEMENT</div>
              <h3>{p.name}</h3>
              <p class="hint">{passwordLabel(p.password_slot)}</p>
              <p class="hint">
                {p.triggers?.length
                  ? `Imports automatically from ${[...new Set(p.triggers.map((t) => t.sender))].join(", ")}`
                  : "Imports by hand only"}
              </p>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
