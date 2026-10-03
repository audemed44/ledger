package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/audemed44/ledger/internal/mail"
	"github.com/audemed44/ledger/internal/smartstatement"
)

// SmartStatementSender is the sender of statements Ledger downloaded from an
// HDFC SmartStatement link, for automatic imports to match.
const SmartStatementSender = "hdfc-smartstatement@ledger.invalid"

// LinkUnavailable starts the reason of an email whose statement link can't
// be fetched any more; it isn't tried again.
const LinkUnavailable = "Statement link: "

// fetchLinked downloads the statement a queued email links to instead of
// attaching (HDFC Bank's SmartStatement), and files it as its own statement
// email, so automatic import and reconciliation apply. The link email then
// leaves the inbox. If the download fails, it stays with the reason, and
// the statement can be downloaded and uploaded by hand. An expired link is
// remembered and not tried again.
func (s *Store) fetchLinked(ctx context.Context, id int64) error {
	if s.Statements == nil {
		return nil
	}
	m, err := s.Message(id)
	if err != nil || m.State != "queued" || m.HasPDF {
		return err
	}
	var outcome string
	err = s.DB.QueryRow("SELECT outcome FROM statement_links WHERE message_id=?", id).Scan(&outcome)
	if err == nil && outcome != "" {
		_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=? AND state='queued'", LinkUnavailable+outcome, id)
		return err
	}
	raw, err := s.ArchivedRaw(id)
	if err != nil {
		return nil
	}
	link := ""
	for _, l := range mail.Decode(raw).Links {
		if u, e := url.Parse(l); e == nil && smartstatement.IsLink(u, s.Statements.Host) {
			link = l
			break
		}
	}
	if link == "" {
		return nil
	}

	pdf, name, err := s.fetchWithPasswords(ctx, link)
	if err != nil {
		reason := LinkUnavailable + err.Error() + ". Download the statement and choose Upload PDF."
		if errors.Is(err, smartstatement.ErrExpired) {
			// Remembered, so it isn't fetched again on every retry.
			if _, e := s.DB.Exec("INSERT OR REPLACE INTO statement_links(message_id,outcome) VALUES(?,?)",
				id, err.Error()+". Download the statement from NetBanking and choose Upload PDF."); e != nil {
				return e
			}
		}
		_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=? AND state='queued'", reason, id)
		return err
	}
	filed, err := s.file(source{
		sender:  SmartStatementSender,
		display: "HDFC SmartStatement",
		subject: "HDFC SmartStatement: ",
		note:    fmt.Sprintf("Downloaded by Ledger from the statement link in “%s” from %s.", m.Subject, m.Sender),
	}, name, pdf)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("UPDATE messages SET state='fetched',reason=? WHERE id=? AND state='queued'",
		fmt.Sprintf("Statement downloaded as email #%d", filed.ID), id)
	return err
}

// fetchWithPasswords downloads a linked statement with the password slot of
// the HDFC Bank account statement parser, or else each configured password
// in turn, stopping at the first the bank accepts.
func (s *Store) fetchWithPasswords(ctx context.Context, link string) ([]byte, string, error) {
	parsers, err := s.StatementParsers()
	if err != nil {
		return nil, "", err
	}
	slots := []int{}
	for _, p := range parsers {
		if p.Adapter == "hdfc-savings" && p.PasswordSlot > 0 && p.PasswordSlot <= len(s.PDFPasswords) {
			slots = append(slots, p.PasswordSlot)
			break
		}
	}
	if len(slots) == 0 {
		for i, p := range s.PDFPasswords {
			if p != "" {
				slots = append(slots, i+1)
			}
		}
	}
	if len(slots) == 0 {
		return nil, "", errors.New("no PDF passwords are configured (LEDGER_PDF_PASSWORDS)")
	}
	err = smartstatement.ErrPassword
	for _, slot := range slots {
		var pdf []byte
		var name string
		pdf, name, err = s.Statements.Fetch(ctx, link, s.PDFPasswords[slot-1])
		if !errors.Is(err, smartstatement.ErrPassword) {
			return pdf, name, err
		}
	}
	return nil, "", fmt.Errorf("%w (password slots %s)", err, strings.Trim(strings.Join(strings.Fields(fmt.Sprint(slots)), ", "), "[]"))
}
