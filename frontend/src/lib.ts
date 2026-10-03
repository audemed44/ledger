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
