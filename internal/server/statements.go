package server

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/audemed44/ledger/internal/statements"
	"github.com/audemed44/ledger/internal/store"
)

func (s *Server) statementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pdf-config", func(w http.ResponseWriter, r *http.Request) {
		slots := []int{}
		// The API exposes which slots are set, never their values.
		for i, p := range s.Store.PDFPasswords {
			if p != "" {
				slots = append(slots, i+1)
			}
		}
		jsonResponse(w, map[string]any{"password_slots": slots, "adapters": statements.Adapters})
	})
	mux.HandleFunc("GET /api/statement-parsers", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Store.StatementParsers()
		if err != nil {
			failure(w, 500, "Could not load statement parsers")
			return
		}
		jsonResponse(w, p)
	})
	mux.HandleFunc("POST /api/statement-parsers", func(w http.ResponseWriter, r *http.Request) {
		var p statements.Parser
		if !decode(w, r, &p) {
			return
		}
		if err := p.Validate(); err != nil {
			failure(w, 400, err.Error())
			return
		}
		if !s.passwordSlotSet(p.PasswordSlot) {
			failure(w, 400, "That password slot is not configured on the server")
			return
		}
		saved, err := s.Store.SaveStatementParser(p)
		if errors.Is(err, store.ErrParserNameTaken) {
			failure(w, 409, err.Error())
			return
		}
		if err != nil {
			failure(w, 500, "Could not save statement parser")
			return
		}
		jsonResponse(w, saved)
	})
	mux.HandleFunc("DELETE /api/statement-parsers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if id == 0 {
			failure(w, 400, "Invalid parser")
			return
		}
		err := s.Store.DeleteStatementParser(id)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Statement parser not found")
			return
		}
		if err != nil {
			failure(w, 500, "Could not delete statement parser")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})

	// Upload takes a PDF statement downloaded by hand, from the app or from
	// Foyer's Drop (multipart field "file"). Foyer shows message and url.
	mux.HandleFunc("POST /api/statements/upload", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, store.MaxUpload+1<<20)
		// Beyond 1 MiB the upload is spooled to disk, not held in memory.
		if r.ParseMultipartForm(1<<20) != nil {
			failure(w, 400, "Choose a PDF to upload (at most 18 MiB)")
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			failure(w, 400, "Choose a PDF to upload (at most 18 MiB)")
			return
		}
		defer file.Close()
		pdf, err := io.ReadAll(io.LimitReader(file, store.MaxUpload+1))
		if err != nil {
			failure(w, 400, "Could not read the upload")
			return
		}
		m, err := s.Store.Upload(header.Filename, pdf)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		message := "Statement imported"
		if m.State == "queued" {
			message = "Saved to the Inbox for review"
			if m.Reason != "" {
				message += ": " + m.Reason
			}
		}
		jsonResponse(w, map[string]any{"id": m.ID, "state": m.State, "reason": m.Reason, "message": message, "url": "/#inbox"})
	})

	// PDF work runs one extraction at a time, to bound memory and CPU.
	busy := make(chan struct{}, 1)
	mux.HandleFunc("POST /api/messages/{id}/pdf", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Part        int    `json:"part"`
			ParserID    int64  `json:"parser_id"`
			Import      bool   `json:"import"`
			Fingerprint string `json:"fingerprint"`
			// Automatic also makes the parser import statements like this
			// one by themselves, once this one is imported.
			Automatic bool `json:"automatic"`
		}
		if !decode(w, r, &body) {
			return
		}
		var parser *statements.Parser
		if body.ParserID != 0 {
			parsers, err := s.Store.StatementParsers()
			if err != nil {
				failure(w, 500, "Could not load statement parsers")
				return
			}
			for _, p := range parsers {
				if p.ID == body.ParserID {
					parser = &p
					break
				}
			}
			if parser == nil {
				failure(w, 404, "Statement parser not found")
				return
			}
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			failure(w, 429, "Another PDF is being processed. Try again shortly")
			return
		}
		id := pathID(r)
		result, err := s.Store.PreviewPDF(r.Context(), id, body.Part, parser, s.Store.PDFPasswords)
		if err != nil {
			failure(w, 422, err.Error())
			return
		}
		if body.Import {
			imported, err := s.Store.ImportStatement(id, body.Part, result, body.Fingerprint)
			if err != nil {
				failure(w, 409, err.Error())
				return
			}
			result.Imported = &imported
			if body.Automatic && parser != nil {
				if _, err = s.Store.AddStatementTrigger(parser.ID, id, body.Part); err != nil {
					result.AutomaticError = err.Error()
				} else {
					result.Automatic = true
					// Statements like it already waiting in the inbox import
					// now, in the background.
					go func() {
						if _, err := s.Store.Reprocess(); err != nil {
							slog.Warn("retrying the backlog after saving an automatic import", "err", err)
						}
					}()
				}
			}
		}
		jsonResponse(w, result)
	})
}

// passwordSlotSet reports whether slot is 0 (try all) or a configured password.
func (s *Server) passwordSlotSet(slot int) bool {
	passwords := s.Store.PDFPasswords
	return slot == 0 || (slot <= len(passwords) && passwords[slot-1] != "")
}
