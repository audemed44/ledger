import { useEffect, useState } from "preact/hooks";
import { ArrowUpRight, Download, LogOut, Plus } from "lucide-preact";
import { api } from "./api";
import { AlertParserEditor } from "./components/AlertParserEditor";
import { AlertParsers } from "./components/AlertParsers";
import { ConnectionPage } from "./components/ConnectionPage";
import { DebitCards } from "./components/DebitCards";
import { TransferRules } from "./components/TransferRules";
import { InboxPage } from "./components/InboxPage";
import { Login } from "./components/Login";
import { MessageReview } from "./components/MessageReview";
import { StatementParsers } from "./components/StatementParsers";
import { TransactionsPage } from "./components/TransactionsPage";
import { ErrorNote } from "./components/ui";
import { blankParser, money } from "./lib";
import type { Message, Parser, Summary, Sync } from "./types";

type Page = "transactions" | "inbox" | "parsers" | "connection";

// Pages live in the URL hash so a reload stays where you were.
const pages: Page[] = ["transactions", "inbox", "parsers", "connection"];
const labels = {
  transactions: "Transactions",
  inbox: "Inbox",
  parsers: "Parsers",
  connection: "Connection",
};

export function App() {
  const [signed, setSigned] = useState<boolean | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [page, setPage] = useState<Page>(
    pages.includes(location.hash.slice(1) as Page)
      ? (location.hash.slice(1) as Page)
      : "transactions",
  );
  const [reviewing, setReviewing] = useState<Message | null>(null);
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
  if (signed === false) return <Login onDone={refresh} />;
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
            {p === "inbox" && summary.queued > 0 && <span class="count">{summary.queued}</span>}
          </a>
        ))}
      </nav>
      {summary.demo && (
        <div class="demo-bar">
          <span class="eyebrow">Demonstration</span> Synthetic transactions. Gmail is disabled.
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
                <p class="hint">Transfers between your accounts excluded</p>
              </div>
              <div class="figure">
                <span class="eyebrow">Transactions</span>
                <div class="figure-value">{String(summary.transactions).padStart(2, "0")}</div>
                <p class="hint">Automatically recorded</p>
              </div>
              <a class={"figure " + (summary.queued ? "attention" : "")} href="#inbox">
                <span class="eyebrow">
                  Needs review <ArrowUpRight size={13} />
                </span>
                <div class="figure-value">{String(summary.queued).padStart(2, "0")}</div>
                <p class="hint">
                  {summary.queued ? "Waiting for your review" : "Everything accounted for"}
                </p>
              </a>
            </div>
            <TransactionsPage summary={summary} version={version} />
          </>
        )}
        {page === "inbox" && (
          <InboxPage
            version={version}
            refresh={refresh}
            parserCount={summary.parsers}
            edit={setReviewing}
          />
        )}
        {page === "parsers" && (
          <>
            <AlertParsers version={version} edit={(p) => setEditing({ parser: p })} />
            <StatementParsers />
            <DebitCards saved={refresh} />
            <TransferRules version={version} saved={refresh} />
          </>
        )}
        {page === "connection" && <ConnectionPage sync={sync} busy={busy} syncNow={syncNow} />}
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
      {reviewing && (
        <MessageReview
          message={reviewing}
          imported={refresh}
          close={() => setReviewing(null)}
          createParser={() => {
            setEditing({
              parser: { ...blankParser, sender: reviewing.sender },
              message: reviewing,
            });
            setReviewing(null);
          }}
        />
      )}
      {editing && (
        <AlertParserEditor
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
