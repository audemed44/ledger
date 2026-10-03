import { useState } from "preact/hooks";
import { Bell, BellOff, Check, CreditCard, Undo2 } from "lucide-preact";
import { api } from "../api";
import { dateLabel, money } from "../lib";
import type { Due, Summary } from "../types";
import { ErrorNote, Section } from "./ui";

// dueWhen matches the server's wording for reminders and Foyer.
export function dueWhen(days: number) {
  if (days < -1) return `${-days} days overdue`;
  if (days === -1) return "1 day overdue";
  if (days === 0) return "Due today";
  if (days === 1) return "Due tomorrow";
  return `Due in ${days} days`;
}

export function CardDues({ summary, saved }: { summary: Summary; saved: () => void }) {
  const [error, setError] = useState("");
  if (!summary.dues.length) return null;
  async function settle(d: Due, settled: boolean) {
    try {
      await api("dues/settle", { account_id: d.account_id, due_date: d.due_date, settled });
      setError("");
      saved();
    } catch (e) {
      setError((e as Error).message);
    }
  }
  const days = [...(summary.reminders.days ?? [])].reverse();
  return (
    <section>
      <Section index="01" title="Card dues">
        <a class="chip" href="#connection">
          {summary.reminders.enabled ? <Bell size={11} /> : <BellOff size={11} />}
          {summary.reminders.enabled ? `Reminders ${days.join(", ")} days before` : "Reminders off"}
        </a>
      </Section>
      <ErrorNote error={error} />
      <div class="dues">
        {summary.dues.map((d) => (
          <article class={"due " + d.status} key={d.account_id}>
            <div class="merchant">
              <span class="transaction-icon">
                <CreditCard size={18} />
              </span>
              <div>
                <strong>{d.issuer}</strong>
                <div class="hint">
                  Card <span class="mono">•• {d.last_four}</span> · Statement{" "}
                  {dateLabel(d.statement_date)}
                </div>
              </div>
            </div>
            <div class="due-date">
              <strong>{d.status === "paid" ? "Paid" : dueWhen(d.days)}</strong>
              <span class="hint">{dateLabel(d.due_date)}</span>
            </div>
            <div class="amount">
              {money(d.status === "paid" ? d.total_due : d.remaining, d.currency)}
              <span class="hint">
                {d.settled
                  ? "Marked paid"
                  : d.paid > 0
                    ? `${money(d.paid, d.currency)} of ${money(d.total_due, d.currency)} paid`
                    : d.minimum_due > 0 && d.status !== "paid"
                      ? `Minimum ${money(d.minimum_due, d.currency)}`
                      : "Total due"}
              </span>
            </div>
            <div class="due-action">
              {d.settled ? (
                <button class="btn" onClick={() => settle(d, false)}>
                  <Undo2 size={14} /> Undo
                </button>
              ) : d.status !== "paid" ? (
                <button class="btn" onClick={() => settle(d, true)}>
                  <Check size={14} /> Mark paid
                </button>
              ) : null}
            </div>
            {d.total_due > 0 && d.paid > 0 && d.status !== "paid" && (
              <div class="due-progress" aria-hidden="true">
                <span style={{ width: `${Math.min(100, (d.paid * 100) / d.total_due)}%` }} />
              </div>
            )}
          </article>
        ))}
      </div>
      <p class="hint">
        From each card’s latest statement. Payments and refunds that arrive after it count as paid;
        mark a card paid when Ledger doesn’t get an alert for the payment.
      </p>
    </section>
  );
}
