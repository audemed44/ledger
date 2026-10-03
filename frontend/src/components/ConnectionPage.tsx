import { useState } from "preact/hooks";
import { BellRing, RefreshCw } from "lucide-preact";
import { api } from "../api";
import type { Summary, Sync } from "../types";
import { ErrorNote, Section } from "./ui";

export function ConnectionPage({
  sync,
  busy,
  syncNow,
  reminders,
}: {
  sync: Sync;
  busy: boolean;
  syncNow: () => void;
  reminders: Summary["reminders"];
}) {
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
            {reminders.enabled ? (
              <div class="connection-facts">
                <div>
                  <span class="eyebrow">Days before the due date</span>
                  <strong>{[...(reminders.days ?? [])].reverse().join(", ")}</strong>
                </div>
                <div>
                  <span class="eyebrow">New statement</span>
                  <strong>{reminders.on_statement ? "Notify when it arrives" : "Off"}</strong>
                </div>
                <div>
                  <span class="eyebrow">When overdue</span>
                  <strong>Once, the next day</strong>
                </div>
              </div>
            ) : null}
            <ErrorNote error={test.error ?? ""} />
            {test.sent && <p class="hint">Test reminder sent.</p>}
            <button class="btn" onClick={sendTest} disabled={!reminders.enabled || test.busy}>
              <BellRing size={15} /> {test.busy ? "Sending…" : "Send a test reminder"}
            </button>
          </div>
          <div class="setup">
            <h3>Turning them on</h3>
            <ol>
              <li>
                Set <code>LEDGER_NOTIFY_URL</code> to an Apprise API endpoint, such as Lookout’s{" "}
                <code>http://lookout:8080/notify/ledger</code>, then restart Ledger.
              </li>
              <li>
                Optionally set <code>LEDGER_REMINDER_DAYS</code> (default <code>5,1,0</code>).
                Reminders go out from 9:00 in Ledger’s <code>TZ</code>.
              </li>
              <li>
                Optionally set <code>LEDGER_REMINDER_ON_STATEMENT=true</code> to also be told when a
                card’s new statement arrives.
              </li>
            </ol>
          </div>
        </div>
      </section>
    </>
  );
}
