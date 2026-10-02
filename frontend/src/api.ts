export type Parser = {
  issuer?: string;
  account_kind?: string;
  id: number;
  name: string;
  sender: string;
  subject: string;
  pattern: string;
  date_layout: string;
  timezone: string;
  currency: string;
  direction: string;
  enabled: boolean;
};
export type Transaction = {
  account_id?: string;
  account_kind?: string;
  id: number;
  message_id: number;
  merchant: string;
  account: string;
  amount: number;
  currency: string;
  direction: string;
  date: string;
  reference: string;
  status: string;
  issuer: string;
};
export type Message = {
  id: number;
  sender: string;
  subject: string;
  date: string;
  body?: string;
  state: string;
  reason: string;
  body_format?: "plain" | "html";
  attachments?: { part: number; name: string; content_type: string }[];
  has_pdf?: boolean;
  can_parse?: boolean;
  content_error?: string;
};
export type Summary = {
  totals: { currency: string; debit: number; credit: number }[];
  accounts: { id: string; issuer: string; kind: string; last_four: string }[];
  transactions: number;
  queued: number;
  parsers: number;
  month: string;
  demo: boolean;
};
export type Sync = {
  configured: boolean;
  running: boolean;
  last_sync: string;
  error: string;
  label: string;
  interval: string;
};
export type Preview = {
  matched: boolean;
  ignored: boolean;
  transaction?: Transaction;
};
export async function api<T>(
  path: string,
  body?: unknown,
  method?: "DELETE",
): Promise<T> {
  const res = await fetch("/api/" + path, {
    method: method ?? (body === undefined ? "GET" : "POST"),
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    if (res.status === 401 && path !== "login")
      window.dispatchEvent(new Event("ledger-unauthorized"));
    const data = await res.json().catch(() => ({ error: "Request failed" }));
    throw new Error(data.error || "Request failed");
  }
  return res.json();
}
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
