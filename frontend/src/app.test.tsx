import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "./App";
import { CardDues } from "./components/CardDues";
import { ConnectionPage } from "./components/ConnectionPage";
import { InsightsPage } from "./components/InsightsPage";
import { TransactionsPage } from "./components/TransactionsPage";
import { money, periodRange, recentMonths } from "./lib";
const summary = {
  totals: [],
  accounts: [],
  transactions: 0,
  queued: 0,
  parsers: 0,
  dues: [],
  reminders: { enabled: false, days: [0, 1, 5], on_statement: false },
  month: "2026-10",
  demo: false,
};
const sync = {
  configured: false,
  running: false,
  last_sync: "",
  error: "",
  label: "Bank",
  interval: "15m0s",
};
function reply(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  location.hash = "";
});
it("requires authentication and never puts the token in a URL", async () => {
  let authenticated = false;
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/login") {
      expect(options?.method).toBe("POST");
      expect(JSON.parse(options?.body as string).token).toBe("test-access-token");
      authenticated = true;
      return reply({ ok: true });
    }
    if (!authenticated) return reply({ error: "Sign in to Ledger" }, 401);
    return reply(
      String(url).includes("summary") ? summary : String(url).includes("sync") ? sync : [],
    );
  });
  render(<App />);
  await screen.findByLabelText("Access token");
  fireEvent.input(screen.getByLabelText("Access token"), {
    target: { value: "test-access-token" },
  });
  fireEvent.click(screen.getByText("Open Ledger"));
  await screen.findByText("The ledger.");
  expect(fetcher.mock.calls.every(([url]) => !String(url).includes("test-access-token"))).toBe(
    true,
  );
});
it("shows the configured Bank label and disables sync until credentials exist", async () => {
  location.hash = "#connection";
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    reply(String(url).includes("summary") ? summary : sync),
  );
  render(<App />);
  await screen.findByText("Gmail connection");
  expect(screen.getByText("Check mail now").closest("button")?.disabled).toBe(true);
  expect(screen.getAllByText("Bank").length).toBeGreaterThan(0);
});
it("shows an empty ledger rather than fabricated financial data", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    reply(String(url).includes("summary") ? summary : String(url).includes("sync") ? sync : []),
  );
  render(<App />);
  await waitFor(() => expect(screen.getByText("Your first alert starts the story.")).toBeTruthy());
  expect(screen.queryByText("Example Shop")).toBeNull();
});
it("formats integer paise without dropping the fractional amount", () => {
  expect(money(123456, "INR")).toBe("₹1,234.56");
  expect(money(1, "INR")).toBe("₹0.01");
  expect(money(12345678, "INR")).toBe("₹1,23,456.78");
  expect(money(123456789012, "INR")).toBe("₹1,23,45,67,890.12");
  expect(money(12345678, "USD")).toBe("USD 123,456.78");
});
it("keeps the transactions on screen while the ledger refreshes", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
    reply([
      {
        id: 1,
        message_id: 1,
        merchant: "Example Shop",
        account: "4242",
        amount: 100,
        currency: "INR",
        direction: "debit",
        date: "2026-10-01",
        reference: "",
        status: "provisional",
        issuer: "Example",
      },
    ]),
  );
  const { rerender } = render(<TransactionsPage summary={summary} version={0} />);
  await screen.findByText("Example Shop");
  // A refresh bumps the version; the list must not blank out meanwhile.
  rerender(<TransactionsPage summary={summary} version={1} />);
  expect(screen.queryByText("Loading activity…")).toBeNull();
  expect(screen.getByText("Example Shop")).toBeTruthy();
});

