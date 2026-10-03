# Parsers

Ledger has two kinds of parser. **Alert parsers** read one transaction out
of a per-transaction alert email. **Statement parsers** read a PDF
statement: every line, plus the summary they have to add up to. They're
separate because a statement's layout (page headers, columns, credit
markers, summary checks) is more than a regex can safely express.

## The inbox

Mail that no parser handled waits in **Inbox**, newest email first, with
the reason. Filter it to **PDF statements** or **Text/HTML alerts**; an
email with a PDF counts as a statement even if it has covering text. The
list shows the latest 200, but **Retry backlog** processes all of it.

Mail is read as plain text. For HTML-only emails, the server extracts the
visible text: scripts, styles, hidden elements and images are dropped, and
nothing is fetched or rendered. When an email has both, the plain text
wins.

## Alert parsers

Open an email in the inbox and choose **Create alert parser**. Select a
value in the email text, then choose what it is: **Amount**, **Merchant**,
**Card / account** (just the last four digits) and **Date** are required;
**Currency** (a code such as INR) and **Reference** are optional. Ledger
writes the body pattern, the date layout and a subject pattern from them,
and the preview shows the transaction it reads. You can also paste a
sample instead of opening an email.

The pattern keeps a few words of literal text around each value, so it
still matches the next email: numbers and month names in that text may
change, and a long stretch between two values is skipped. The pattern must
read back exactly what you tagged, or Ledger says what went wrong; usually
the fix is selecting the whole value, or tagging values that are closer
together. Numeric dates are read day first (01/10/2026 is 1 October)
unless the year comes first or the middle number can't be a month.

Under **Advanced** you can edit the pattern and layout by hand. An alert
parser matches one **exact sender** and a **subject** regex, then runs a
**body** regex (Go syntax) with named groups:

| Group | | |
| --- | --- | --- |
| `amount` | required | `1,234.56`; Indian and western grouping, at most two decimals |
| `merchant` | required | |
| `account` | required | Exactly the last four digits |
| `date` | required | Read with the parser's date layout and time zone |
| `currency` | optional | Otherwise the parser's default |
| `direction` | optional | `debit` or `credit`; otherwise the parser's default |
| `reference` | optional | |

Date layouts are Go's: `02-Jan-2006`, `2006-01-02`, `02/01/2006 15:04`.

- **Issuer and account type** decide which account a transaction belongs
  to: the issuer, card or bank account, and the last four digits. Use the
  same issuer on every parser for one bank, so purchases, refunds and
  transfers land on the same account. Parsers from before account types
  existed stay "unknown" until you set one.
- **Ignore** as the direction keeps matching emails (declined
  transactions, say) out of the ledger and out of the inbox.
- An email must match **exactly one** parser, once. If two parsers match,
  or a body matches twice, it stays in the inbox.
- Saving a parser retries the whole backlog. Editing or deleting a parser
  never changes transactions it already imported.
- **Export YAML** downloads every alert parser, for backups or version
  control.

Alert transactions are **provisional** until a statement confirms them.

## Statement parsers

A statement parser is a named preset: a layout, which password to try and
how much rounding to accept. Create one under **Parsers → PDF statement
parsers**, then open a statement email in the inbox, pick the parser and
choose **Extract & validate PDF**. The preview shows the account, statement
and due dates, totals, every row and the extracted text.

**Import transactions** appears only when the statement validated. The
import re-reads the PDF on the server and saves every row at once, as
confirmed transactions, with the statement's summary.

- **Passwords**: set `LEDGER_PDF_PASSWORDS=Password1|Password2|…` (up to
  32; a password can't contain `|`). A parser tries all of them, or one
  slot. Only the slot number is saved, so reordering the list changes what
  a slot means. Unencrypted PDFs are always tried first.
- **Validation**: every dated row in the transaction table must parse, and
  the debit and credit rows must equal the summary's purchases and
  payments exactly. The final balance may differ from the summary by up to
  the parser's tolerance (0–99 paise, default 99) for rounding. Statements
  with finance charges are flagged until reconciliation can handle them.
  A statement that fails keeps the rows it could read, for diagnosis, and
  can't be imported.
- **One card per statement**: supplementary or ambiguous card numbers are
  rejected rather than assigned to the primary card.
- **No duplicates**: the same statement imported again, from the same
  email or a resent one, changes nothing. A *different* statement for the
  same account and date is refused. So is a statement with a row that
  might duplicate an existing alert transaction, and an alert that arrives
  after its statement stays in the inbox: matching the two is
  reconciliation's job, which hasn't landed yet.
- Deleting a statement parser keeps its statements, transactions and
  emails.

### Supported layouts

- **HDFC Credit Card Parser v1**: the HDFC credit card summary and dated
  transaction table. It works on the Tata Neu and Regalia layouts; the ₹
  glyph extracts as `C`.

A new layout is code: an adapter in `internal/statements`, built and
tested against a synthetic copy of the layout. Run `ledger check-statement`
on your own PDF to see whether a layout reads it.
