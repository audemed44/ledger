import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { ErrorNote, Field } from "./ui";

type Link = { issuer: string; card: string; account: string };

// DebitCards links each debit card to the bank account it draws on, so its
// alerts are recorded on that account and reconcile with its statements.
export function DebitCards({ saved }: { saved: () => void }) {
  const [links, setLinks] = useState<Link[]>([]),
    [draft, setDraft] = useState<Link>({ issuer: "", card: "", account: "" }),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  function load() {
    api<Link[]>("card-links")
      .then(setLinks)
      .catch((e) => setError(e.message));
  }
  useEffect(load, []);
  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      load();
      saved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section class="statement-section">
      <div class="section-head">
        <span class="section-index">03</span>
        <h2>Debit cards</h2>
      </div>
      <p class="hint">
        Alert parsers with the Debit card account type record purchases on the bank account the card
        draws on. Link each card here; its alerts wait in the Inbox until it is.
      </p>
      <ErrorNote error={error} />
      {links.map((l) => (
        <div class="trigger" key={l.issuer + l.card}>
          <span>
            {l.issuer.toUpperCase()} debit card •• {l.card}
            <span class="hint">Bank account •• {l.account}</span>
          </span>
          <button
            class="btn"
            disabled={busy}
            onClick={() =>
              run(() => api("card-links", { issuer: l.issuer, card: l.card }, "DELETE"))
            }
          >
            Remove
          </button>
        </div>
      ))}
      <form
        class="card-link-form"
        onSubmit={(e) => {
          e.preventDefault();
          run(async () => {
            await api("card-links", draft);
            setDraft({ issuer: "", card: "", account: "" });
          });
        }}
      >
        <Field label="Issuer" hint="As on the alert parser, e.g. HDFC">
          <input
            required
            value={draft.issuer}
            onInput={(e) => setDraft({ ...draft, issuer: e.currentTarget.value })}
          />
        </Field>
        <Field label="Card, last 4">
          <input
            required
            inputMode="numeric"
            pattern="[0-9]{4}"
            maxLength={4}
            value={draft.card}
            onInput={(e) => setDraft({ ...draft, card: e.currentTarget.value })}
          />
        </Field>
        <Field label="Bank account, last 4">
          <input
            required
            inputMode="numeric"
            pattern="[0-9]{4}"
            maxLength={4}
            value={draft.account}
            onInput={(e) => setDraft({ ...draft, account: e.currentTarget.value })}
          />
        </Field>
        <button class="btn primary" disabled={busy}>
          Link card
        </button>
      </form>
    </section>
  );
}
