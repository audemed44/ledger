import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { ErrorNote } from "./ui";

type Rule = { id: number; issuer: string; pattern: string; example: string };

// TransferRules lists the rules that treat lines like a description as
// transfers (SBI's sweeps into deposits, say). They're made from a
// transaction's actions.
export function TransferRules({ version, saved }: { version: number; saved: () => void }) {
  const [rules, setRules] = useState<Rule[]>([]),
    [error, setError] = useState("");
  function load() {
    api<Rule[]>("transfer-rules")
      .then(setRules)
      .catch((e) => setError(e.message));
  }
  useEffect(load, [version]);
  return (
    <section class="statement-section">
      <div class="section-head">
        <span class="section-index">04</span>
        <h2>Transfer rules</h2>
      </div>
      <p class="hint">
        Transfers between your own accounts are paired by themselves: the same amount leaving one
        account and arriving in another within 3 days. For money moving to an account Ledger doesn’t
        see, such as an auto-sweep into deposits, open a transaction’s actions and choose “Treat
        everything like this as transfers”. Transfers stay listed, but out of spending.
      </p>
      <ErrorNote error={error} />
      {rules.length ? (
        rules.map((r) => (
          <div class="trigger" key={r.id}>
            <span>
              {r.issuer} · like “{r.example}”<span class="hint mono">{r.pattern}</span>
            </span>
            <button
              class="btn"
              onClick={async () => {
                try {
                  await api(`transfer-rules/${r.id}`, undefined, "DELETE");
                  load();
                  saved();
                } catch (e) {
                  setError((e as Error).message);
                }
              }}
            >
              Remove
            </button>
          </div>
        ))
      ) : (
        <p class="hint">No rules yet.</p>
      )}
    </section>
  );
}
