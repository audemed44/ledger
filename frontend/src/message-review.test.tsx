import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AlertParserEditor } from "./components/AlertParserEditor";
import { AlertParsers } from "./components/AlertParsers";
import { DebitCards } from "./components/DebitCards";
import { MessageReview } from "./components/MessageReview";
import { blankParser } from "./lib";
import { App } from "./App";
import type { Message } from "./types";
const message: Message = {
  id: 1,
  sender: "alerts@example.invalid",
  subject: "Example card alert",
  date: "2026-10-03T00:00:00Z",
  state: "queued",
  reason: "No matching alert parser",
  body: "INR 123.45 at Example Shop card 4242",
  body_format: "html",
  can_parse: true,
};
function reply(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.open = true;
    },
  });
});
afterEach(() => {
  cleanup();
  Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal");
  vi.restoreAllMocks();
  location.hash = "";
});
it("opens readable email text before parser settings, including HTML-only mail", async () => {
  location.hash = "#inbox";
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (url === "/api/summary")
      return reply({
        totals: [],
        accounts: [],
        transactions: 0,
        queued: 1,
        parsers: 0,
        month: "2026-10",
        demo: false,
      });
    if (url === "/api/sync")
      return reply({
        configured: true,
        running: false,
        last_sync: "",
        error: "",
        label: "Bank",
        interval: "15m",
      });
    if (url === "/api/messages") return reply([message]);
    if (url === "/api/messages/1") return reply(message);
    throw Error("Unexpected URL");
  });
  // Flush authentication and queue effects before querying the loaded inbox.
  // A synchronous render leaves nested effects to JSDOM frame scheduling.
  await act(async () => {
    render(<App />);
  });
  fireEvent.click(await screen.findByRole("button", { name: "Review" }));
  const text = await screen.findByLabelText("Email text");
  expect((text as HTMLTextAreaElement).value).toBe(message.body);
  expect(screen.queryByLabelText("Body pattern")).toBeNull();
  expect(screen.getByText(/Text extracted from HTML/)).toBeTruthy();
  fireEvent.click(await screen.findByRole("button", { name: "Create alert parser" }));
  await screen.findByText("Teach your ledger.");
  expect(screen.getByLabelText("Email text").textContent).toBe(message.body);
});

// select highlights text inside the example, as a user would.
function select(container: HTMLElement, text: string) {
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  let node = walker.nextNode() as Text;
  while (!node.data.includes(text)) node = walker.nextNode() as Text;
  const start = node.data.indexOf(text);
  const range = document.createRange();
  range.setStart(node, start);
  range.setEnd(node, start + text.length);
  document.getSelection()!.removeAllRanges();
  document.getSelection()!.addRange(range);
  document.dispatchEvent(new Event("selectionchange"));
}

