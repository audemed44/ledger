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

Alert transactions are **provisional** until a statement confirms them
(see [Reconciliation](#reconciliation)).

## Statement parsers

A statement parser is a named preset: a layout, which password to try and
how much rounding to accept. Create one under **Parsers → PDF statement
parsers**, then open a statement email in the inbox, pick the parser and
choose **Extract & validate PDF**. The preview shows the account, statement
and due dates, totals, every row and the extracted text.

**Import transactions** appears only when the statement validated. The
import re-reads the PDF on the server and saves every row at once, as
confirmed transactions, with the statement's summary.

### Automatic import

Leave **Import future statements like this automatically** ticked when you
import a statement by hand, and the parser saves a trigger: the email's
exact sender, plus its subject and the PDF's file name with numbers and
month names loosened. From then on, a statement email that fits a trigger
is extracted, validated and imported as soon as it arrives (and saving
triggers is picked up by **Retry backlog**). One that doesn't validate, or
that no trigger or more than one trigger fits, stays in the inbox with the
reason. Remove triggers by editing the parser.

## Reconciliation

Each statement line is matched to an alert transaction on the same account
(issuer, card or bank, last four digits) with the same amount, currency and
direction, dated up to 3 days apart; an equal reference wins a tie. A match
confirms the alert transaction, which keeps its own merchant name and date.
Lines with no alert (fees, cashback, missed alerts) are added, confirmed.

Alerts on that account that the statement doesn't contain are **flagged**,
if they're from its period: since 3 days before the account's previous
statement, or 28 days back for the first one, up to 3 days before the
statement date. Alerts from those last 3 days may still be posting, so
they stay provisional for the next statement. A flagged alert that a
later statement contains is confirmed then.

A flagged alert is usually a pre-authorisation that settled for a
different amount, or a charge that was reversed. **Dismiss** it to leave it
out of the totals; **Restore** brings it back.

An alert that arrives after its statement was imported confirms the
statement line instead of adding another transaction.

Not handled yet: refunds linked to their charge, foreign currency markup
lines, EMI conversions, and card payments paired with the bank debit as a
transfer.

## Supported layouts

Each layout checks the statement's lines against its own totals, and
refuses a statement with a second card or account in it.

| Layout | Issuer | Checks |
| --- | --- | --- |
| **HDFC Credit Card Parser v1** (`hdfc-credit-card`) | HDFC, card | Summary and dated table. Works on the Tata Neu and Regalia layouts; the ₹ glyph extracts as `C` |
| **Axis Credit Card Parser v1** (`axis-credit-card`) | Axis, card | Previous balance, payments, credits, purchases, cash and other charges against the Dr/Cr lines |

Descriptions that wrap onto the lines around their row are joined back
together. For alerts and statements to reconcile, give alert parsers the
issuer in the table (case doesn't matter) and the same account type.

A new layout is code: an adapter in `internal/statements`, built and
tested against a synthetic copy of the layout. Run `ledger check-statement`
on your own PDF to see whether a layout reads it.
