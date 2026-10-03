package server

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/reminders"
	"github.com/audemed44/ledger/internal/store"
)

// widget answers Foyer's app widget (format v1): this month's debits per
// currency, how much mail needs review, and what's due on your cards.
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
			"value":   ledger.Money(t.Debit, t.Currency),
			"caption": "Excludes transfers between your accounts",
		})
	}
	if len(stats) == 0 {
		stats = append(stats, map[string]string{"label": "Debits this month", "value": "—", "caption": "No alerts yet"})
	}
	if stat := dueStat(out.Dues); stat != nil {
		stats = append(stats, stat)
	}
	stats = append(stats, map[string]string{"label": "Needs review", "value": strconv.Itoa(out.Queued)})
	items := []map[string]any{}
	for _, d := range out.Dues {
		if d.Status == "paid" {
			continue
		}
		due, _ := time.Parse("2006-01-02", d.DueDate)
		item := map[string]any{
			"title":    d.Issuer + " card ••" + d.LastFour,
			"subtitle": "Due " + due.Format("2 Jan") + " · " + reminders.When(d.Days),
			"caption":  reminders.Rupees(d.Remaining),
			"url":      "/#transactions",
			"action": map[string]string{
				"label":   "Mark paid",
				"url":     "/api/foyer/dues/" + url.PathEscape(d.AccountID) + "/" + d.DueDate + "/paid",
				"confirm": "Mark " + d.Issuer + " ••" + d.LastFour + " paid?",
			},
		}
		if d.TotalDue > 0 && d.Paid > 0 {
			item["progress"] = min(100, int(d.Paid*100/d.TotalDue))
		}
		items = append(items, item)
	}
	jsonResponse(w, map[string]any{
		"version":      1,
		"stats":        stats,
		"items_title":  "Card payments due",
		"items_layout": "list",
		"items":        items,
		"progress":     []any{},
		// Foyer's Drop offers PDFs to Ledger, for statements banks only link to.
		"accepts": map[string]any{
			"url":   "/api/statements/upload",
			"types": []string{".pdf", "application/pdf"},
			"label": "Send to Ledger",
		},
	})
}

// dueStat is the total still owed on cards, toned by the nearest due date;
// nil when no card statement has been imported.
func dueStat(dues []store.Due) map[string]string {
	if len(dues) == 0 {
		return nil
	}
	var total int64
	nearest, open := 0, 0
	for _, d := range dues {
		if d.Status == "paid" {
			continue
		}
		if open == 0 || d.Days < nearest {
			nearest = d.Days
		}
		total += d.Remaining
		open++
	}
	stat := map[string]string{"label": "Card dues", "value": reminders.Rupees(total), "caption": "All paid", "tone": "good"}
	if open > 0 {
		stat["caption"] = "Next " + reminders.When(nearest)
		stat["tone"] = "accent"
		if nearest < 0 {
			stat["tone"] = "bad"
		} else if nearest <= 3 {
			stat["tone"] = "warn"
		}
	}
	return stat
}
