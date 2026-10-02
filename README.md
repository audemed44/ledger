# Ledger

A private, self-hosted finance ledger. Gmail alerts become provisional transactions; mail that needs a parser stays visible. Go, SQLite and a Preact UI styled like Foyer and Hoist: black, heavy Inter, blue accent, square frames.

![Ledger with synthetic demo data](docs/ledger-desktop.png)

## Initial release

- Gmail IMAP polling of the **Bank** label, read-only and without marking messages read.
- Exact-sender alert rules, Go named-group regexes, live server-side previews, editable parsers and YAML export.
- Durable raw MIME archives (attachments included), Message-ID deduplication, persistent UID cursors and backlog retries.
- Filtered transactions, separate currency totals, CSV export and authenticated Foyer widget.
- Token sign-in, local fonts, mobile layout and web-app manifest. Financial data is not cached offline.
- Named **PDF statement parsers**, starting with HDFC Credit Card (Tata Neu layout), with in-app text/transaction previews, password selection, row-total validation and configurable final-balance rounding. Previews do not import statements.

Statement ingestion/reconciliation, categories, spending charts, due-date feeds, reminders and recurring charges are subsequent work. All imported alerts are provisional. Dashboard totals are **debits**, not reconciled spending: they still include card payments and transfers. Unsupported or ambiguous mail stays queued. Email extraction supports plain text and HTML-only MIME bodies. HTML is converted to inert text on the server; scripts, styles, hidden text and external images are not rendered or fetched. Plain-text alternatives take precedence. Existing queued messages are recovered from their archives on upgrade. Supported currencies use two decimal places.

## Run

Copy `.env.example` to `.env`, generate an access token with `openssl rand -hex 32`, and set `LEDGER_TOKEN`. Never commit `.env`. Compose loads it automatically; the native binary reads environment variables and does not load dotenv files itself.

```sh
cp .env.example .env
chmod 600 .env
mkdir -p data
# The container runs as UID 10001; the bind mount must be writable by it.
sudo chown 10001:10001 data
chmod 700 data
docker compose up -d
```

The image is `ghcr.io/audemed44/ledger:latest`, published after merge to main. Before that, build locally with `docker build -t ghcr.io/audemed44/ledger:latest .`. Put the service behind your private HTTPS reverse proxy; default port is 8087. `LEDGER_SECURE_COOKIES=true` is for HTTPS; set it to `false` only for local HTTP development. No public deployment is required.

## Gmail setup

1. Gmail → Settings → Filters and Blocked Addresses → Create a new filter.
2. Add bank alert/statement senders in **From**, joined with `OR`.
3. Apply your existing **Bank** label. Tick **Also apply filter to matching conversations** to label historical mail. Ensure Bank is visible in IMAP.
4. Enable Google two-step verification and create an app password. Set `GMAIL_USER` and `GMAIL_APP_PASSWORD` in the stack's `.env`. Enter the app password without display spaces.
5. Choose `LEDGER_BACKFILL=true` **before the first successful sync** to import labelled mail dated **1 January 2026 onward**, inclusive. Default `false` records the current UID baseline and imports only subsequently labelled messages. This setting applies only when establishing a new mailbox cursor; changing it later does not rewind that cursor.
6. Restart Ledger. Open **Inbox**, review an alert and define its fields. Saving retries all queued mail. You can also create/test rules using the synthetic example before mail arrives.

An app password grants access to the entire mailbox. The application selects only `GMAIL_LABEL` (default `Bank`); this is an application boundary, not a Gmail credential restriction. It does not move, delete or mark mail read. Network and authentication failures are shown under Connection, without logging credentials or message contents.

Polling defaults to 15 minutes, fetching up to 500 messages per pass for bounded backfill. The 2026 cutoff also applies to resumed backfills and UIDVALIDITY rescans. On upgrade, older queued messages are excluded from review automatically; their archives and any already imported transactions are preserved. A message over 25 MiB pauses sync visibly rather than silently skipping it. UIDVALIDITY changes trigger rescan/deduplication. The application archives before advancing its cursor, so interrupted work is retried safely. Malformed MIME and PDF statements remain queued. Queue UI shows the latest 200; retry processes the whole backlog. Keep the database and archive together in backups.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `LEDGER_TOKEN` | required, ≥24 characters | UI/API access token |
| `GMAIL_USER` | empty | Gmail address |
| `GMAIL_APP_PASSWORD` | empty | App password; blank disables mail polling |
| `LEDGER_PDF_PASSWORDS` | empty | Pipe-separated statement passwords; server-side only |
| `GMAIL_LABEL` | `Bank` | Only mailbox selected |
| `LEDGER_POLL_INTERVAL` | `15m` | Poll interval, at least 1m |
| `LEDGER_BACKFILL` | `false` | Import label mail from 1 January 2026 when creating cursor |
| `LEDGER_DATA_DIR` | `/data` | SQLite and archive directory |
| `LEDGER_LISTEN` | `:8080` | Native HTTP listener |
| `LEDGER_SECURE_COOKIES` | `true` | Require HTTPS session cookies |
| `TZ` | container stack: `Asia/Kolkata` | Dashboard month boundary |