it("marks a transaction as a transfer from its actions", async () => {
  const posts: string[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (options?.method === "POST") posts.push(String(url));
    return reply([
      {
        id: 7,
        message_id: 1,
        merchant: "SWEEP TFR DR",
        account: "4556",
        amount: 500000,
        currency: "INR",
        direction: "debit",
        date: "2026-10-01",
        reference: "",
        status: "confirmed",
        issuer: "SBI",
      },
    ]);
  });
  render(<TransactionsPage summary={summary} version={0} />);
  fireEvent.click(await screen.findByRole("button", { name: "Actions for SWEEP TFR DR" }));
  fireEvent.click(screen.getByRole("button", { name: /Treat everything like “SWEEP TFR DR”/ }));
  await waitFor(() => expect(posts).toEqual(["/api/transactions/7/transfer-rule"]));
});

it("shows card payments waiting for, or missing, the card's credit", async () => {
  const row = {
    message_id: 1,
    account: "9001",
    currency: "INR",
    direction: "debit",
    reference: "",
    status: "confirmed",
    issuer: "HDFC",
  };
  vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
    reply([
      {
        ...row,
        id: 1,
        merchant: "IB BILLPAY DR-HDFCWI-400000XXXXXX4242",
        amount: 100000,
        date: "2026-11-14",
        transfer: "pending",
      },
      {
        ...row,
        id: 2,
        merchant: "IB BILLPAY DR-HDFCWI-400000XXXXXX8080",
        amount: 200000,
        date: "2026-09-14",
        card_payment: true,
      },
    ]),
  );
  render(<TransactionsPage summary={summary} version={0} />);
  expect(await screen.findByText("card payment")).toBeTruthy();
  expect(screen.getByText("no card credit")).toBeTruthy();
});

it("shows a month's insights, and switches the trend to one category", async () => {
  const urls: string[] = [];
  const months = Array.from({ length: 12 }, (_, i) => ({
    month: `2026-${String(i + 1).padStart(2, "0")}`,
    amount: (i + 1) * 100000,
  }));
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    urls.push(String(url));
    return reply({
      month: "2026-12",
      currency: "INR",
      total: 1200000,
      count: 3,
      refunds: 50000,
      previous: 1100000,
      average: 850000,
      baseline_count: 6,
      days: 31,
      days_in_month: 31,
      current: false,
      months,
      categories: [
        {
          category_id: 7,
          category: "Gacha",
          amount: 900000,
          count: 2,
          previous: 0,
          average: 300000,
          refunds: 50000,
          months: months.map((_, i) => (i === 11 ? 900000 : 0)),
        },
        {
          category_id: 0,
          category: "",
          amount: 300000,
          count: 1,
          previous: 0,
          average: 0,
          months: [],
        },
      ],
      daily: Array(31).fill(0),
      previous_daily: Array(30).fill(0),
      largest: [],
      insights: [{ text: "Gacha: ₹9,000, 3× its usual ₹3,000.", tone: "up" }],
    });
  });
  render(<InsightsPage version={0} month="2026-12" />);
  expect(await screen.findByText("Gacha: ₹9,000, 3× its usual ₹3,000.")).toBeTruthy();
  expect(screen.getByText("+200%")).toBeTruthy();
  expect(screen.getByText("After ₹500.00 of refunds and money back")).toBeTruthy();
  expect(screen.getByText("−₹500 back")).toBeTruthy();
  expect(screen.getByRole("link", { name: /^₹9,000\.00/ }).getAttribute("href")).toBe(
    "#transactions?category=7&period=month-2026-12",
  );
  fireEvent.click(screen.getByRole("button", { name: "Gacha" }));
  expect(screen.getByText("Gacha by month")).toBeTruthy();
  // Picking a month's column loads that month.
  fireEvent.click(screen.getByRole("button", { name: /^March 2026/ }));
  await waitFor(() => expect(urls.at(-1)).toBe("/api/insights?month=2026-03"));
});

