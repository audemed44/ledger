import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/preact";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "./main";
import { money } from "./api";
const summary = {
  totals: [],
  accounts: [],
  transactions: 0,
  queued: 0,
  parsers: 0,
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
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (url, options) => {
      if (url === "/api/login") {
        expect(options?.method).toBe("POST");
        expect(JSON.parse(options?.body as string).token).toBe(
          "test-access-token",
        );
        authenticated = true;
        return reply({ ok: true });
      }
      if (!authenticated) return reply({ error: "Sign in to Ledger" }, 401);
      return reply(
        String(url).includes("summary")
          ? summary
          : String(url).includes("sync")
            ? sync
            : [],
      );
    });
  render(<App />);
  await screen.findByLabelText("Access token");
  fireEvent.input(screen.getByLabelText("Access token"), {
    target: { value: "test-access-token" },
  });
  fireEvent.click(screen.getByText("Open Ledger"));
  await screen.findByText("The ledger.");
  expect(
    fetcher.mock.calls.every(
      ([url]) => !String(url).includes("test-access-token"),
    ),
  ).toBe(true);
});
it("shows the configured Bank label and disables sync until credentials exist", async () => {
  location.hash = "#connection";
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    reply(String(url).includes("summary") ? summary : sync),
  );
  render(<App />);
  await screen.findByText("Gmail connection");
  expect(screen.getByText("Check mail now").closest("button")?.disabled).toBe(
    true,
  );
  expect(screen.getAllByText("Bank").length).toBeGreaterThan(0);
});
it("shows an empty ledger rather than fabricated financial data", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    reply(
      String(url).includes("summary")
        ? summary
        : String(url).includes("sync")
          ? sync
          : [],
    ),
  );
  render(<App />);
  await waitFor(() =>
    expect(screen.getByText("Your first alert starts the story.")).toBeTruthy(),
  );
  expect(screen.queryByText("Example Shop")).toBeNull();
});
it("formats integer paise without dropping the fractional amount", () => {
  expect(money(123456, "INR")).toBe("₹1,234.56");
  expect(money(1, "INR")).toBe("₹0.01");
});
