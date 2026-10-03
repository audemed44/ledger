package server

import (
	"database/sql"
	"errors"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/store"
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
		// ?reread=1 re-reads the emails the parser handled before this
		// change, so they follow it.
		var handled store.Handled
		if p.ID != 0 && r.URL.Query().Get("reread") == "1" {
			var err error
			if handled, _, err = s.Store.HandledByID(p.ID); err != nil {
				failure(w, 500, "Could not find the parser's emails")
				return
			}
		}
		saved, err := s.Store.SaveParser(p)
		if err != nil {
			failure(w, 500, "Could not save parser")
			return
		}
		out := reread{Parser: saved}
		if out.Reread, out.Kept, err = s.Store.Reread(handled.IDs); err != nil {
			failure(w, 500, "Parser saved, but re-reading its emails stopped")
			return
		}
		jsonResponse(w, out)
	})
	// Handled counts the emails a parser has recorded or ignored, for the
	// editor to offer re-reading them.
	mux.HandleFunc("GET /api/parsers/{id}/handled", func(w http.ResponseWriter, r *http.Request) {
		h, ok, err := s.Store.HandledByID(pathID(r))
		if err != nil {
			failure(w, 500, "Could not find the parser's emails")
			return
		}
		if !ok {
			failure(w, 404, "Parser not found")
			return
		}
		jsonResponse(w, h)
	})
	mux.HandleFunc("DELETE /api/parsers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if id == 0 {
			failure(w, 400, "Invalid parser")
			return
		}
		// ?reread=1 also re-reads the emails it handled: with no parser for
		// them, their transactions go and they return to the inbox.
		var handled store.Handled
		if r.URL.Query().Get("reread") == "1" {
			var err error
			if handled, _, err = s.Store.HandledByID(id); err != nil {
				failure(w, 500, "Could not find the parser's emails")
				return
			}
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
		out := reread{}
		if out.Reread, out.Kept, err = s.Store.Reread(handled.IDs); err != nil {
			failure(w, 500, "Parser deleted, but re-reading its emails stopped")
			return
		}
		jsonResponse(w, out)
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
	// Debit card links: saving or removing one retries the backlog, so
	// alerts waiting for a link are recorded.
	mux.HandleFunc("GET /api/card-links", func(w http.ResponseWriter, r *http.Request) {
		links, err := s.Store.CardLinks()
		if err != nil {
			failure(w, 500, "Could not load debit cards")
			return
		}
		jsonResponse(w, links)
	})
	mux.HandleFunc("POST /api/card-links", func(w http.ResponseWriter, r *http.Request) {
		var l store.CardLink
		if !decode(w, r, &l) {
			return
		}
		if err := s.Store.SaveCardLink(l); err != nil {
			failure(w, 400, err.Error())
			return
		}
		if _, err := s.Store.Reprocess(); err != nil {
			failure(w, 500, "Link saved, but retrying the backlog stopped")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("DELETE /api/card-links", func(w http.ResponseWriter, r *http.Request) {
		var l store.CardLink
		if !decode(w, r, &l) {
			return
		}
		err := s.Store.DeleteCardLink(l.Issuer, l.Card)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Debit card not found")
			return
		}
		if err != nil {
			failure(w, 500, "Could not remove debit card")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	// Wordings: add an email's wording to an existing parser, or merge a
	// parser for the same alert into another.
	mux.HandleFunc("POST /api/parsers/{id}/wordings", func(w http.ResponseWriter, r *http.Request) {
		var body alerts.Wording
		if !decode(w, r, &body) {
			return
		}
		p, err := s.Store.AddWording(pathID(r), body)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Parser not found")
			return
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		jsonResponse(w, p)
	})
	mux.HandleFunc("POST /api/parsers/{id}/merge", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From int64 `json:"from"`
		}
		if !decode(w, r, &body) {
			return
		}
		p, err := s.Store.MergeParsers(pathID(r), body.From)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Parser not found")
			return
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		jsonResponse(w, p)
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

// reread is a parser change's result: the parser, and how many of its
// emails were re-read or kept (confirmed by a statement).
type reread struct {
	alerts.Parser
	Reread int `json:"reread"`
	Kept   int `json:"kept"`
}
