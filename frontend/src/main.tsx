import { render } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import {
  ArrowDownLeft,
  ArrowUpRight,
  ArrowRight,
  RefreshCw,
  Download,
  Plus,
  Mail,
  LogOut,
  Check,
  BookOpen,
  SlidersHorizontal,
  Inbox,
} from "lucide-preact";
import "@fontsource/inter/latin-400.css";
import "@fontsource/inter/latin-500.css";
import "@fontsource/inter/latin-600.css";
import "@fontsource/inter/latin-700.css";
import "@fontsource/inter/latin-800.css";
import "@fontsource/geist-mono/latin-400.css";
import "./styles.css";
import { api, money, dateLabel, blankParser, samplePattern } from "./api";
import type {
  Parser,
  Transaction,
  Message,
  Summary,
  Sync,
  Preview,
} from "./api";

type Page = "transactions" | "inbox" | "parsers" | "connection";
const pages: Page[] = ["transactions", "inbox", "parsers", "connection"];
const labels = {
  transactions: "Transactions",
  inbox: "Needs a parser",
  parsers: "Parsers",
  connection: "Connection",
};
function Section({
  index,
  title,
  children,
}: {
  index: string;
  title: string;
  children?: ComponentChildren;
}) {
  return (
    <div class="section-head">
      <span class="section-index">{index}</span>
      <h2>{title}</h2>
      <div class="spacer" />
      {children}
    </div>
  );
}
function ErrorNote({ error }: { error: string }) {
  return error ? (
    <div class="notice error" role="alert">
      {error}
    </div>
  ) : null;
}
function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ComponentChildren;
  hint?: string;
}) {
  return (
    <label class="field">
      <span class="eyebrow">{label}</span>
      {children}
      {hint && <span class="hint">{hint}</span>}
    </label>
  );
}
function Empty({
  title,
  children,
}: {
  title: string;
  children: ComponentChildren;
}) {
  return (
    <div class="empty">
      <Inbox size={28} />
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}
export function App() {
  const [signed, setSigned] = useState<boolean | null>(null),
    [token, setToken] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [page, setPage] = useState<Page>(
    pages.includes(location.hash.slice(1) as Page)
      ? (location.hash.slice(1) as Page)
      : "transactions",
  );
  const [summary, setSummary] = useState<Summary | null>(null),
    [sync, setSync] = useState<Sync | null>(null),
    [version, setVersion] = useState(0),
    [editing, setEditing] = useState<{
      parser: Parser;
      message?: Message;
    } | null>(null);
  function refresh() {
    setVersion((v) => v + 1);
  }
  useEffect(() => {
    const handler = () => setSigned(false);
    window.addEventListener("ledger-unauthorized", handler);
    const hash = () =>
      setPage(
        pages.includes(location.hash.slice(1) as Page)
          ? (location.hash.slice(1) as Page)
          : "transactions",
      );
    window.addEventListener("hashchange", hash);
    return () => {
      window.removeEventListener("ledger-unauthorized", handler);
      window.removeEventListener("hashchange", hash);
    };
  }, []);
  useEffect(() => {
    let live = true;
    Promise.all([api<Summary>("summary"), api<Sync>("sync")])
      .then(([a, b]) => {
        if (live) {
          setSummary(a);
          setSync(b);
          setSigned(true);
          setError("");
        }
      })
      .catch((e) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
    };
  }, [version]);
  useEffect(() => {
    if (!signed) return;
    const timer = setInterval(refresh, 30000);
    return () => clearInterval(timer);
  }, [signed]);
  async function login(e: Event) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("login", { token });
      setToken("");
      refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function syncNow() {
    setBusy(true);
    setError("");
    try {
      const result = await api<Sync>("sync", {});
      setSync(result);
      refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  if (signed === false)
    return (
      <main class="login">
        <form onSubmit={login}>
          <span class="brand">
            <span class="brand-mark" />
            LEDGER
          </span>
          <div class="eyebrow">Private by design</div>
          <h1>
            Your money.
            <br />
            Your server.
          </h1>
          <p class="muted">
            Sign in with the access token from your Ledger configuration.
          </p>
          <Field label="Access token">
            <input
              type="password"
              autoComplete="current-password"
              value={token}
              onInput={(e) => setToken(e.currentTarget.value)}
              required
              autoFocus
            />
          </Field>
          <ErrorNote error={error === "Sign in to Ledger" ? "" : error} />
          <button class="btn primary" disabled={busy}>
            Open Ledger <ArrowRight size={16} />
          </button>
        </form>
      </main>
    );
  if (!summary || !sync)
    return (
      <main class="shell">
        <div class="eyebrow">LEDGER</div>
        <h1>Opening your ledger.</h1>
        <ErrorNote error={error} />
        {error && (
          <button class="btn" onClick={refresh}>
            Retry
          </button>
        )}
      </main>
    );
  return (
    <div class="shell">
      <header class="topbar">
        <a class="brand" href="#transactions">
          <span class="brand-mark" />
          LEDGER<span class="brand-sub">PERSONAL FINANCE</span>
        </a>
        <div class="spacer" />
        <span class="private-label">
          <span class="dot" /> ON YOUR SERVER
        </span>
        <button
          class="icon-btn"
          aria-label="Sign out"
          onClick={async () => {
            try {
              await api("logout", {});
              setSigned(false);
            } catch (e) {
              setError((e as Error).message);
            }
          }}
        >
          <LogOut size={16} />
        </button>
      </header>
      <nav aria-label="Main navigation">
        {pages.map((p) => (
          <a href={"#" + p} class={page === p ? "active" : ""}>
            {labels[p]}
            {p === "inbox" && summary.queued > 0 && (
              <span class="count">{summary.queued}</span>
            )}
          </a>
        ))}
      </nav>
      {summary.demo && (
        <div class="demo-bar">
          <span class="eyebrow">Demonstration</span> Synthetic transactions.
          Gmail is disabled.
        </div>
      )}
      <ErrorNote error={error} />
      <main class="page">
        <div class="page-head">
          <div class="eyebrow">
            <span class="accent">YOUR MONEY, ACCOUNTED FOR</span>
            <span class="slash">/</span>
            {new Date().toLocaleDateString("en-GB", {
              month: "long",
              year: "numeric",
            })}
          </div>
          <div class="title-row">
            <h1>
              {page === "transactions"
                ? "The ledger."
                : page === "inbox"
                  ? "Nothing lost."
                  : page === "parsers"
                    ? "Read between\nthe lines."
                    : "Mail to money."}
            </h1>
            {page === "transactions" && (
              <a class="btn export-top" href="/api/transactions.csv">
                <Download size={14} /> Export CSV
              </a>
            )}
            {page === "parsers" && (
              <button
                class="btn primary"
                onClick={() => setEditing({ parser: { ...blankParser } })}
              >
                <Plus size={15} /> New parser
              </button>
            )}
          </div>
          <p class="lede">
            {page === "transactions"
              ? "Every alert, in one place. A clearer picture of what comes and goes."
              : page === "inbox"
                ? "Unrecognised mail stays here until you teach Ledger how to read it."
                : page === "parsers"
                  ? "Teach Ledger your bank’s format once. Keep every detail on your server."
                  : "A quiet connection to the financial emails you already receive."}
          </p>
        </div>
        {page === "transactions" && (
          <>
            <div class="figures">
              <div class="figure wide">
                <span class="eyebrow">Debits this month</span>
                {summary.totals.length ? (
                  summary.totals.map((t) => (
                    <div class="figure-value">
                      {money(t.debit, t.currency)}
                      <span class="currency">{t.currency}</span>
                    </div>
                  ))
                ) : (
                  <div class="figure-value">—</div>
                )}
                <p class="hint">
                  Provisional · includes transfers until reconciliation
                </p>
              </div>
              <div class="figure">
                <span class="eyebrow">Transactions</span>
                <div class="figure-value">
                  {String(summary.transactions).padStart(2, "0")}
                </div>
                <p class="hint">Automatically recorded</p>
              </div>
              <a
                class={"figure " + (summary.queued ? "attention" : "")}
                href="#inbox"
              >
                <span class="eyebrow">
                  Needs a parser <ArrowUpRight size={13} />
                </span>
                <div class="figure-value">
                  {String(summary.queued).padStart(2, "0")}
                </div>
                <p class="hint">
                  {summary.queued
                    ? "Waiting for your review"
                    : "Everything accounted for"}
                </p>
              </a>
            </div>
            <Transactions summary={summary} version={version} />
          </>
        )}
        {page === "inbox" && (
          <Queue
            version={version}
            refresh={refresh}
            edit={(m) =>
              setEditing({
                parser: { ...blankParser, sender: m.sender },
                message: m,
              })
            }
          />
        )}
        {page === "parsers" && (
          <Parsers version={version} edit={(p) => setEditing({ parser: p })} />
        )}
        {page === "connection" && (
          <Connection sync={sync} busy={busy} syncNow={syncNow} />
        )}
      </main>
      <footer>
        <span>
          LEDGER <span class="muted">/ A LITTLE MORE CLARITY.</span>
        </span>
        <a href="#connection">
          <span class={"dot " + (sync.error ? "warn" : "")} />
          {sync.error
            ? "Connection needs attention"
            : sync.configured
              ? sync.last_sync
                ? "Last checked " +
                  new Date(sync.last_sync).toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                  })
                : "Gmail connected"
              : "Gmail not connected"}
          <ArrowUpRight size={12} />
        </a>
      </footer>
      {editing && (
        <Editor
          initial={editing.parser}
          message={editing.message}
          close={() => setEditing(null)}
          saved={() => {
            setEditing(null);
            refresh();
          }}
        />
      )}
    </div>
  );
}
function Transactions({
  summary,
  version,
}: {
  summary: Summary;
  version: number;
}) {
  const [rows, setRows] = useState<Transaction[]>([]),
    [search, setSearch] = useState(""),
    [account, setAccount] = useState(""),
    [from, setFrom] = useState(""),
    [to, setTo] = useState(""),
    [status, setStatus] = useState(""),
    [offset, setOffset] = useState(0),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true);
  const params = new URLSearchParams({
    search,
    account,
    from,
    to,
    status,
    offset: String(offset),
  }).toString();
  useEffect(() => {
    setOffset(0);
  }, [search, account, from, to, status]);
  useEffect(() => {
    let current = true;
    setLoading(true);
    const timer = setTimeout(
      () =>
        api<Transaction[]>("transactions?" + params)
          .then((data) => {
            if (current) {
              setRows(data);
              setError("");
            }
          })
          .catch((e) => {
            if (current) setError(e.message);
          })
          .finally(() => {
            if (current) setLoading(false);
          }),
      150,
    );
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [params, version]);
  return (
    <section>
      <Section index="01" title="Activity">
        <span class="eyebrow">ALERTS → TRANSACTIONS</span>
      </Section>
      <div class="filters">
        <Field label="Search">
          <input
            type="search"
            placeholder="Merchant, issuer or reference"
            value={search}
            onInput={(e) => setSearch(e.currentTarget.value)}
          />
        </Field>
        <Field label="Account">
          <select
            value={account}
            onChange={(e) => setAccount(e.currentTarget.value)}
          >
            <option value="">All accounts</option>
            {summary.accounts.map((a) => (
              <option>{a}</option>
            ))}
          </select>
        </Field>
        <Field label="From">
          <input
            aria-label="From date"
            type="date"
            value={from}
            onInput={(e) => setFrom(e.currentTarget.value)}
          />
        </Field>
        <Field label="To">
          <input
            aria-label="To date"
            type="date"
            value={to}
            onInput={(e) => setTo(e.currentTarget.value)}
          />
        </Field>
        <Field label="Status">
          <select
            value={status}
            onChange={(e) => setStatus(e.currentTarget.value)}
          >
            <option value="">All statuses</option>
            <option value="provisional">Provisional</option>
            <option value="confirmed">Confirmed</option>
            <option value="flagged">Flagged</option>
          </select>
        </Field>
      </div>
      <ErrorNote error={error} />
      {loading ? (
        <div class="loading" role="status">
          Loading activity…
        </div>
      ) : rows.length ? (
        <div class="transaction-list">
          <div class="transaction-row table-head eyebrow">
            <span>Merchant / account</span>
            <span>Date</span>
            <span>Status</span>
            <span class="right">Amount</span>
          </div>
          {rows.map((t) => (
            <article class="transaction-row" key={t.id}>
              <div class="merchant">
                <span class={"transaction-icon " + t.direction}>
                  {t.direction === "debit" ? (
                    <ArrowUpRight size={18} />
                  ) : (
                    <ArrowDownLeft size={18} />
                  )}
                </span>
                <div>
                  <strong>{t.merchant}</strong>
                  <div class="hint">
                    {t.issuer} <span class="mono">•• {t.account}</span>
                  </div>
                </div>
              </div>
              <time class="transaction-date" dateTime={t.date}>
                {dateLabel(t.date)}
              </time>
              <div class="transaction-status">
                <span class="chip">{t.status}</span>
              </div>
              <div class={"amount " + t.direction}>
                {t.direction === "credit" ? "+" : "−"}
                {money(t.amount, t.currency)}
                <span class="hint">{t.currency}</span>
              </div>
            </article>
          ))}
        </div>
      ) : (
        <Empty
          title={
            search || account || from || to || status
              ? "No matching transactions"
              : "Your first alert starts the story."
          }
        >
          {search || account || from || to || status
            ? "Try widening your filters."
            : "Connect Gmail, then create a parser for your bank’s alerts. Transactions will appear here automatically."}
        </Empty>
      )}
      <div class="list-foot">
        <span class="hint">
          {rows.length
            ? `${offset + 1}–${offset + rows.length} shown`
            : "No activity yet"}{" "}
          · Amounts are unconfirmed until matched to a statement.
        </span>
        <div class="actions">
          {offset > 0 && (
            <button
              class="btn"
              onClick={() => setOffset(Math.max(0, offset - 100))}
            >
              Previous
            </button>
          )}
          {rows.length === 100 && (
            <button class="btn" onClick={() => setOffset(offset + 100)}>
              Next
            </button>
          )}
          <a class="text-link" href={"/api/transactions.csv?" + params}>
            Export filtered <Download size={13} />
          </a>
        </div>
      </div>
    </section>
  );
}
function Queue({
  version,
  refresh,
  edit,
}: {
  version: number;
  refresh: () => void;
  edit: (m: Message) => void;
}) {
  const [rows, setRows] = useState<Message[] | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState("");
  useEffect(() => {
    api<Message[]>("messages")
      .then(setRows)
      .catch((e) => setError(e.message));
  }, [version]);
  return (
    <section>
      <Section index="01" title="Needs a parser">
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
      </Section>
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
                <p class="queue-reason">
                  {m.reason || "No matching alert parser"}
                </p>
              </div>
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
            </article>
          ))}
          <p class="hint">
            Showing up to 200 queued messages. Retry backlog processes all
            queued mail.
          </p>
        </div>
      ) : (
        <Empty title="Nothing waiting in the wings.">
          Unmatched emails will appear here, with their original text safely
          archived.
        </Empty>
      )}
    </section>
  );
}
function Parsers({
  version,
  edit,
}: {
  version: number;
  edit: (p: Parser) => void;
}) {
  const [rows, setRows] = useState<Parser[] | null>(null),
    [error, setError] = useState("");
  useEffect(() => {
    api<Parser[]>("parsers")
      .then(setRows)
      .catch((e) => setError(e.message));
  }, [version]);
  return (
    <section>
      <Section index="01" title="Alert parsers">
        <a class="text-link" href="/api/parsers.yaml">
          Export YAML <Download size={14} />
        </a>
      </Section>
      <ErrorNote error={error} />
      {rows === null ? (
        <p class="loading">Loading parsers…</p>
      ) : rows.length ? (
        <div class="parser-grid">
          {rows.map((p, i) => (
            <button class="parser-card" onClick={() => edit(p)}>
              <div class="eyebrow">
                <span class="accent">{String(i + 1).padStart(2, "0")}</span>
                <span class="spacer" />
                <span class={"dot " + (!p.enabled ? "off" : "")} />
                {p.enabled ? "ACTIVE" : "DISABLED"}
              </div>
              <h3>{p.name}</h3>
              <p class="hint">{p.sender}</p>
              <div class="parser-bottom">
                <span class="chip">
                  {p.direction === "ignore"
                    ? "Ignore matching alerts"
                    : p.direction + " · " + p.currency}
                </span>
                <ArrowUpRight size={20} />
              </div>
            </button>
          ))}
        </div>
      ) : (
        <Empty title="Give your inbox a little context.">
          Create an alert parser using a sample from your queue. Preview the
          result before saving.
        </Empty>
      )}
      <div class="notice">
        <BookOpen size={18} />
        <span>
          Alert parsers record provisional transactions. Statement parsing and
          reconciliation follow in phase 2.
        </span>
      </div>
    </section>
  );
}
function Connection({
  sync,
  busy,
  syncNow,
}: {
  sync: Sync;
  busy: boolean;
  syncNow: () => void;
}) {
  return (
    <section>
      <Section index="01" title="Gmail connection">
        <span class="chip">
          {sync.configured ? "Configured" : "Setup required"}
        </span>
      </Section>
      <div class="connection-grid">
        <div>
          <h2 class="subheading">
            One label.
            <br />
            Everything in its place.
          </h2>
          <p class="lede">
            Ledger checks only the label you choose. Original emails and
            attachments are archived on your server.
          </p>
          <div class="connection-facts">
            <div>
              <span class="eyebrow">Label</span>
              <strong>{sync.label}</strong>
            </div>
            <div>
              <span class="eyebrow">Check interval</span>
              <strong>{sync.interval}</strong>
            </div>
            <div>
              <span class="eyebrow">Last successful check</span>
              <strong>
                {sync.last_sync
                  ? new Date(sync.last_sync).toLocaleString()
                  : "Not yet"}
              </strong>
            </div>
          </div>
          <ErrorNote error={sync.error} />
          <button
            class="btn primary"
            onClick={syncNow}
            disabled={!sync.configured || busy || sync.running}
          >
            <RefreshCw size={15} class={busy ? "spin" : ""} />
            {busy || sync.running ? "Checking mail…" : "Check mail now"}
          </button>
        </div>
        <div class="setup">
          <h3>A one-time setup</h3>
          <ol>
            <li>
              Create a Gmail filter for your banks’ alert and statement sender
              addresses.
            </li>
            <li>
              Apply the <strong>{sync.label}</strong> label. Select “Also apply
              filter to matching conversations” if you want past emails
              available.
            </li>
            <li>
              Add your Gmail address and app password to the stack’s{" "}
              <code>.env</code>, then restart Ledger. Set{" "}
              <code>LEDGER_BACKFILL=true</code> before the first sync to import
              existing mail.
            </li>
            <li>
              Open{" "}
              <a class="text-link" href="#inbox">
                Needs a parser
              </a>{" "}
              to teach Ledger each bank’s format.
            </li>
          </ol>
          <p class="hint">
            An app password can access the whole mailbox. Ledger reads only the
            configured label. Credentials are never saved in the database.
          </p>
        </div>
      </div>
    </section>
  );
}
function Editor({
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
    [savedID, setSavedID] = useState(0);
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
            <Field label="Issuer name">
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
          <Field
            label="Subject pattern"
            hint="Go regular expression; leave blank to match any subject."
          >
            <input
              class="mono"
              value={parser.subject}
              onInput={(e) => update("subject", e.currentTarget.value)}
              placeholder="(?i)purchase alert"
            />
          </Field>
          <Field
            label="Body pattern"
            hint="Required named groups: amount, merchant, account (last 4), date. Optional: currency, direction, reference."
          >
            <textarea
              class="mono pattern"
              value={parser.pattern}
              onInput={(e) => update("pattern", e.currentTarget.value)}
              spellcheck={false}
              placeholder="(?P<amount>...)"
            />
          </Field>
          <button
            class="text-link"
            onClick={() => {
              update("pattern", samplePattern);
              if (!message) {
                setBody(
                  "Amount: INR 1290.00\nMerchant: Example Store\nCard: 4242\nDate: 2026-10-01\nReference: EXAMPLE-001\n",
                );
                setSender("alerts@example.invalid");
                setSubject("Purchase alert");
                setParser((p) => ({
                  ...p,
                  name: p.name || "Example Bank",
                  sender: "alerts@example.invalid",
                  subject: "^Purchase alert$",
                }));
              }
            }}
          >
            Use synthetic example <ArrowRight size={13} />
          </button>
          <div class="two-col">
            <Field
              label="Date layout"
              hint="Go layout: 02-Jan-2006 or 2006-01-02"
            >
              <input
                class="mono"
                value={parser.date_layout}
                onInput={(e) => update("date_layout", e.currentTarget.value)}
              />
            </Field>
            <Field label="Timezone">
              <input
                value={parser.timezone}
                onInput={(e) => update("timezone", e.currentTarget.value)}
              />
            </Field>
            <Field label="Default currency">
              <select
                value={parser.currency}
                onChange={(e) => update("currency", e.currentTarget.value)}
              >
                {[
                  "INR",
                  "USD",
                  "EUR",
                  "GBP",
                  "AUD",
                  "CAD",
                  "SGD",
                  "AED",
                  "CHF",
                  "HKD",
                ].map((c) => (
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
                <input
                  value={sender}
                  onInput={(e) => setSender(e.currentTarget.value)}
                />
              </Field>
              <Field label="Sample subject">
                <input
                  value={subject}
                  onInput={(e) => setSubject(e.currentTarget.value)}
                />
              </Field>
            </div>
          )}
          {message && (
            <div>
              <h3>{message.subject}</h3>
              <p class="hint">{message.sender}</p>
            </div>
          )}
          <Field label="Plain-text email">
            <textarea
              class="mono sample"
              value={body}
              readOnly={!!message}
              onInput={(e) => setBody(e.currentTarget.value)}
              spellcheck={false}
              placeholder="Use the synthetic example, or open a message from the queue."
            />
          </Field>
          <div class="preview">
            <div class="eyebrow">EXTRACTED TRANSACTION</div>
            {previewError ? (
              <p class="error-text" role="status">
                {previewError}
              </p>
            ) : preview?.transaction ? (
              <>
                <div class="preview-amount">
                  {money(
                    preview.transaction.amount,
                    preview.transaction.currency,
                  )}
                </div>
                <h3>{preview.transaction.merchant}</h3>
                <p class="hint">
                  {preview.transaction.issuer} · ••{" "}
                  {preview.transaction.account}
                </p>
                <p class="hint">
                  {dateLabel(preview.transaction.date)} ·{" "}
                  {preview.transaction.direction}
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
                    : "Your preview will appear here as you edit."}
              </p>
            )}
          </div>
          <p class="hint">
            Preview runs on your server. No mail is sent to external parsing
            services.
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
            Saving retries all queued mail. Previously imported transactions
            stay unchanged.
          </span>
        </div>
        <button class="btn primary" disabled={busy} onClick={save}>
          {busy ? "Saving…" : "Save & retry backlog"}
          <ArrowRight size={15} />
        </button>
      </div>
    </dialog>
  );
}
const root = document.getElementById("app");
if (root) render(<App />, root);
