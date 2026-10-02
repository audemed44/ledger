import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/preact";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MessageReview } from "./MessageReview";
import { App } from "./main";
import type { Message } from "./api";
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
  fireEvent.click(
    await screen.findByRole("button", { name: "Create alert parser" }),
  );
  await screen.findByText("Teach your ledger.");
  expect(
    (screen.getByLabelText("Email text") as HTMLTextAreaElement).value,
  ).toBe(message.body);
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
  expect(screen.getByRole("alert").textContent).toContain(
    "Archive could not be read",
  );
  expect(
    screen.queryByRole("button", { name: "Create alert parser" }),
  ).toBeNull();
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
        attachments: [
          { part: 1, name: "statement.pdf", content_type: "application/pdf" },
        ],
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
  fireEvent.click(screen.getByRole("button", { name: "Save PDF parser" }));
  await screen.findByRole("button", { name: "Extract & validate PDF" });
  fireEvent.click(
    screen.getByRole("button", { name: "Extract & validate PDF" }),
  );
  const text = await screen.findByLabelText("Extracted PDF text");
  expect((text as HTMLTextAreaElement).value).toBe("SYNTHETIC PDF TEXT");
  expect(screen.getByText("HDFC · Credit card •• 4242")).toBeTruthy();
  expect(saved.name).toBe("HDFC Credit Card");
  expect(saved.password_slot).toBe(2);
  expect(saved.password).toBeUndefined();
  expect(
    screen.queryByRole("button", { name: "Create alert parser" }),
  ).toBeNull();
});