it("builds an alert parser from text selected in the email", async () => {
  let request: any;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/parsers/from-example") {
      request = JSON.parse(options?.body as string);
      return reply({ pattern: "GENERATED", date_layout: "2-Jan-06", subject: "(?i)^Example$" });
    }
    if (url === "/api/parsers/preview") return reply({ matched: false, ignored: false });
    throw Error(`Unexpected URL: ${url}`);
  });
  const body = "Rs. 123.45 at Example Shop on card 4242 on 01-Oct-26.";
  render(
    <AlertParserEditor
      initial={{ ...blankParser }}
      message={{ ...message, body }}
      close={() => {}}
      saved={() => {}}
    />,
  );
  const text = screen.getByLabelText("Email text");
  for (const [label, value] of [
    ["Amount", " 123.45 "],
    ["Merchant", "Example Shop"],
    ["Card / account", "4242"],
  ]) {
    await act(async () => select(text, value));
    fireEvent.click(screen.getByRole("button", { name: new RegExp("^" + label) }));
  }
  expect(screen.getByText(/Still to tag: Date/)).toBeTruthy();
  expect(text.querySelectorAll("mark")).toHaveLength(3);
  await act(async () => select(text, "01-Oct-26"));
  fireEvent.click(screen.getByRole("button", { name: /^Date/ }));
  await waitFor(() => expect(request).toBeTruthy());
  expect(request.marks).toEqual([
    { field: "amount", start: 4, end: 10 },
    { field: "merchant", start: 14, end: 26 },
    { field: "account", start: 35, end: 39 },
    { field: "date", start: 43, end: 52 },
  ]);
  fireEvent.click(screen.getByText(/Advanced/));
  await waitFor(() =>
    expect((screen.getByLabelText(/Body pattern/) as HTMLTextAreaElement).value).toBe("GENERATED"),
  );
  expect((screen.getByLabelText(/Date layout/) as HTMLInputElement).value).toBe("2-Jan-06");
  expect((screen.getByLabelText(/Subject pattern/) as HTMLInputElement).value).toBe(
    "(?i)^Example$",
  );
});
it("renders hostile text inertly and shows an honest missing-content error", () => {
  render(
    <MessageReview
      message={{
        ...message,
        body: '<img src="https://tracker.invalid/pixel" onerror="alert(1)">',
        can_parse: false,
        content_error: "Archive could not be read",
      }}
      close={() => {}}
      createParser={() => {}}
    />,
  );
  expect(document.querySelector("img")).toBeNull();
  expect(screen.getByRole("alert").textContent).toContain("Archive could not be read");
  expect(screen.queryByRole("button", { name: "Create alert parser" })).toBeNull();
});
it("configures a named PDF parser using password slots, then shows account and text", async () => {
  const parser = {
    id: 7,
    name: "HDFC Credit Card",
    adapter: "hdfc-credit-card",
    password_slot: 2,
    balance_tolerance_paise: 99,
  };
  let saved: any;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/pdf-config")
      return reply({
        password_slots: [1, 2],
        adapters: [
          {
            id: "hdfc-credit-card",
            name: "HDFC Credit Card",
            description: "HDFC Tata Neu statement layout",
          },
        ],
      });
    if (url === "/api/statement-parsers" && options?.method === "POST") {
      saved = JSON.parse(options.body as string);
      return reply(parser);
    }
    if (url === "/api/statement-parsers") return reply([]);
    if (url === "/api/messages/1/pdf") {
      expect(JSON.parse(options?.body as string)).toEqual({
        part: 1,
        parser_id: 7,
      });
      return reply({
        text: "SYNTHETIC PDF TEXT",
        password_slot: 2,
        parser_name: parser.name,
        statement: {
          issuer: "HDFC",
          account: "4242",
          account_kind: "card",
          transactions: [],
          date: "2026-10-01",
          due_date: "2026-10-21",
          minimum_due: 5000,
          total_due: 100000,
          balanced: true,
          discrepancy: 0,
          warnings: [],
        },
      });
    }
    throw Error("Unexpected URL");
  });
  render(
    <MessageReview
      message={{
        ...message,
        has_pdf: true,
        can_parse: false,
        attachments: [{ part: 1, name: "statement.pdf", content_type: "application/pdf" }],
      }}
      close={() => {}}
      createParser={() => {}}
    />,
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Create PDF parser",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Create PDF parser" }));
  fireEvent.change(screen.getByLabelText(/PDF password/), {
    target: { value: "2" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save parser configuration" }));
  await screen.findByRole("button", { name: "Extract & validate PDF" });
  fireEvent.click(screen.getByRole("button", { name: "Extract & validate PDF" }));
  const text = await screen.findByLabelText("Extracted PDF text");
  expect((text as HTMLTextAreaElement).value).toBe("SYNTHETIC PDF TEXT");
  expect(screen.getByText("HDFC · Credit card •• 4242")).toBeTruthy();
  expect(saved.name).toBe("HDFC Credit Card Parser v1");
  expect(saved.password_slot).toBe(2);
  expect(saved.password).toBeUndefined();
  expect(screen.queryByRole("button", { name: "Create alert parser" })).toBeNull();
});

it("imports only after an explicit action and refreshes the ledger", async () => {
  const imported = vi.fn();
  const preset = {
    id: 7,
    name: "HDFC Credit Card Parser v1",
    adapter: "hdfc-credit-card",
    password_slot: 0,
    balance_tolerance_paise: 99,
  };
  let importRequests = 0;
  const result = {
    text: "SYNTHETIC STATEMENT",
    fingerprint: "reviewed-fingerprint",
    password_slot: 0,
    statement: {
      transactions: [
        {
          date: "2026-10-01",
          merchant: "EXAMPLE SHOP",
          amount: 50000,
          currency: "INR",
          direction: "debit",
        },
      ],
      issuer: "HDFC",
      account: "4242",
      account_kind: "card",
      date: "2026-10-02",
      due_date: "2026-10-22",
      minimum_due: 5000,
      total_due: 50000,
      balanced: true,
      discrepancy: 0,
      warnings: [],
    },
  };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/statement-parsers") return reply([preset]);
    if (url === "/api/pdf-config") return reply({ password_slots: [], adapters: [] });
    if (url === "/api/messages/1/pdf") {
      const body = JSON.parse(options?.body as string);
      if (body.import) {
        expect(body.fingerprint).toBe(result.fingerprint);
        expect(body.automatic).toBe(true);
        importRequests++;
        return reply({
          ...result,
          parser_name: preset.name,
          automatic: true,
          imported: { statement_id: 1, count: 1, matched: 1, flagged: 0, already_imported: false },
        });
      }
      return reply(result);
    }
    throw Error(`Unexpected URL: ${url}`);
  });
  await act(async () => {
    render(
      <MessageReview
        message={{
          ...message,
          has_pdf: true,
          can_parse: false,
          attachments: [{ part: 0, name: "statement.pdf", content_type: "application/pdf" }],
        }}
        close={() => {}}
        createParser={() => {}}
        imported={imported}
      />,
    );
  });
  fireEvent.click(await screen.findByRole("button", { name: "Extract & validate PDF" }));
  const button = await screen.findByRole("button", {
    name: "Import 1 transactions",
  });
  expect(importRequests).toBe(0);
  expect(screen.getByText("EXAMPLE SHOP")).toBeTruthy();
  fireEvent.click(button);
  await screen.findByText(/Imported: 1 transactions. 1 confirmed an alert/);
  expect(screen.getByText(/will import statements like this one by itself/)).toBeTruthy();
  expect(importRequests).toBe(1);
  expect(imported).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "Import 1 transactions" })).toBeNull();
});

