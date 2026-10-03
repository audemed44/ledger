package server

import (
	"net/http"
	"time"

	"github.com/audemed44/ledger/internal/reminders"
	"github.com/audemed44/ledger/internal/store"
)

func (s *Server) dueRoutes(mux *http.ServeMux) {
	// Mark a card's statement paid, for payments Ledger saw no alert for.
	mux.HandleFunc("POST /api/dues/settle", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AccountID string `json:"account_id"`
			DueDate   string `json:"due_date"`
			Settled   bool   `json:"settled"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := s.Store.SettleDue(body.AccountID, body.DueDate, body.Settled); err != nil {
			failure(w, 400, "Invalid card or due date")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	// Foyer's Mark paid button posts {} here.
	mux.HandleFunc("POST /api/foyer/dues/{account}/{due}/paid", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.SettleDue(r.PathValue("account"), r.PathValue("due"), true); err != nil {
			failure(w, 400, "Invalid card or due date")
			return
		}
		jsonResponse(w, map[string]string{"message": "Marked paid"})
	})
	mux.HandleFunc("POST /api/reminders/test", func(w http.ResponseWriter, r *http.Request) {
		if !s.Reminders.Enabled() {
			failure(w, 409, "Set LEDGER_NOTIFY_URL to send reminders")
			return
		}
		title, body := reminders.Message(sampleDue())
		if err := s.Reminders.Send(r.Context(), "Test: "+title, body, "info"); err != nil {
			failure(w, 502, err.Error())
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
}

// sampleDue is the made-up card a test reminder describes.
func sampleDue() store.Due {
	return store.Due{
		Issuer: "Example Bank", LastFour: "0000", Currency: "INR", Days: 3, Status: "due",
		DueDate:  time.Now().AddDate(0, 0, 3).Format("2006-01-02"),
		TotalDue: 1234500, MinimumDue: 61700, Remaining: 1234500,
	}
}
