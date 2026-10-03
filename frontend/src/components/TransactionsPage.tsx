import { useEffect, useState } from "preact/hooks";
import { ArrowDownLeft, ArrowUpRight, Download, MoreHorizontal } from "lucide-preact";
import { api } from "../api";
import { dateLabel, money, periodRange, recentMonths } from "../lib";
import type { Summary, Transaction } from "../types";
import { Empty, ErrorNote, Field, Section } from "./ui";

export function TransactionsPage({ summary, version }: { summary: Summary; version: number }) {
  const [rows, setRows] = useState<Transaction[]>([]),
    [search, setSearch] = useState(""),
    [account, setAccount] = useState(""),
    [from, setFrom] = useState(""),
    [to, setTo] = useState(""),
    [status, setStatus] = useState(""),
    [kind, setKind] = useState(""),
    [period, setPeriod] = useState(""),
    [actions, setActions] = useState(0),
    [offset, setOffset] = useState(0),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true),
    [reload, setReload] = useState(0);
  // act runs one of a row's actions, then reloads the list.
  async function act(path: string, body: unknown) {
    try {
      await api(path, body);
      setActions(0);
      setReload((n) => n + 1);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function dismiss(t: Transaction, dismissed: boolean) {
    try {
      await api(`transactions/${t.id}/dismiss`, { dismissed });
      setReload((n) => n + 1);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  // A period from the menu, or the custom dates.
  const range = period === "custom" ? { from, to } : periodRange(period);
  const params = new URLSearchParams({
    search,
    account,
    from: range.from,
    to: range.to,
    status,
    kind,
    offset: String(offset),
  }).toString();
  useEffect(() => {
    setOffset(0);
  }, [search, account, from, to, status, kind, period]);
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
  }, [params, version, reload]);
  return (
    <section>
      <Section index={summary.dues.length ? "02" : "01"} title="Activity">
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
          <select value={account} onChange={(e) => setAccount(e.currentTarget.value)}>
            <option value="">All accounts</option>
            {summary.accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.issuer} ·{" "}
                {a.kind === "card" ? "Card" : a.kind === "bank" ? "Bank account" : "Account"} ••{" "}
                {a.last_four}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Period">
          <select value={period} onChange={(e) => setPeriod(e.currentTarget.value)}>
            <option value="">All time</option>
            <option value="this-month">This month</option>
            <option value="last-month">Last month</option>
            <option value="months-3">Last 3 months</option>
            <option value="months-6">Last 6 months</option>
            <option value="months-12">Last 12 months</option>
            <option value="this-year">This year</option>
            <optgroup label="Month">
              {recentMonths(24).map((m) => (
                <option key={m.value} value={m.value}>
                  {m.label}
                </option>
              ))}
            </optgroup>
            <option value="custom">Custom dates…</option>
          </select>
        </Field>
        <Field label="Status">
          <select value={status} onChange={(e) => setStatus(e.currentTarget.value)}>
            <option value="">All statuses</option>
            <option value="provisional">Provisional</option>
            <option value="confirmed">Confirmed</option>
            <option value="flagged">Flagged</option>
            <option value="dismissed">Dismissed</option>
          </select>
        </Field>
        <Field label="Type">
          <select value={kind} onChange={(e) => setKind(e.currentTarget.value)}>
            <option value="">Everything</option>
            <option value="spending">Spending and income</option>
            <option value="transfers">Transfers between your accounts</option>
          </select>
        </Field>
        {period === "custom" && (
          <div class="custom-dates">
            <Field label="From">
              <input
                aria-label="From date"
                type="date"
                value={from}
                max={to || undefined}
                onInput={(e) => setFrom(e.currentTarget.value)}
              />
            </Field>
            <Field label="To">
              <input
                aria-label="To date"
                type="date"
                value={to}
                min={from || undefined}
                onInput={(e) => setTo(e.currentTarget.value)}
              />
            </Field>
          </div>
        )}
      </div>
      <ErrorNote error={error} />
      {/* Refreshes keep the rows on screen, so the page doesn't jump. */}
      {loading && !rows.length ? (
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
                    {t.issuer} ·{" "}
                    {t.account_kind === "bank"
                      ? "Bank"
                      : t.account_kind === "card"
                        ? "Card"
                        : "Account"}{" "}
                    <span class="mono">•• {t.account}</span>
                  </div>
                </div>
              </div>
              <time class="transaction-date" dateTime={t.date}>
                {dateLabel(t.date)}
              </time>
              <div class="transaction-status">
                <span
                  class={"chip " + t.status}
                  title={
                    t.status === "flagged"
                      ? "Not on the statement for its period"
                      : t.matched
                        ? "Alert and statement line matched"
                        : undefined
                  }
                >
                  {t.status}
                  {t.matched ? " · matched" : ""}
                </span>
                {t.transfer && (
                  <span
                    class="chip transfer"
                    title={
                      t.transfer === "paired"
                        ? "Paired with the other side, on another of your accounts"
                        : t.transfer === "rule"
                          ? "A transfer rule matched its description"
                          : "Marked as a transfer"
                    }
                  >
                    transfer
                  </span>
                )}
                <button
                  class="icon-btn row-more"
                  aria-label={`Actions for ${t.merchant}`}
                  aria-expanded={actions === t.id}
                  onClick={() => setActions(actions === t.id ? 0 : t.id)}
                >
                  <MoreHorizontal size={15} />
                </button>
                {(t.status === "flagged" || t.status === "dismissed") && (
                  <button
                    class="text-link"
                    onClick={() => dismiss(t, t.status === "flagged")}
                    title={
                      t.status === "flagged"
                        ? "Not a real charge: leave it out of totals"
                        : "Count it again"
                    }
                  >
                    {t.status === "flagged" ? "Dismiss" : "Restore"}
                  </button>
                )}
              </div>
              <div class={"amount " + t.direction}>
                {t.direction === "credit" ? "+" : "−"}
                {money(t.amount, t.currency)}
              </div>
              {actions === t.id && (
                <div class="row-actions">
                  {t.transfer ? (
                    <button
                      class="btn"
                      onClick={() => act(`transactions/${t.id}/transfer`, { transfer: false })}
                    >
                      Not a transfer{t.transfer === "paired" ? " (unpair both sides)" : ""}
                    </button>
                  ) : (
                    <>
                      <button
                        class="btn"
                        onClick={() => act(`transactions/${t.id}/transfer`, { transfer: true })}
                      >
                        Mark as a transfer
                      </button>
                      <button
                        class="btn"
                        onClick={() => act(`transactions/${t.id}/transfer-rule`, {})}
                      >
                        Treat everything like “{t.merchant}” as transfers
                      </button>
                    </>
                  )}
                </div>
              )}
            </article>
          ))}
        </div>
      ) : (
        <Empty
          title={
            search || account || range.from || range.to || status || kind
              ? "No matching transactions"
              : "Your first alert starts the story."
          }
        >
          {search || account || range.from || range.to || status || kind
            ? "Try widening your filters."
            : "Connect Gmail, then create a parser for your bank’s alerts. Transactions will appear here automatically."}
        </Empty>
      )}
      <div class="list-foot">
        <span class="hint">
          {rows.length ? `${offset + 1}–${offset + rows.length} shown` : "No activity yet"} · Alerts
          are provisional until a statement confirms them.
        </span>
        <div class="actions">
          {offset > 0 && (
            <button class="btn" onClick={() => setOffset(Math.max(0, offset - 100))}>
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
