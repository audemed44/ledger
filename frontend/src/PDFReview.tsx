import { useEffect, useState } from "preact/hooks";
import { api, money, dateLabel } from "./api";
import type { Message, Transaction } from "./api";
import {
  StatementParserForm,
  newStatementParser,
  passwordLabel,
} from "./StatementParsers";
import type { StatementParser, PDFConfig } from "./StatementParsers";

type PDFResult = {
  text: string;
  password_slot: number;
  parser_name?: string;
  parse_error?: string;
  statement?: {
    transactions: Transaction[];
    issuer: string;
    account: string;
    account_kind: string;
    date: string;
    due_date: string;
    minimum_due: number;
    opening: number;
    total_due: number;
    balanced: boolean;
    rounding_accepted?: boolean;
    balance_tolerance_paise?: number;
    discrepancy: number;
    warnings: string[];
  };
};
export function PDFReview({ message }: { message: Message }) {
  const attachments = (message.attachments || []).filter(
    (a) =>
      a.content_type === "application/pdf" ||
      a.name.toLowerCase().endsWith(".pdf"),
  );
  const [part, setPart] = useState(attachments[0]?.part ?? -1),
    [parserID, setParserID] = useState(0),
    [parsers, setParsers] = useState<StatementParser[]>([]),
    [config, setConfig] = useState<PDFConfig | null>(null),
    [creating, setCreating] = useState(false),
    [result, setResult] = useState<PDFResult | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    Promise.all([
      api<StatementParser[]>("statement-parsers"),
      api<PDFConfig>("pdf-config"),
    ])
      .then(([p, c]) => {
        if (live) {
          setParsers(p);
          setConfig(c);
          if (p.length) setParserID(p[0].id);
        }
      })
      .catch((e) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
    };
  }, []);
  return (
    <section class="pdf-review" aria-label="PDF statement review">
      <h3>Read the PDF</h3>
      <div class="two-col">
        <label class="field">
          <span class="eyebrow">PDF attachment</span>
          <select
            value={part}
            disabled={busy}
            onChange={(e) => {
              setPart(Number(e.currentTarget.value));
              setResult(null);
            }}
          >
            {attachments.map((a) => (
              <option key={a.part} value={a.part}>
                {a.name}
              </option>
            ))}
          </select>
        </label>
        <label class="field">
          <span class="eyebrow">PDF parser</span>
          <select
            value={parserID}
            disabled={busy}
            onChange={(e) => {
              setParserID(Number(e.currentTarget.value));
              setResult(null);
            }}
          >
            <option value={0}>Extract text only (try all passwords)</option>
            {parsers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} · {passwordLabel(p.password_slot)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {config && !config.password_slots.length && (
        <p class="hint">
          For encrypted statements, set LEDGER_PDF_PASSWORDS in Hoist, with
          passwords separated by |, then redeploy.
        </p>
      )}
      {creating && config ? (
        <StatementParserForm
          initial={{ ...newStatementParser }}
          config={config}
          cancel={() => setCreating(false)}
          saved={(p) => {
            setParsers((list) => [...list, p]);
            setParserID(p.id);
            setResult(null);
            setCreating(false);
          }}
        />
      ) : (
        <div class="actions">
          <button
            class="btn primary"
            disabled={busy || part < 0 || !config}
            onClick={async () => {
              setBusy(true);
              setError("");
              setResult(null);
              try {
                setResult(
                  await api<PDFResult>(`messages/${message.id}/pdf`, {
                    part,
                    parser_id: parserID,
                  }),
                );
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy
              ? "Opening PDF…"
              : parserID
                ? "Extract & validate PDF"
                : "Extract PDF text"}
          </button>
          <button
            class="btn"
            disabled={busy || !config}
            onClick={() => setCreating(true)}
          >
            Create PDF parser
          </button>
        </div>
      )}
      {error && (
        <div class="notice error" role="alert">
          {error}
        </div>
      )}
      {result && (
        <div class="pdf-result">
          <p class="hint">
            {result.password_slot
              ? `Opened with Password ${result.password_slot}`
              : "Opened without a password"}
            {result.parser_name ? ` · ${result.parser_name}` : ""}
          </p>
          {result.parse_error && (
            <div class="notice error" role="alert">
              {result.parse_error}. The extracted text is available below.
              Nothing imported.
            </div>
          )}
          {result.statement && (
            <>
              <div
                class={"notice " + (result.statement.balanced ? "" : "error")}
                role="status"
              >
                {result.statement.transactions.length} transactions found.{" "}
                {result.statement.balanced
                  ? result.statement.rounding_accepted
                    ? `Balance check passed with ${money(result.statement.discrepancy, "INR")} rounding (within ${result.statement.balance_tolerance_paise} paise).`
                    : "Balance check passed."
                  : `Flagged for review. Balance difference: ${money(result.statement.discrepancy, "INR")}.`}{" "}
                Preview only; nothing imported.
              </div>
              <div class="pdf-facts">
                <div>
                  <span class="eyebrow">Statement account</span>
                  {result.statement.issuer} ·{" "}
                  {result.statement.account_kind === "bank"
                    ? "Bank account"
                    : "Credit card"}{" "}
                  •• {result.statement.account}
                </div>
                <div>
                  <span class="eyebrow">Statement date</span>
                  {dateLabel(result.statement.date)}
                </div>
                <div>
                  <span class="eyebrow">Payment due</span>
                  {dateLabel(result.statement.due_date)}
                </div>
                <div>
                  <span class="eyebrow">Total due</span>
                  {money(result.statement.total_due, "INR")}
                </div>
                <div>
                  <span class="eyebrow">Minimum due</span>
                  {money(result.statement.minimum_due, "INR")}
                </div>
              </div>
              {result.statement.warnings.map((w) => (
                <p class="hint" key={w}>
                  {w}
                </p>
              ))}
              <details class="statement-rows">
                <summary>
                  Extracted transactions ({result.statement.transactions.length}
                  )
                </summary>
                {result.statement.transactions.map((t, i) => (
                  <div class="statement-row" key={i}>
                    <span>
                      {dateLabel(t.date)}
                      <strong>{t.merchant}</strong>
                    </span>
                    <span>
                      {t.direction === "credit" ? "+" : "−"}
                      {money(t.amount, t.currency)}
                    </span>
                  </div>
                ))}
              </details>
            </>
          )}
          <label class="field">
            <span class="eyebrow">Extracted PDF text</span>
            <textarea
              class="mono message-text pdf-text"
              readOnly
              value={result.text}
              spellcheck={false}
            />
          </label>
          {!result.text.trim() && (
            <p class="hint">
              This PDF contains no extractable text. Scanned statements require
              OCR, which is not supported yet.
            </p>
          )}
        </div>
      )}
    </section>
  );
}
