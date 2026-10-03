package server

import (
	"net/http"
	"strconv"

	"github.com/audemed44/ledger/internal/ledger"
)

// widget answers Foyer's app widget (format v1): this month's debits per
// currency and how much mail needs review.
func (s *Server) widget(w http.ResponseWriter, r *http.Request) {
	out, err := s.summary()
	if err != nil {
		failure(w, 500, "Could not load widget")
		return
	}
	stats := []map[string]string{}
	for _, t := range out.Totals {
		stats = append(stats, map[string]string{
			"label":   "Debits this month",
			"value":   ledger.Decimal(t.Debit),
			"unit":    t.Currency,
			"caption": "Excludes transfers between your accounts",
		})
	}
	if len(stats) == 0 {
		stats = append(stats, map[string]string{"label": "Debits this month", "value": "—", "caption": "No alerts yet"})
	}
	stats = append(stats, map[string]string{"label": "Needs review", "value": strconv.Itoa(out.Queued)})
	jsonResponse(w, map[string]any{
		"version":      1,
		"stats":        stats,
		"items_layout": "list",
		"items":        []any{},
		"progress":     []any{},
		// Foyer's Drop offers PDFs to Ledger, for statements banks only link to.
		"accepts": map[string]any{
			"url":   "/api/statements/upload",
			"types": []string{".pdf", "application/pdf"},
			"label": "Send to Ledger",
		},
	})
}