Use a strong access token. Rotate it to invalidate every session. Foyer should use the token as a bearer key. Gmail and PDF passwords stay in environment variables, never SQLite. Other environment variables are not returned by APIs.

## Alert parsers versus statement parsers

Alert rules operate on plain email text. Each rule matches one exact sender, a subject regex and a body regex, then extracts one transaction. Required named groups: `amount`, `merchant`, `account` (last four digits), `date`. Optional: `currency`, `direction` (`debit`/`credit`), `reference`. The defaults supply currency/direction when absent. Dates use Go layouts (e.g. `02-Jan-2006`), interpreted in the rule's IANA timezone. An `ignore` rule can retain declined alerts without creating transactions. Keep the issuer/bank field consistent across purchase, refund and transfer rules; parser names can describe individual formats. Select Credit card or Bank account so identical suffixes cannot mix card and bank activity. Account identity includes issuer, type and the extracted last four digits; legacy rules remain explicitly untyped until configured. Multiple matches or overlapping rules are queued for review.

Statements need a separate, issuer-specific layout adapter: page headers, columns, credit markers, summaries and balance checks. A generic regex editor is not a substitute for these checks. The initial HDFC Tata Neu adapter handles this known layout, including its currency glyph extracting as `C`; it rejects unmatched dated rows and requires line-item debit/credit totals to match the statement summary. Unknown layouts remain unsupported, and a final balance discrepancy above the configured tolerance is flagged. Finance charges require further reconciliation.

In **Parsers → PDF statement parsers**, create a named parser such as **HDFC Credit Card**, choose the supported layout and either **Try all configured passwords** or **Password 1**, **Password 2**, etc. Set its final balance tolerance from 0 to 99 paise (default 99). Then open a PDF email in **Inbox → Review**, select that parser, and choose **Extract & validate PDF**. The preview shows the account/card, statement dates, totals, transactions, extracted PDF text and any accepted rounding. Supplementary/ambiguous card identifiers are rejected rather than assigned to the primary card. Statement import/reconciliation and authoritative per-account balances are not implemented yet.

Configure `LEDGER_PDF_PASSWORDS` in the stack environment as `Password1|Password2|Password3`. Hoist can save the value directly; quote it if entering it through a shell. The server tries unencrypted access first, then the selected slot or all configured passwords, up to 32 slots and a one-minute extraction limit. Password values never enter the browser or SQLite; only slot numbers are persisted. Reordering the list changes which password a slot refers to. Passwords containing `|` are not supported by this format. The existing `LEDGER_PDF_PASSWORD` remains available only for the local diagnostic command.

To check a local PDF, supply its password privately via `LEDGER_PDF_PASSWORD` and run:

```sh
ledger check-statement --pdf /path/to/statement.pdf
```

Requires `qpdf` and `pdftotext` (included in the image). Password goes to qpdf over stdin, never command-line arguments. Decrypted temporary files are removed after extraction. Output contains counts and validation results, not merchants, account identifiers or statement amounts. The default final-balance tolerance is 99 paise (₹0.99); individual row totals still must match exactly. Exit 0 means validated, 2 means review needed; other errors mean unsupported/unreadable input. Nothing is imported. Real PDFs/passwords are excluded from fixtures and source control; tests use handwritten synthetic examples.

## Foyer

Use an **app** widget with URL `http://ledger:8080/api/foyer/widget` and `key` set to the Ledger token via your Foyer secret environment. It implements Foyer widget format v1 with monthly debits per currency and queue count. A next-card-due stat will follow statement ingestion. The endpoint requires bearer authentication.

## Development

Use native Go and Node. No Docker is needed for development tools.

```sh
cd frontend
npm ci
npm run build
cd ..
go build -o ledger ./cmd/ledger
```

For synthetic demo data, set `LEDGER_TOKEN`, `LEDGER_DATA_DIR` to a scratch directory, `LEDGER_SECURE_COOKIES=false`, and run `./ledger --demo`. Demo mode disables Gmail regardless of environment credentials. Never use the same directory for demo and real mail. `npm run dev` proxies API calls to localhost:8080.

Checks:

```sh
cd frontend
npm run format:check && npm run typecheck && npm test && npm run build
cd ..
go vet ./... && go test -race ./...
docker build -t ledger:dev .
```

Dependencies: modernc SQLite keeps CGO disabled; go-imap/go-message handle IMAP and MIME/charset complexity; yaml.v3 exports editable rules. golang.org/x/net/html extracts HTML-only mail without executing or displaying the markup. No polling services beyond Gmail, no cloud parsers, no analytics. Idle memory has not yet been benchmarked on a deployed homelab instance; the 15–20 MB requirement is a target, not a measured claim.
