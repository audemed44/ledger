package server

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
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
}

func filter(r *http.Request) store.Filter {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	return store.Filter{
		Search:  q.Get("search"),
		Account: q.Get("account"),
		Status:  q.Get("status"),
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
	Month    string           `json:"month"`
	Demo     bool             `json:"demo"`
}

// summary's month follows TZ.
func (s *Server) summary() (Summary, error) {
	out := Summary{Month: time.Now().Format("2006-01"), Demo: s.Demo}
	var err error
	if out.Counts, err = s.Store.Counts(); err != nil {
		return out, err
	}
	if out.Totals, err = s.Store.MonthTotals(out.Month); err != nil {
		return out, err
	}
	out.Accounts, err = s.Store.Accounts()
	return out, err
}

// csv streams every matching transaction, page by page.
func (s *Server) csv(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ledger-transactions.csv"`)
	writer := csv.NewWriter(w)
	defer writer.Flush()
	writer.Write([]string{"Date", "Issuer", "Account", "Account type", "Merchant", "Amount", "Currency", "Direction", "Status", "Reference"})
	f := filter(r)
	f.Offset = 0
	for {
		rows, err := s.Store.Transactions(f)
		if err != nil {
			return
		}
		for _, t := range rows {
			record := []string{t.Date, t.Issuer, t.Account, t.AccountKind, t.Merchant, ledger.Decimal(t.Amount),
				t.Currency, t.Direction, t.Status, t.Reference}
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