it("keeps partial PDF rows visible and offers no import on validation failure", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (url === "/api/statement-parsers")
      return reply([{ id: 1, name: "HDFC Credit Card Parser v1", password_slot: 0 }]);
    if (url === "/api/pdf-config") return reply({ password_slots: [], adapters: [] });
    return reply({
      text: "DIAGNOSTIC TEXT",
      password_slot: 0,
      parse_error: "unparsed transaction on line 12",
      statement: {
        transactions: [
          {
            date: "2026-10-01",
            merchant: "READABLE ROW",
            amount: 12345,
            currency: "INR",
            direction: "debit",
          },
        ],
        balanced: false,
        warnings: [],
      },
    });
  });
  await act(async () => {
    render(
      <MessageReview
        message={{
          ...message,
          has_pdf: true,
          can_parse: false,
          attachments: [{ part: 0, name: "statement.pdf", content_type: "application/pdf" }],
        }}
        close={() => {}}
        createParser={() => {}}
      />,
    );
  });
  fireEvent.click(await screen.findByRole("button", { name: "Extract & validate PDF" }));
  await screen.findByText("READABLE ROW");
  expect(screen.getByRole("alert").textContent).toContain("unparsed transaction on line 12");
  expect(screen.queryByRole("button", { name: /^Import / })).toBeNull();
  expect((screen.getByLabelText("Extracted PDF text") as HTMLTextAreaElement).value).toBe(
    "DIAGNOSTIC TEXT",
  );
});

