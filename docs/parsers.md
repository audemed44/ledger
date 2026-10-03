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
| `merchant` | required, unless the parser has a description | |
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
  transactions, say) out of the ledger and out of the inbox. An ignore
  parser needs no body pattern: without one, it ignores every email from
  the sender whose subject matches.
- **Description when the email names no merchant**: some alerts, such as
  a card payment received, name no merchant. Leave Merchant untagged and
  give a description ("Payment received"); it stands in until a statement
  line confirms the transaction, and then the statement's description
  replaces it. Reconciliation never used the merchant: it matches on
  account, amount, currency, direction and date.
- **Several wordings**: banks word one alert in several ways (HDFC's UPI
  alerts, say). When you build a parser from an email and a parser for its
  sender exists, **Save as → Another wording of** adds this wording to that
  parser instead. Each wording has its own body pattern and date layout,
  and may record onto a different account type (UPI from a bank account or
  a RuPay card). An email must match exactly one wording. Editing a parser
  lists its other wordings, and **Merge another parser into this one**
  moves a parser's wordings over (same sender, issuer, direction, currency
  and description) and deletes it; transactions don't change.
- **Debit card** as the account type records a debit card's alerts on the
  bank account it draws on; see [Debit cards](#debit-cards).
- An email must match **exactly one** parser, once. If two parsers match,
  or a body matches twice, it stays in the inbox.
- Saving a parser retries the whole backlog.
- **Fixing a mistake**: when you edit or delete a parser that has already
  handled emails (read a credit as a debit, say), **Re-read the emails this
  parser already handled** (ticked by default) undoes what they recorded
  and reads them again with the parsers as they are now. After a delete,
  emails no parser matches go back to the inbox. An alert transaction a
  statement has confirmed stays as the statement has it. Untick it to leave
  past transactions as they are.
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

### Uploading a statement

Some banks email a link to a download page rather than the PDF. Download
the statement, then choose **Upload PDF** in the inbox, or share it to
Foyer's Drop and press **Send to Ledger**. The PDF is filed like an email
from `uploads@ledger.invalid`, with the subject "Uploaded statement:" and
its file name, and archived. Review and import it as usual; with
**Import future statements like this automatically** ticked, later
uploads with a similar file name import themselves. Uploading the same
file again changes nothing. PDFs can be up to 18 MiB.

The link emails themselves will wait in the inbox; **Ignore emails like
this** on one keeps them out (see below).

## Ignoring emails

OTPs, notices and promotions don't belong in the ledger. Open one in the
inbox and choose **Ignore emails like this**: Ledger saves an ignore
parser for that exact sender and a subject like this one (numbers and month
names may differ, so every OTP matches), then retries the backlog so the
rest leave the inbox too. Ignored mail stays archived. The rule is listed
with the alert parsers, where you can edit or delete it.

## Debit cards

A debit card alert names the card, but the money leaves a bank account, and
that's the account the bank statement covers. Give the card's alert parser
the **Debit card** account type, then link the card under **Parsers → Debit
cards**: the issuer, the card's last four digits and the account's. Its
transactions are then recorded on the bank account (with "Debit card
••1234" as the reference), and reconcile with that account's statements.

An alert from a card that isn't linked waits in the inbox, saying so;
linking the card retries the backlog. Changing or removing a link doesn't
move transactions already recorded.

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
  same account and date is refused.
- Deleting a statement parser keeps its statements, transactions and
  emails.

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
| **ICICI Credit Card Parser v1** (`icici-credit-card`) | ICICI, card | Summary from the page layout; lines from the PDF's raw text, because the layout text loses their amounts |
| **IDFC FIRST Credit Card Parser v1** (`idfc-credit-card`) | IDFC, card | Opening balance, purchases, EMI and other debits, payments and refunds; balances in credit are supported. ₹ extracts as `r` |
| **SBI Savings Account Parser v1** (`sbi-savings`) | SBI, bank | Opening and closing balance and every line's running balance, for the one account in the transaction overview |
| **HDFC Bank Account Parser v1** (`hdfc-savings`) | HDFC, bank | Summary counts and totals, and every line's running balance |

Descriptions that wrap onto the lines around their row are joined back
together. For alerts and statements to reconcile, give alert parsers the
issuer in the table (case doesn't matter) and the same account type.

A new layout is code: an adapter in `internal/statements`, built and
tested against a synthetic copy of the layout. Run `ledger check-statement`
on your own PDF to see whether a layout reads it.
