package server

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/reminders"
	"github.com/audemed44/ledger/internal/store"
)

func (s *Server) transactionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/summary", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.summary()
		if err != nil {
			failure(w, 500, "Could not load summary")
			return
		}
		jsonResponse(w, out)
	})
	mux.HandleFunc("GET /api/transactions", func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.Transactions(filter(r))
		if err != nil {
			failure(w, 500, "Could not load transactions")
			return
		}
		jsonResponse(w, rows)
	})
	mux.HandleFunc("GET /api/transactions.csv", s.csv)
	// Transfers: mark or unmark one by hand, or treat everything described
	// like it as a transfer.
	mux.HandleFunc("POST /api/transactions/{id}/transfer", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Transfer bool `json:"transfer"`
		}
		if !decode(w, r, &body) {
			return
		}
		err := s.Store.SetTransfer(pathID(r), body.Transfer)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Transaction not found")
			return
		}
		if err != nil {
			failure(w, 500, "Could not update transaction")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/transactions/{id}/transfer-rule", func(w http.ResponseWriter, r *http.Request) {
		rule, err := s.Store.TransferRuleLike(pathID(r))
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Transaction not found")
			return
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		jsonResponse(w, rule)
	})
	mux.HandleFunc("GET /api/transfer-rules", func(w http.ResponseWriter, r *http.Request) {
		rules, err := s.Store.TransferRules()
		if err != nil {
			failure(w, 500, "Could not load transfer rules")
			return
		}
		jsonResponse(w, rules)
	})
	mux.HandleFunc("DELETE /api/transfer-rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := s.Store.DeleteTransferRule(pathID(r))
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Rule not found")
			return
		}
		if err != nil {
			failure(w, 500, "Could not delete rule")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	// A flagged alert transaction can be dismissed, or restored.
	mux.HandleFunc("POST /api/transactions/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Dismissed bool `json:"dismissed"`
		}
		if !decode(w, r, &body) {
			return
		}
		err := s.Store.Dismiss(pathID(r), body.Dismissed)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 409, "Only flagged transactions can be dismissed, and only dismissed ones restored")
			return
		}
		if err != nil {
			failure(w, 500, "Could not update transaction")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
}

func filter(r *http.Request) store.Filter {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	return store.Filter{
		Search:  q.Get("search"),
		Account: q.Get("account"),
		Status:  q.Get("status"),
		Kind:    q.Get("kind"),
		From:    q.Get("from"),
		To:      q.Get("to"),
		Offset:  offset,
	}
}

// Summary is the dashboard: this month's totals per currency, counts and
// the accounts to filter by.
type Summary struct {
	store.Counts
	Totals   []store.Total    `json:"totals"`
	Accounts []ledger.Account `json:"accounts"`
	Dues     []store.Due      `json:"dues"`
	// Reminders is whether payment reminders are sent (LEDGER_NOTIFY_URL is
	// set), and their settings.
	Reminders struct {
		Enabled bool `json:"enabled"`
		reminders.Settings
	} `json:"reminders"`
	Month string `json:"month"`
	Demo  bool   `json:"demo"`
	// FoyerURL is the homelab's start page, linked from the header.
	FoyerURL string `json:"foyer_url,omitempty"`
}

// summary's month follows TZ.
func (s *Server) summary() (Summary, error) {
	out := Summary{Month: time.Now().Format("2006-01"), Demo: s.Demo, FoyerURL: s.FoyerURL}
	var err error
	if out.Counts, err = s.Store.Counts(); err != nil {
		return out, err
	}
	if out.Totals, err = s.Store.MonthTotals(out.Month); err != nil {
		return out, err
	}
	if out.Accounts, err = s.Store.Accounts(); err != nil {
		return out, err
	}
	out.Reminders.Enabled = s.Reminders.Enabled()
	if out.Reminders.Settings, err = reminders.LoadSettings(s.Store); err != nil {
		return out, err
	}
	out.Dues, err = s.Store.Dues(time.Now())
	return out, err
}

// csv streams every matching transaction, page by page.
func (s *Server) csv(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ledger-transactions.csv"`)
	writer := csv.NewWriter(w)
	defer writer.Flush()
	writer.Write([]string{"Date", "Issuer", "Account", "Account type", "Merchant", "Amount", "Currency", "Direction", "Status", "Reference", "Transfer"})
	f := filter(r)
	f.Offset = 0
	for {
		rows, err := s.Store.Transactions(f)
		if err != nil {
			return
		}
		for _, t := range rows {
			record := []string{t.Date, t.Issuer, t.Account, t.AccountKind, t.Merchant, ledger.Decimal(t.Amount),
				t.Currency, t.Direction, t.Status, t.Reference, t.Transfer}
			for i := range record {
				record[i] = csvSafe(record[i])
			}
			if writer.Write(record) != nil {
				return
			}
		}
		if len(rows) < store.PageSize {
			return
		}
		f.Offset += len(rows)
	}
}

// csvSafe stops spreadsheets running merchant names as formulas.
func csvSafe(s string) string {
	trimmed := strings.TrimSpace(s)
	for _, prefix := range []string{"=", "+", "-", "@"} {
		if strings.HasPrefix(trimmed, prefix) {
			return "'" + s
		}
	}
	if strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