it("reuses and deletes an existing PDF parser without creating another", async () => {
  const preset = {
    id: 7,
    name: "HDFC Credit Card Parser v1",
    adapter: "hdfc-credit-card",
    password_slot: 0,
    balance_tolerance_paise: 99,
  };
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/statement-parsers/7" && options?.method === "DELETE")
      return reply({ ok: true });
    if (url === "/api/statement-parsers") return reply([preset]);
    if (url === "/api/pdf-config")
      return reply({
        password_slots: [],
        adapters: [{ id: "hdfc-credit-card", name: preset.name }],
      });
    throw Error(`Unexpected URL: ${url}`);
  });
  vi.spyOn(window, "confirm").mockReturnValue(true);
  await act(async () => {
    render(
      <MessageReview
        message={{
          ...message,
          has_pdf: true,
          can_parse: false,
          attachments: [{ part: 0, name: "statement.pdf", content_type: "application/pdf" }],
        }}
        close={() => {}}
        createParser={() => {}}
      />,
    );
  });
  await waitFor(() =>
    expect((screen.getByLabelText("PDF parser") as HTMLSelectElement).value).toBe("7"),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Edit selected parser" }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete PDF parser" }));
  await screen.findByRole("button", { name: "Extract PDF text" });
  expect(fetcher.mock.calls.filter(([, o]) => o?.method === "POST")).toHaveLength(0);
  expect(screen.queryByRole("option", { name: /HDFC Credit Card Parser v1/ })).toBeNull();
});

it("requests inbox filters from the server", async () => {
  location.hash = "#inbox";
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (url === "/api/summary")
      return reply({
        totals: [],
        accounts: [],
        transactions: 0,
        queued: 1,
        parsers: 1,
        month: "2026-10",
        demo: false,
      });
    if (url === "/api/sync") return reply({ configured: true, running: false, label: "Bank" });
    if (url === "/api/messages?kind=pdf") return reply([{ ...message, subject: "PDF statement" }]);
    if (url === "/api/messages?kind=text") return reply([{ ...message, subject: "Text alert" }]);
    return reply([]);
  });
  await act(async () => {
    render(<App />);
  });
  fireEvent.change(await screen.findByLabelText("Inbox filter"), {
    target: { value: "pdf" },
  });
  await screen.findByText("PDF statement");
  fireEvent.change(await screen.findByLabelText("Inbox filter"), {
    target: { value: "text" },
  });
  await screen.findByText("Text alert");
  expect(fetcher.mock.calls.some(([url]) => url === "/api/messages?kind=pdf")).toBe(true);
  expect(screen.queryByText("PDF statement")).toBeNull();
});

it("uploads a PDF statement from the inbox and opens it for review", async () => {
  location.hash = "#inbox";
  let sent: FormData | undefined;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/summary")
      return reply({
        totals: [],
        accounts: [],
        transactions: 0,
        queued: 0,
        parsers: 1,
        month: "2026-10",
        demo: false,
      });
    if (url === "/api/sync") return reply({ configured: true, running: false, label: "Bank" });
    if (url === "/api/statements/upload") {
      sent = options?.body as FormData;
      return reply({ id: 9, state: "queued", message: "Saved to the Inbox for review" });
    }
    if (url === "/api/messages/9")
      return reply({ ...message, id: 9, has_pdf: true, can_parse: false, attachments: [] });
    if (url === "/api/statement-parsers") return reply([]);
    if (url === "/api/pdf-config") return reply({ password_slots: [], adapters: [] });
    return reply([]);
  });
  await act(async () => {
    render(<App />);
  });
  const input = (await screen.findByText(/Upload PDF/)).querySelector("input")!;
  const file = new File(["%PDF-1.4"], "statement.pdf", { type: "application/pdf" });
  await act(async () => {
    fireEvent.change(input, { target: { files: [file] } });
  });
  await screen.findByText("Saved to the Inbox for review");
  expect((sent!.get("file") as File).name).toBe("statement.pdf");
  await screen.findByText("Review and import the PDF");
});

