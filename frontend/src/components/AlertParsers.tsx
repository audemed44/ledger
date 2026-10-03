import { useEffect, useState } from "preact/hooks";
import { ArrowUpRight, BookOpen, Download, EyeOff } from "lucide-preact";
import { api } from "../api";
import type { Parser } from "../types";
import { Empty, ErrorNote, Field, Section } from "./ui";

// issuerOf is a parser's issuer, or for an ignore rule without one, the
// issuer of another parser for the same sender.
export function issuerOf(p: Parser, all: Parser[]) {
  if (p.issuer?.trim()) return p.issuer.trim();
  const sibling = all.find(
    (o) => o.issuer?.trim() && o.sender.toLowerCase() === p.sender.toLowerCase(),
  );
  return sibling?.issuer?.trim() || "Other";
}

// AlertParsers lists the alert parsers, then the ignore rules, with a
// search and an issuer filter over both.
export function AlertParsers({ version, edit }: { version: number; edit: (p: Parser) => void }) {
  const [rows, setRows] = useState<Parser[] | null>(null),
    [error, setError] = useState(""),
    [search, setSearch] = useState(""),
    [issuer, setIssuer] = useState("");
  useEffect(() => {
    api<Parser[]>("parsers")
      .then(setRows)
      .catch((e) => setError(e.message));
  }, [version]);
  const all = rows || [];
  const issuers = [...new Set(all.map((p) => issuerOf(p, all)))].sort((a, b) =>
    a === "Other" ? 1 : b === "Other" ? -1 : a.localeCompare(b),
  );
  const term = search.trim().toLowerCase();
  const shown = all
    .filter((p) => !issuer || issuerOf(p, all) === issuer)
    .filter(
      (p) => !term || [p.name, p.sender, p.subject].some((v) => v.toLowerCase().includes(term)),
    )
    .sort((a, b) => a.name.localeCompare(b.name));
  const parsers = shown.filter((p) => p.direction !== "ignore"),
    ignores = shown.filter((p) => p.direction === "ignore");
  return (
    <>
      <section>
        <Section index="01" title="Alert parsers">
          <a class="text-link" href="/api/parsers.yaml">
            Export YAML <Download size={14} />
          </a>
        </Section>
        <ErrorNote error={error} />
        {rows !== null && rows.length > 0 && (
          <div class="parser-filters">
            <Field label="Search parsers">
              <input
                type="search"
                placeholder="Name, sender or subject"
                value={search}
                onInput={(e) => setSearch(e.currentTarget.value)}
              />
            </Field>
            <Field label="Issuer">
              <select value={issuer} onChange={(e) => setIssuer(e.currentTarget.value)}>
                <option value="">All issuers</option>
                {issuers.map((i) => (
                  <option key={i} value={i}>
                    {i}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        )}
        {rows === null ? (
          <p class="loading">Loading parsers…</p>
        ) : parsers.length ? (
          <div class="parser-grid">
            {parsers.map((p) => (
              <button class="parser-card" key={p.id} onClick={() => edit(p)}>
                <div class="eyebrow">
                  <span class="accent">{issuerOf(p, all)}</span>
                  <span class="spacer" />
                  <span class={"dot " + (!p.enabled ? "off" : "")} />
                  {p.enabled ? "ACTIVE" : "DISABLED"}
                </div>
                <h3>{p.name}</h3>
                <p class="hint">{p.sender}</p>
                <div class="parser-bottom">
                  <span class="chip">
                    {p.direction} · {p.currency}
                    {p.wordings?.length ? ` · ${p.wordings.length + 1} wordings` : ""}
                  </span>
                  <ArrowUpRight size={20} />
                </div>
              </button>
            ))}
          </div>
        ) : rows.length ? (
          <p class="hint">No alert parsers match.</p>
        ) : (
          <Empty title="Give your inbox a little context.">
            Create an alert parser using a sample from your queue. Preview the result before saving.
          </Empty>
        )}
        <div class="notice">
          <BookOpen size={18} />
          <span>
            Alert parsers record provisional transactions. Importing a statement confirms the ones
            it contains and flags the ones it doesn’t.
          </span>
        </div>
      </section>
      <section class="statement-section">
        <Section index="02" title="Ignore rules" />
        <p class="hint">
          Emails these match (OTPs, notices, promotions) leave the inbox and stay archived. Make one
          with “Ignore emails like this” on an email in the inbox.
        </p>
        {ignores.length ? (
          ignores.map((p) => (
            <button class="trigger ignore-rule" key={p.id} onClick={() => edit(p)}>
              <span>
                <EyeOff size={13} /> {p.name}
                <span class="hint">
                  {p.sender}
                  {p.pattern ? " · when the body matches" : ""}
                  {p.enabled ? "" : " · disabled"}
                </span>
              </span>
              <ArrowUpRight size={16} />
            </button>
          ))
        ) : (
          <p class="hint">
            {rows?.some((p) => p.direction === "ignore") ? "No ignore rules match." : "None yet."}
          </p>
        )}
      </section>
    </>
  );
}
