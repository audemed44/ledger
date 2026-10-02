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
export const samplePattern = String.raw`(?s)Amount: (?P<currency>[A-Z]{3}) (?P<amount>[\d,.]+)\nMerchant: (?P<merchant>[^\n]+)\nCard: (?P<account>\d{4})\nDate: (?P<date>[^\n]+)\nReference: (?P<reference>[^\n]+)`;
export const newStatementParser: StatementParser = {
  balance_tolerance_paise: 99,
  id: 0,
  name: "HDFC Credit Card Parser v1",
  adapter: "hdfc-credit-card",
  password_slot: 0,
};
export function passwordLabel(slot: number) {
  return slot ? `Password ${slot}` : "Try all configured passwords";
}
