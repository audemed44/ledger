import { useState } from "preact/hooks";
import { ArrowRight } from "lucide-preact";
import { api } from "../api";
import { ErrorNote, Field } from "./ui";

export function Login({ onDone }: { onDone: () => void }) {
  const [token, setToken] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function login(e: Event) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("login", { token });
      setToken("");
      onDone();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main class="login">
      <form onSubmit={login}>
        <span class="brand">
          <span class="brand-mark" />
          LEDGER
        </span>
        <div class="eyebrow">Private by design</div>
        <h1>
          Your money.
          <br />
          Your server.
        </h1>
        <p class="muted">
          Sign in with the token from <code>LEDGER_TOKEN</code>.
        </p>
        <Field label="Access token">
          <input
            type="password"
            autoComplete="current-password"
            value={token}
            onInput={(e) => setToken(e.currentTarget.value)}
            required
            autoFocus
          />
        </Field>
        <ErrorNote error={error === "Sign in to Ledger" ? "" : error} />
        <button class="btn primary" disabled={busy}>
          Open Ledger <ArrowRight size={16} />
        </button>
      </form>
    </main>
  );
}
