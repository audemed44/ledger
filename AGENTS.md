# Ledger

Self-hosted finance ledger. Go + SQLite (modernc, no cgo), embedded Preact/TypeScript UI.

- Financial mail stays on the server. Use synthetic fixtures only; do not inspect real mail or data directories.
- Secrets come from environment variables, never SQLite, logs, frontend bundles, or git.
- Money uses integer minor units. Never infer a successful parse from partial/invalid fields.
- Preserve unmatched messages and raw MIME archives. Ingestion and retries must be idempotent.
- All APIs require authentication; reject cross-origin browser writes. No external fonts or analytics.
- Match Foyer/Hoist: black, heavy Inter, numbered headings with 2px rules, square corners, #2563ff. Check mobile.
- Keep dependencies and idle memory small. IMAP and MIME libraries handle protocol complexity; SQLite persists data; YAML exports parser definitions; x/net/html safely extracts HTML-only email text without executing markup or fetching resources.
- Run Go vet and race tests; frontend format check, typecheck, tests, build; container build before pushing.
- Feature branches, conventional commits, PRs, rebase merge only.
