package server

import (
	"database/sql"
	"errors"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/audemed44/ledger/internal/alerts"
)

func (s *Server) parserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/parsers", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Store.Parsers()
		if err != nil {
			failure(w, 500, "Could not load parsers")
			return
		}
		jsonResponse(w, p)
	})
	mux.HandleFunc("POST /api/parsers", func(w http.ResponseWriter, r *http.Request) {
		var p alerts.Parser
		if !decode(w, r, &p) {
			return
		}
		if err := p.Validate(); err != nil {
			failure(w, 400, err.Error())
			return
		}
		saved, err := s.Store.SaveParser(p)
		if err != nil {
			failure(w, 500, "Could not save parser")
			return
		}
		jsonResponse(w, saved)
	})
	mux.HandleFunc("DELETE /api/parsers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if id == 0 {
			failure(w, 400, "Invalid parser")
			return
		}
		err := s.Store.DeleteParser(id)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Parser not found")
			return
		}
		if err != nil {
			failure(w, 500, "Could not delete parser")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	// Preview runs a draft parser over a queued message or a pasted sample.
	mux.HandleFunc("POST /api/parsers/preview", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parser    alerts.Parser `json:"parser"`
			MessageID int64         `json:"message_id"`
			Sender    string        `json:"sender"`
			Subject   string        `json:"subject"`
			Body      string        `json:"body"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.MessageID != 0 {
			m, err := s.Store.Message(body.MessageID)
			if err != nil {
				failure(w, 404, "Message not found")
				return
			}
			body.Sender, body.Subject, body.Body = m.Sender, m.Subject, m.Body
		}
		result, err := body.Parser.Parse(body.Sender, body.Subject, body.Body)
		if err != nil {
			failure(w, 422, err.Error())
			return
		}
		jsonResponse(w, result)
	})
	// From-example writes a body pattern and date layout from tagged text.
	mux.HandleFunc("POST /api/parsers/from-example", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Subject string        `json:"subject"`
			Body    string        `json:"body"`
			Marks   []alerts.Mark `json:"marks"`
		}
		if !decode(w, r, &body) {
			return
		}
		example, err := alerts.FromExample(body.Subject, body.Body, body.Marks)
		if err != nil {
			failure(w, 422, err.Error())
			return
		}
		jsonResponse(w, example)
	})
	mux.HandleFunc("GET /api/parsers.yaml", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Store.Parsers()
		if err != nil {
			failure(w, 500, "Could not export parsers")
			return
		}
		raw, err := yaml.Marshal(p)
		if err != nil {
			failure(w, 500, "Could not export parsers")
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", `attachment; filename="ledger-parsers.yaml"`)
		w.Write(raw)
	})
}
