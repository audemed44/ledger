import type { Parser, StatementParser } from "./types";

export function money(amount: number, currency: string) {
  return new Intl.NumberFormat("en-IN", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
  }).format(amount / 100);
}
export function dateLabel(value: string) {
  return new Date(value).toLocaleDateString("en-GB", {
    day: "2-digit",
    month: "short",
    year: "numeric",
  });
}
export const blankParser: Parser = {
  issuer: "",
  account_kind: "card",
  id: 0,
  name: "",
  sender: "",
  subject: "",
  pattern: "",
  date_layout: "2006-01-02",
  timezone: "Asia/Kolkata",
  currency: "INR",
  direction: "debit",
  enabled: true,
};
// A synthetic alert for trying the parser editor without real mail.
export const syntheticAlert = {
  sender: "alerts@example.invalid",
  subject: "Purchase alert",
  body: "Amount: INR 1290.00\nMerchant: Example Store\nCard: 4242\nDate: 2026-10-01\nReference: EXAMPLE-001\n",
};
export const newStatementParser: StatementParser = {
  balance_tolerance_paise: 99,
  id: 0,
  name: "HDFC Credit Card Parser v1",
  adapter: "hdfc-credit-card",
  password_slot: 0,
  triggers: [],
};
export function passwordLabel(slot: number) {
  return slot ? `Password ${slot}` : "Try all configured passwords";
}

// A period of the transactions list: "" (all time), "this-month",
// "last-month", "months-3"/"months-6"/"months-12" (the last N months
// including this one), "this-year", or "month-YYYY-MM". Its range is
// inclusive YYYY-MM-DD dates in local time.
export function periodRange(period: string, today = new Date()): { from: string; to: string } {
  const day = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  const month = (y: number, m: number) => ({
    from: day(new Date(y, m, 1)),
    to: day(new Date(y, m + 1, 0)),
  });
  const y = today.getFullYear(),
    m = today.getMonth();
  if (period === "this-month") return month(y, m);
  if (period === "last-month") return month(y, m - 1);
  if (period === "this-year") return { from: `${y}-01-01`, to: `${y}-12-31` };
  const last = /^months-(\d+)$/.exec(period);
  if (last) return { from: day(new Date(y, m - Number(last[1]) + 1, 1)), to: month(y, m).to };
  const named = /^month-(\d{4})-(\d{2})$/.exec(period);
  if (named) return month(Number(named[1]), Number(named[2]) - 1);
  return { from: "", to: "" };
}

// recentMonths lists the last n months, newest first, for the period menu.
export function recentMonths(n: number, today = new Date()) {
  return Array.from({ length: n }, (_, i) => {
    const d = new Date(today.getFullYear(), today.getMonth() - i, 1);
    return {
      value: `month-${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`,
      label: d.toLocaleDateString("en-GB", { month: "long", year: "numeric" }),
    };
  });
}
