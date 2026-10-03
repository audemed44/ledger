# Ledger

Instructions for coding agents working in this repository. `CLAUDE.md`
imports this file.

## Project

A private finance ledger filled from bank and card emails in one Gmail
label. A Go server (`cmd/ledger`, `internal/`) serves a JSON API and the
Preact + TypeScript frontend (`frontend/`), built into `web/dist` and
embedded in the binary. State is SQLite at `/data/ledger.db` plus every
email archived intact under `/data/archive`.

- `internal/ledger`: shared types: transactions, account identities,
  money in integer minor units.
- `internal/mail`: MIME decoding: text (HTML made inert), attachments, PDFs.
- `internal/gmail`: the IMAP poller: one label, read-only, UID cursor.
- `internal/alerts`: alert parsers (sender + subject + named-group regex),
  and writing that regex from fields tagged in an example email.
- `internal/pattern`: loose literal patterns from example text (numbers
  and month names vary), for parser subjects and statement triggers.
- `internal/statements`: PDF extraction (qpdf, pdftotext) and the
  issuer-specific statement layouts.
- `internal/store`: the database and archive: ingest, processing, parsers,
  statement import, migrations.
- `internal/server`: HTTP API, auth, the Foyer widget.
- `internal/fixture`: synthetic emails, statements and PDFs for tests.

## Constraints

- **Financial data stays on the server.** Never read real mail, statements
  or the data folder, not even to build a parser. Tests and examples use
  handwritten synthetic fixtures only. No LLM or cloud parsing.
- Secrets (token, Gmail app password, PDF passwords) come from the
  environment. Never store them in SQLite, log them, return them from the
  API or send them to the browser.
- Money is integer minor units. Never accept a parse built from partial or
  invalid fields; when in doubt, leave the message in the inbox with a
  reason.
- Nothing is lost: every email is archived before the database changes,
  unmatched mail stays queued, and ingesting, processing and importing
  again must change nothing.
- Every `/api/` call needs the token (bearer or the derived session
  cookie), and state-changing requests from another origin are refused.
- **Low memory is a feature.** It idles around 11 MB. Direct dependencies:
  modernc.org/sqlite (pure Go, cgo-free), go-imap and go-message (IMAP and
  MIME), x/net/html (HTML-only mail), yaml.v3 (parser export). Justify any
  new one.
- UI style is Foyer's: Swiss editorial, always dark, heavy Inter headlines,
  tracked uppercase eyebrows, 2px rules over numbered headings, square
  corners, one accent (#2563ff). Check phone width too.

## Commits

Conventional Commits: `<type>(<scope>): <summary>`, e.g. `feat(alerts): ...`.

## Checks before pushing

```sh
go vet ./... && go test -race ./...        # needs web/dist, qpdf and pdftotext
cd frontend && npm run format:check && npm run typecheck && npm test && npm run build
docker build -t ledger:dev .
```
