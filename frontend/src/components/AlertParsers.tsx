import { useEffect, useState } from "preact/hooks";
import { ArrowUpRight, BookOpen, Download } from "lucide-preact";
import { api } from "../api";
import type { Parser } from "../types";
import { Empty, ErrorNote, Section } from "./ui";

export function AlertParsers({ version, edit }: { version: number; edit: (p: Parser) => void }) {
  const [rows, setRows] = useState<Parser[] | null>(null),
    [error, setError] = useState("");
  useEffect(() => {
    api<Parser[]>("parsers")
      .then(setRows)
      .catch((e) => setError(e.message));
  }, [version]);
  return (
    <section>
      <Section index="01" title="Alert parsers">
        <a class="text-link" href="/api/parsers.yaml">
          Export YAML <Download size={14} />
        </a>
      </Section>
      <ErrorNote error={error} />
      {rows === null ? (
        <p class="loading">Loading parsers…</p>
      ) : rows.length ? (
        <div class="parser-grid">
          {rows.map((p, i) => (
            <button class="parser-card" onClick={() => edit(p)}>
              <div class="eyebrow">
                <span class="accent">{String(i + 1).padStart(2, "0")}</span>
                <span class="spacer" />
                <span class={"dot " + (!p.enabled ? "off" : "")} />
                {p.enabled ? "ACTIVE" : "DISABLED"}
              </div>
              <h3>{p.name}</h3>
              <p class="hint">{p.sender}</p>
              <div class="parser-bottom">
                <span class="chip">
                  {p.direction === "ignore"
                    ? "Ignore matching alerts"
                    : p.direction + " · " + p.currency}
                </span>
                <ArrowUpRight size={20} />
              </div>
            </button>
          ))}
        </div>
      ) : (
        <Empty title="Give your inbox a little context.">
          Create an alert parser using a sample from your queue. Preview the result before saving.
        </Empty>
      )}
      <div class="notice">
        <BookOpen size={18} />
        <span>
          Alert parsers record provisional transactions. Matching them to statement lines is coming
          next.
        </span>
      </div>
    </section>
  );
}