it("ignores emails like this one after confirming", async () => {
  const done = vi.fn(),
    closed = vi.fn();
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async () => reply({ parser: {}, processed: 3 }));
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(
    <MessageReview
      message={{ ...message, subject: "482913 is your OTP" }}
      close={closed}
      createParser={() => {}}
      imported={done}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: /Ignore emails like this/ }));
  await waitFor(() => expect(closed).toHaveBeenCalled());
  expect(fetcher.mock.calls[0][0]).toBe("/api/messages/1/ignore");
  expect(done).toHaveBeenCalled();
});

it("links a debit card to its bank account", async () => {
  const saved = vi.fn();
  let links: unknown[] = [];
  let posted: any;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/card-links" && options?.method === "POST") {
      posted = JSON.parse(options.body as string);
      links = [posted];
      return reply({ ok: true });
    }
    return reply(links);
  });
  render(<DebitCards saved={saved} />);
  fireEvent.input(screen.getByLabelText(/Issuer/), { target: { value: "HDFC" } });
  fireEvent.input(screen.getByLabelText(/Card, last 4/), { target: { value: "4242" } });
  fireEvent.input(screen.getByLabelText(/Bank account, last 4/), { target: { value: "9001" } });
  fireEvent.click(screen.getByRole("button", { name: "Link card" }));
  await screen.findByText("HDFC debit card •• 4242");
  expect(posted).toEqual({ issuer: "HDFC", card: "4242", account: "9001" });
  expect(saved).toHaveBeenCalled();
});

it("re-reads a parser's emails when its mistake is fixed", async () => {
  let saveURL = "";
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (url === "/api/parsers/3/handled") return reply({ emails: 4, confirmed: 1 });
    if (String(url).startsWith("/api/parsers?")) {
      saveURL = String(url);
      return reply({ ...blankParser, id: 3, reread: 3, kept: 1 });
    }
    return reply({ processed: 0 });
  });
  const saved = vi.fn();
  render(
    <AlertParserEditor
      initial={{ ...blankParser, id: 3, name: "Card", sender: "a@example.invalid", pattern: "x" }}
      close={() => {}}
      saved={saved}
    />,
  );
  const option = await screen.findByLabelText(/Re-read the 4 emails/);
  expect((option as HTMLInputElement).checked).toBe(true);
  expect(screen.getByText(/1 confirmed by a statement/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /Save & retry backlog/ }));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  expect(saveURL).toBe("/api/parsers?reread=1");
});

it("builds a parser for alerts that name no merchant", async () => {
  let request: any;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/parsers/from-example") {
      request = JSON.parse(options?.body as string);
      return reply({ pattern: "GENERATED", date_layout: "2-Jan-2006", subject: "" });
    }
    return reply({ matched: false, ignored: false });
  });
  const body = "We have received payment of INR 600.00 on card 4242 on 01-Oct-2026.";
  render(
    <AlertParserEditor
      initial={{ ...blankParser }}
      message={{ ...message, body }}
      close={() => {}}
      saved={() => {}}
    />,
  );
  const text = screen.getByLabelText("Email text");
  for (const [label, value] of [
    ["Amount", "600.00"],
    ["Card / account", "4242"],
    ["Date", "01-Oct-2026"],
  ]) {
    await act(async () => select(text, value));
    fireEvent.click(screen.getByRole("button", { name: new RegExp("^" + label) }));
  }
  expect(screen.getByText(/Still to tag: Merchant/)).toBeTruthy();
  fireEvent.input(screen.getByLabelText(/Description when the email names no merchant/), {
    target: { value: "Payment received" },
  });
  await waitFor(() => expect(request).toBeTruthy());
  expect(request.marks.map((m: any) => m.field)).toEqual(["amount", "account", "date"]);
});

