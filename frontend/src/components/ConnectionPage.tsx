import { RefreshCw } from "lucide-preact";
import type { Sync } from "../types";
import { ErrorNote, Section } from "./ui";

export function ConnectionPage({
  sync,
  busy,
  syncNow,
}: {
  sync: Sync;
  busy: boolean;
  syncNow: () => void;
}) {
  return (
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
            Ledger checks only the label you choose. Original emails and attachments are archived on
            your server.
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
              Add your Gmail address and app password to the stack’s <code>.env</code>, then restart
              Ledger. Set <code>LEDGER_BACKFILL=true</code> before the first sync to import existing
              mail dated 1 January 2026 onward.
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
  );
}
