import { useEffect, useState } from "preact/hooks";
import { ArrowDownRight, ArrowUpRight, Minus } from "lucide-preact";
import { api } from "../api";
import { dateLabel, money, payee, recentMonths } from "../lib";
import type { CategoryInsight, Insights } from "../types";
import { MonthColumns, RunningTotal } from "./charts";
import { ErrorNote, Field, Section } from "./ui";

// whole is an amount to the rupee, for averages: ₹38,368.
function whole(amount: number, currency: string) {
  return money(Math.round(amount / 100) * 100, currency).replace(/\.00$/, "");
}

function monthName(month: string, long = true) {
  return new Date(Number(month.slice(0, 4)), Number(month.slice(5, 7)) - 1, 1).toLocaleDateString(
    "en-GB",
    long ? { month: "long", year: "numeric" } : { month: "long" },
  );
}

// change is how far a is from b, signed: "+35%", "−12%", or "new".
function change(a: number, b: number) {
  if (b <= 0) return a > 0 ? "new" : "—";
  const p = Math.round(((a - b) / b) * 100);
  return p === 0 ? "±0%" : (p > 0 ? "+" : "−") + Math.abs(p) + "%";
}

function Delta({ a, b, against }: { a: number; b: number; against: string }) {
  const text = change(a, b);
  const Icon =
    text.startsWith("+") || text === "new"
      ? ArrowUpRight
      : text.startsWith("−")
        ? ArrowDownRight
        : Minus;
  return (
    <p class="delta">
      <Icon size={13} /> <strong>{text}</strong> {against}
    </p>
  );
}

// The months the menu offers: this one and the 23 before, as YYYY-MM.
function menuMonths(current: string) {
  const [y, m] = current.split("-").map(Number);
  return recentMonths(24, new Date(y, m - 1, 15)).map((o) => ({
    value: o.value.slice(6),
    label: o.label,
  }));
}

