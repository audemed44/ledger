import { useState } from "preact/hooks";
import { BellRing, RefreshCw, Save } from "lucide-preact";
import { api } from "../api";
import type { Summary, Sync } from "../types";
import { ErrorNote, Field, Section } from "./ui";

export function ConnectionPage({
  sync,
  busy,
  syncNow,
  reminders,
  saved,
}: {
  sync: Sync;
  busy: boolean;
  syncNow: () => void;
  reminders: Summary["reminders"];
  saved: () => void;
}) {
  // The form's edits, until saved; the summary refreshes underneath it.
  const [draft, setDraft] = useState<{ days: string; on_statement: boolean } | null>(null);
  const [saving, setSaving] = useState<{ busy?: boolean; error?: string; done?: boolean }>({});
  const form = draft ?? {
    days: [...(reminders.days ?? [])].reverse().join(", "),
    on_statement: reminders.on_statement,
  };
  async function saveSettings() {
    const parts = form.days
      .split(",")
      .map((d) => d.trim())
      .filter(Boolean);
    if (parts.some((d) => !/^\d+$/.test(d))) {
      setSaving({ error: "Enter whole numbers of days, separated by commas" });
      return;
    }
    setSaving({ busy: true });
    try {
      await api("reminders/settings", { days: parts.map(Number), on_statement: form.on_statement });
      setDraft(null);
      setSaving({ done: true });
      saved();
    } catch (e) {
      setSaving({ error: (e as Error).message });
    }
  }
  const [test, setTest] = useState<{ busy?: boolean; error?: string; sent?: boolean }>({});
  async function sendTest() {
    setTest({ busy: true });
    try {
      await api("reminders/test", {});
      setTest({ sent: true });
    } catch (e) {
      setTest({ error: (e as Error).message });
    }
  }
  return (
    <>
      <section>
        <Section index="01" title="Gmail connection">
          <span class="chip">{sync.configured ? "Configured" : "Setup required"}</span>
        </Section>
        <div class="connection-grid">
          <div>
            <h2 class="subheading">
              One label.
              <br />
              Everything in its place.
            </h2>
            <p class="lede">
              Ledger checks only the label you choose. Original emails and attachments are archived
              on your server.
            </p>
            <div class="connection-facts">
              <div>
                <span class="eyebrow">Label</span>
                <strong>{sync.label}</strong>
              </div>
              <div>
                <span class="eyebrow">Check interval</span>
                <strong>{sync.interval}</strong>
              </div>
              <div>
                <span class="eyebrow">Last successful check</span>
                <strong>
                  {sync.last_sync ? new Date(sync.last_sync).toLocaleString() : "Not yet"}
                </strong>
              </div>
            </div>
            <ErrorNote error={sync.error} />
            <button
              class="btn primary"
              onClick={syncNow}
              disabled={!sync.configured || busy || sync.running}
            >
              <RefreshCw size={15} class={busy ? "spin" : ""} />
              {busy || sync.running ? "Checking mail…" : "Check mail now"}
            </button>
          </div>
          <div class="setup">
            <h3>A one-time setup</h3>
            <ol>
              <li>Create a Gmail filter for your banks’ alert and statement sender addresses.</li>
              <li>
                Apply the <strong>{sync.label}</strong> label. Select “Also apply filter to matching
                conversations” if you want past emails available.
              </li>
              <li>
                Add your Gmail address and app password to the stack’s <code>.env</code>, then
                restart Ledger. Set <code>LEDGER_BACKFILL=true</code> before the first sync to
                import existing mail dated 1 January 2026 onward.
              </li>
              <li>
                Open{" "}
                <a class="text-link" href="#inbox">
                  Needs review
                </a>{" "}
                to teach Ledger each bank’s format.
              </li>
            </ol>
            <p class="hint">
              An app password can access the whole mailbox. Ledger reads only the configured label.
              Credentials are never saved in the database.
            </p>
          </div>
        </div>
      </section>
      <section>
        <Section index="02" title="Payment reminders">
          <span class="chip">{reminders.enabled ? "On" : "Off"}</span>
        </Section>
        <div class="connection-grid">
          <div>
            <p class="lede">
              Before each card’s due date, Ledger sends what’s left to pay to your notification
              service, until the payments cover the statement or you mark it paid.
            </p>
            <div class="reminder-settings">
              <Field
                label="Days before the due date"
                hint="Separated by commas, each 0–30; 0 is the due date. Reminders go out from 9:00."
              >
                <input
                  inputMode="numeric"
                  value={form.days}
                  onInput={(e) => setDraft({ ...form, days: e.currentTarget.value })}
                />
              </Field>
              <label class="check">
                <input
                  type="checkbox"
                  checked={form.on_statement}
                  onChange={(e) => setDraft({ ...form, on_statement: e.currentTarget.checked })}
                />
                Also notify when a card’s new statement arrives
              </label>
              <div class="connection-facts">
                <div>
                  <span class="eyebrow">When overdue</span>
                  <strong>Once, the next day</strong>
                </div>
              </div>
            </div>
            <ErrorNote error={saving.error ?? test.error ?? ""} />
            {saving.done && !draft && <p class="hint">Reminder settings saved.</p>}
            {test.sent && <p class="hint">Test reminder sent.</p>}
            <div class="actions">
              <button class="btn primary" onClick={saveSettings} disabled={!draft || saving.busy}>
                <Save size={15} /> {saving.busy ? "Saving…" : "Save reminder settings"}
              </button>
              <button class="btn" onClick={sendTest} disabled={!reminders.enabled || test.busy}>
                <BellRing size={15} /> {test.busy ? "Sending…" : "Send a test reminder"}
              </button>
            </div>
          </div>
          <div class="setup">
            <h3>Turning them on</h3>
            <ol>
              <li>
                Set <code>LEDGER_NOTIFY_URL</code> to an Apprise API endpoint, such as Lookout’s{" "}
                <code>http://lookout:8080/notify/ledger</code>, then restart Ledger.
              </li>
              <li>
                Choose the days here. Reminders go out from 9:00 in Ledger’s <code>TZ</code>.
              </li>
            </ol>
          </div>
        </div>
      </section>
    </>
  );
}
