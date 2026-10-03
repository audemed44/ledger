// Shapes of the JSON the server returns.
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
  description?: string;
  wordings?: Wording[];
};
// Wording is another way an alert is worded, with its own body pattern.
export type Wording = { pattern: string; date_layout: string; account_kind?: string };
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
  matched?: boolean;
  placeholder?: boolean;
  transfer?: "" | "paired" | "rule" | "manual";
  transfer_of?: number;
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
  /** Foyer, the homelab's start page (HOMEPAGE_URL). */
  foyer_url?: string;
  totals: { currency: string; debit: number; credit: number }[];
  accounts: { id: string; issuer: string; kind: string; last_four: string }[];
  transactions: number;
  queued: number;
  parsers: number;
  dues: Due[];
  reminders: { enabled: boolean; days: number[] | null; on_statement: boolean };
  month: string;
  demo: boolean;
};
export type Due = {
  account_id: string;
  issuer: string;
  last_four: string;
  statement_date: string;
  due_date: string;
  currency: string;
  total_due: number;
  minimum_due: number;
  paid: number;
  remaining: number;
  days: number;
  settled: boolean;
  status: "paid" | "due" | "overdue";
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
export type StatementParser = {
  balance_tolerance_paise: number;
  id: number;
  name: string;
  adapter: string;
  password_slot: number;
  triggers?: Trigger[];
};
export type Trigger = { sender: string; subject: string; filename: string };
export type Mark = { field: string; start: number; end: number };
export type Example = { pattern: string; date_layout?: string; subject: string };
export type PDFConfig = {
  password_slots: number[];
  adapters: { id: string; name: string; description: string }[];
};
export type PDFResult = {
  fingerprint?: string;
  imported?: {
    statement_id: number;
    count: number;
    matched: number;
    flagged: number;
    already_imported: boolean;
  };
  automatic?: boolean;
  automatic_error?: string;
  text: string;
  password_slot: number;
  parser_name?: string;
  parse_error?: string;
  statement?: {
    transactions: Transaction[];
    issuer: string;
    account: string;
    account_kind: string;
    date: string;
    due_date: string;
    minimum_due: number;
    opening: number;
    total_due: number;
    balanced: boolean;
    rounding_accepted?: boolean;
    balance_tolerance_paise?: number;
    discrepancy: number;
    warnings: string[];
  };
};