it("adds an email's wording to an existing parser, or merges parsers", async () => {
  const sibling = { ...blankParser, id: 5, name: "HDFC UPI", sender: message.sender, pattern: "x" };
  const posts: [string, any][] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (url === "/api/parsers") return reply([sibling]);
    if (options?.method === "POST") posts.push([String(url), JSON.parse(options.body as string)]);
    return reply({ matched: false, ignored: false });
  });
  const saved = vi.fn();
  render(
    <AlertParserEditor
      initial={{ ...blankParser, pattern: "NEW WORDING", date_layout: "2-1-06" }}
      message={message}
      close={() => {}}
      saved={saved}
    />,
  );
  fireEvent.change(await screen.findByLabelText(/Save as/), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: /Save & retry backlog/ }));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  expect(posts[0]).toEqual([
    "/api/parsers/5/wordings",
    { pattern: "NEW WORDING", date_layout: "2-1-06", account_kind: "card" },
  ]);
  cleanup();

  // Editing parser 6 offers merging parser 5 into it.
  posts.length = 0;
  render(
    <AlertParserEditor
      initial={{ ...sibling, id: 6, name: "HDFC UPI 2" }}
      close={() => {}}
      saved={saved}
    />,
  );
  fireEvent.change(await screen.findByLabelText(/Merge another parser/), {
    target: { value: "5" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Merge" }));
  await waitFor(() => expect(posts.length).toBeGreaterThan(0));
  expect(posts[0]).toEqual(["/api/parsers/6/merge", { from: 5 }]);
});

it("lists ignore rules separately and filters parsers by issuer and search", async () => {
  const p = (id: number, name: string, issuer: string, sender: string, direction = "debit") => ({
    ...blankParser,
    id,
    name,
    issuer,
    sender,
    direction,
    subject: "",
    pattern: "x",
  });
  vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
    reply([
      p(1, "HDFC · UPI payment", "HDFC", "alerts@hdfc.invalid"),
      p(2, "SBI · NACH debit", "SBI", "alerts@sbi.invalid"),
      p(3, "Ignore · HDFC · OTP", "", "alerts@hdfc.invalid", "ignore"),
    ]),
  );
  render(<AlertParsers version={0} edit={() => {}} />);
  await screen.findByText("HDFC · UPI payment");
  // The ignore rule is in its own section, filed under its sender's issuer.
  expect(screen.getByText("Ignore rules")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Issuer"), { target: { value: "SBI" } });
  expect(screen.queryByText("HDFC · UPI payment")).toBeNull();
  expect(screen.queryByText(/Ignore · HDFC · OTP/)).toBeNull();
  fireEvent.change(screen.getByLabelText("Issuer"), { target: { value: "HDFC" } });
  expect(screen.getByText(/Ignore · HDFC · OTP/)).toBeTruthy();
  fireEvent.input(screen.getByLabelText("Search parsers"), { target: { value: "otp" } });
  expect(screen.queryByText("HDFC · UPI payment")).toBeNull();
  expect(screen.getByText(/Ignore · HDFC · OTP/)).toBeTruthy();
});

it("dismisses an email from the inbox list", async () => {
  location.hash = "#inbox";
  const posts: [string, any][] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, options) => {
    if (options?.method === "POST") posts.push([String(url), JSON.parse(options.body as string)]);
    if (url === "/api/summary")
      return reply({
        totals: [],
        accounts: [],
        transactions: 0,
        queued: 1,
        parsers: 1,
        month: "2026-10",
        demo: false,
      });
    if (url === "/api/sync") return reply({ configured: true, running: false, label: "Bank" });
    if (url === "/api/messages") return reply([message]);
    return reply({ ok: true });
  });
  await act(async () => {
    render(<App />);
  });
  fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
  await screen.findByText(/Dismissed “Example card alert”/);
  expect(posts).toEqual([["/api/messages/1/dismiss", { dismissed: true }]]);
});