it("turns periods into date ranges", () => {
  const today = new Date(2026, 9, 3); // 3 October 2026
  expect(periodRange("this-month", today)).toEqual({ from: "2026-10-01", to: "2026-10-31" });
  expect(periodRange("last-month", today)).toEqual({ from: "2026-09-01", to: "2026-09-30" });
  expect(periodRange("months-3", today)).toEqual({ from: "2026-08-01", to: "2026-10-31" });
  expect(periodRange("months-12", today)).toEqual({ from: "2025-11-01", to: "2026-10-31" });
  expect(periodRange("this-year", today)).toEqual({ from: "2026-01-01", to: "2026-12-31" });
  expect(periodRange("month-2026-02", today)).toEqual({ from: "2026-02-01", to: "2026-02-28" });
  expect(periodRange("", today)).toEqual({ from: "", to: "" });
  expect(recentMonths(2, today).map((m) => m.value)).toEqual(["month-2026-10", "month-2026-09"]);
});

it("filters transactions by a period from the menu", async () => {
  const urls: string[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    urls.push(String(url));
    return reply([]);
  });
  render(<TransactionsPage summary={summary} version={0} />);
  fireEvent.change(await screen.findByLabelText("Period"), { target: { value: "last-month" } });
  const { from, to } = periodRange("last-month");
  await waitFor(() =>
    expect(urls.some((u) => u.includes(`from=${from}`) && u.includes(`to=${to}`))).toBe(true),
  );
  expect(screen.queryByLabelText("From date")).toBeNull();
  fireEvent.change(screen.getByLabelText("Period"), { target: { value: "custom" } });
  expect(screen.getByLabelText("From date")).toBeTruthy();
});

it("lists card dues and marks one paid", async () => {
  const calls: { url: string; body: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, body: String(init?.body ?? "") });
      return new Response("{}");
    }),
  );
  const saved = vi.fn();
  const due = {
    account_id: "key",
    issuer: "Example Bank",
    last_four: "4242",
    statement_date: "2026-09-20",
    due_date: "2026-10-08",
    currency: "INR",
    total_due: 1234500,
    minimum_due: 61700,
    paid: 0,
    remaining: 1234500,
    days: 5,
    settled: false,
    status: "due" as const,
  };
  render(
    <CardDues
      summary={{
        ...summary,
        dues: [due],
        reminders: { enabled: true, days: [0, 1, 5], on_statement: false },
      }}
      saved={saved}
    />,
  );
  expect(screen.getByText("Due in 5 days")).toBeTruthy();
  expect(screen.getByText(money(1234500, "INR"))).toBeTruthy();
  expect(screen.getByText("Reminders 5, 1, 0 days before")).toBeTruthy();
  fireEvent.click(screen.getByText("Mark paid"));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  expect(calls[0].url).toBe("/api/dues/settle");
  expect(JSON.parse(calls[0].body)).toEqual({
    account_id: "key",
    due_date: "2026-10-08",
    settled: true,
  });
});

it("saves reminder days and the new-statement notice from the Connection page", async () => {
  const calls: { url: string; body: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, body: String(init?.body ?? "") });
      return new Response("{}");
    }),
  );
  const saved = vi.fn();
  render(
    <ConnectionPage
      sync={sync}
      busy={false}
      syncNow={() => {}}
      reminders={{ enabled: true, days: [0, 1, 5], on_statement: false }}
      saved={saved}
    />,
  );
  const days = screen.getByDisplayValue("5, 1, 0");
  fireEvent.input(days, { target: { value: "7, x" } });
  fireEvent.click(screen.getByText("Save reminder settings"));
  expect(screen.getByText("Enter whole numbers of days, separated by commas")).toBeTruthy();
  fireEvent.input(days, { target: { value: "7, 2, 0" } });
  fireEvent.click(screen.getByLabelText("Also notify when a card’s new statement arrives"));
  fireEvent.click(screen.getByText("Save reminder settings"));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  expect(calls[0].url).toBe("/api/reminders/settings");
  expect(JSON.parse(calls[0].body)).toEqual({ days: [7, 2, 0], on_statement: true });
});
