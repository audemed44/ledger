import { money } from "../lib";
import type { CategoryTotal } from "../types";
import { Section } from "./ui";

// CategorySpend ranks a month's spending by category, one bar each. Picking
// a category filters the activity below to it; picking it again clears it.
export function CategorySpend({
  index,
  label,
  totals,
  picked,
  pick,
}: {
  index: string;
  label: string;
  totals: CategoryTotal[];
  picked: string;
  pick: (category: string) => void;
}) {
  // One currency per chart: the one most was spent in.
  const sums = new Map<string, number>();
  for (const t of totals) sums.set(t.currency, (sums.get(t.currency) ?? 0) + t.amount);
  const currency = [...sums].sort((a, b) => b[1] - a[1])[0]?.[0];
  const rows = totals.filter((t) => t.currency === currency);
  const total = sums.get(currency ?? "") ?? 0;
  const largest = rows[0]?.amount ?? 0;
  return (
    <section class="category-spend">
      <Section index={index} title="Spending by category">
        <span class="eyebrow">{label}</span>
      </Section>
      {rows.length ? (
        <div class="category-bars">
          {rows.map((t) => {
            const key = t.category_id ? String(t.category_id) : "none";
            const name = t.category || "Uncategorised";
            const share = total ? Math.round((t.amount / total) * 100) : 0;
            return (
              <button
                key={key}
                class={
                  "category-bar" +
                  (t.category_id ? "" : " uncategorised") +
                  (picked === key ? " picked" : "")
                }
                aria-pressed={picked === key}
                title={`${name}: ${money(t.amount, t.currency)}, ${t.count} transaction${t.count === 1 ? "" : "s"}, ${share}% of spending`}
                onClick={() => pick(picked === key ? "" : key)}
              >
                <span class="category-name">{name}</span>
                <span class="category-track">
                  <span
                    class="category-fill"
                    style={{ width: `${Math.max(1, (t.amount / largest) * 100)}%` }}
                  />
                </span>
                <span class="category-amount">{money(t.amount, t.currency)}</span>
                <span class="category-share hint">{share}%</span>
              </button>
            );
          })}
        </div>
      ) : (
        <p class="hint">No spending this month yet.</p>
      )}
      <p class="hint">
        Debits that aren’t transfers. Pick a category to list its transactions. Set categories from
        a transaction’s actions, by hand or with a rule.
      </p>
    </section>
  );
}