export function InsightsPage({ version, month: thisMonth }: { version: number; month: string }) {
  const [month, setMonth] = useState(thisMonth),
    [data, setData] = useState<Insights | null>(null),
    [picked, setPicked] = useState<number | null>(null),
    [table, setTable] = useState(false),
    [loading, setLoading] = useState(true),
    [error, setError] = useState("");
  useEffect(() => {
    let current = true;
    setLoading(true);
    api<Insights>("insights?month=" + month)
      .then((d) => {
        if (current) {
          setData(d);
          setError("");
        }
      })
      .catch((e) => current && setError(e.message))
      .finally(() => current && setLoading(false));
    return () => {
      current = false;
    };
  }, [month, version]);
  if (!data)
    return (
      <section>
        <ErrorNote error={error} />
        <div class="loading" role="status">
          Loading insights…
        </div>
      </section>
    );

  const c = data.currency;
  const spent = data.categories.filter((x) => x.amount > 0);
  const largest = Math.max(...spent.map((x) => x.amount), 1);
  const chosen = picked === null ? null : data.categories.find((x) => x.category_id === picked);
  const trend = chosen
    ? data.months.map((m, i) => ({ month: m.month, amount: chosen.months[i] ?? 0 }))
    : data.months;
  const previousMonth = data.months.at(-2)?.month ?? "";
  const usual = data.baseline_count === 1 ? "last month" : `${data.baseline_count}-month average`;
  const link = (x: CategoryInsight) =>
    `#transactions?category=${x.category_id || "none"}&period=month-${data.month}`;
  return (
    <section class={"insights" + (loading ? " refreshing" : "")}>
      <div class="filters insights-filters">
        <Field label="Month">
          <select value={month} onChange={(e) => setMonth(e.currentTarget.value)}>
            {menuMonths(thisMonth).map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <ErrorNote error={error} />

      <div class="figures insight-figures">
        <div class="figure wide">
          <span class="eyebrow">
            Spent in {monthName(data.month, false)}
            {data.current ? " so far" : ""}
          </span>
          <div class="figure-value hero">{money(data.total, c)}</div>
          {data.refunds > 0 && (
            <p class="hint">After {money(data.refunds, c)} of refunds and money back</p>
          )}
          {data.previous > 0 && (
            <Delta
              a={data.total}
              b={data.previous}
              against={`vs ${monthName(previousMonth, false)}`}
            />
          )}
          {data.average > 0 && (
            <Delta
              a={data.current ? (data.projected ?? data.total) : data.total}
              b={data.average}
              against={`${data.current ? "pace " : ""}vs your ${usual} (${whole(data.average, c)})`}
            />
          )}
        </div>
        <div class="figure">
          <span class="eyebrow">{data.current ? "On pace for" : "Daily average"}</span>
          <div class="figure-value">
            {money(
              data.current
                ? (data.projected ?? 0)
                : Math.round(data.total / Math.max(1, data.days)),
              c,
            )}
          </div>
          <p class="hint">
            {data.current
              ? `Spent so far, plus your everyday rate (spends under ₹5,000) for the ${data.days_in_month - data.days} days left`
              : `Over ${data.days_in_month} days`}
          </p>
        </div>
        <div class="figure">
          <span class="eyebrow">Transactions</span>
          <div class="figure-value">{String(data.count).padStart(2, "0")}</div>
          <p class="hint">
            {spent.length} {spent.length === 1 ? "category" : "categories"}, transfers excluded
          </p>
        </div>
      </div>

      {data.insights.length > 0 && (
        <>
          <Section index="01" title="What stands out" />
          <ul class="insight-list">
            {data.insights.map((i) => (
              <li key={i.text} class={i.tone}>
                {i.tone === "up" ? (
                  <ArrowUpRight size={15} aria-label="More than usual" />
                ) : i.tone === "down" ? (
                  <ArrowDownRight size={15} aria-label="Less than usual" />
                ) : (
                  <Minus size={15} aria-hidden="true" />
                )}
                <span>{i.text}</span>
              </li>
            ))}
          </ul>
        </>
      )}

      <Section index="02" title="By category">
        <span class="eyebrow">{monthName(data.month)}</span>
      </Section>
      {spent.length ? (
        <div class="insight-categories">
          <div class="insight-category head eyebrow">
            <span>Category</span>
            <span />
            <span class="right">Spent</span>
            <span class="right">Usual</span>
            <span class="right">Change</span>
          </div>
          {spent.map((x) => (
            <div
              key={x.category_id}
              class={
                "insight-category" +
                (x.category_id ? "" : " uncategorised") +
                (picked === x.category_id ? " picked" : "")
              }
            >
              <button
                class="category-pick"
                aria-pressed={picked === x.category_id}
                title="Show this category's months in the trend"
                onClick={() => setPicked(picked === x.category_id ? null : x.category_id)}
              >
                {x.category || "Uncategorised"}
              </button>
              <span class="category-track">
                <span class="category-fill" style={{ width: `${(x.amount / largest) * 100}%` }} />
              </span>
              <a
                class="right category-amount"
                href={link(x)}
                title={
                  x.refunds
                    ? `${money(x.amount + x.refunds, c)} spent, ${money(x.refunds, c)} back. List these transactions`
                    : "List these transactions"
                }
              >
                {money(x.amount, c)}
                {x.refunds > 0 && <span class="refunded">−{whole(x.refunds, c)} back</span>}
              </a>
              <span class="right hint">{x.average ? whole(x.average, c) : "—"}</span>
              <span class="right category-change">{change(x.amount, x.average)}</span>
            </div>
          ))}
        </div>
      ) : (
        <p class="hint">No spending in {monthName(data.month)}.</p>
      )}
      <p class="hint">
        Refunds and money paid back count against their category in the month they arrive. “Usual”
        is the {usual}. Pick a category to see its months below; its amount lists its transactions.
      </p>

      <Section
        index="03"
        title={chosen ? `${chosen.category || "Uncategorised"} by month` : "By month"}
      >
        {chosen && (
          <button class="text-link" onClick={() => setPicked(null)}>
            Show all spending
          </button>
        )}
      </Section>
      <MonthColumns
        months={trend}
        focus={data.month}
        currency={c}
        label={`${chosen ? chosen.category || "Uncategorised" : "Spending"} per month, the last 12 months`}
        pick={setMonth}
      />
      <p class="hint">
        Pick a month to see it.{" "}
        <button class="text-link" onClick={() => setTable(!table)}>
          {table ? "Hide the table" : "Show as a table"}
        </button>
      </p>
      {table && (
        <table class="insight-table">
          <tbody>
            {[...trend].reverse().map((m) => (
              <tr key={m.month}>
                <td>{monthName(m.month)}</td>
                <td class="right">{money(m.amount, c)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <Section index="04" title="Through the month" />
      <div class="chart-legend">
        <span>
          <span class="line-key now" /> {monthName(data.month, false)}{" "}
          <strong>{money(data.total, c)}</strong>
        </span>
        {previousMonth && (
          <span>
            <span class="line-key before" /> {monthName(previousMonth, false)}{" "}
            <strong>{money(data.previous, c)}</strong>
          </span>
        )}
      </div>
      <RunningTotal
        daily={data.daily}
        previous={data.previous_daily}
        days={data.days}
        currency={c}
        names={[monthName(data.month, false), monthName(previousMonth, false)]}
      />

      <Section index="05" title="Largest spends" />
      {data.largest.length ? (
        <div class="insight-largest">
          {data.largest.map((t) => (
            <div class="trigger" key={t.id}>
              <span>
                <strong title={t.merchant}>{payee(t.merchant)}</strong>
                <span class="hint">
                  {" "}
                  · {dateLabel(t.date)} · {t.category || "Uncategorised"}
                </span>
              </span>
              <span class="mono">{money(t.amount, t.currency)}</span>
            </div>
          ))}
        </div>
      ) : (
        <p class="hint">Nothing yet.</p>
      )}
    </section>
  );
}
