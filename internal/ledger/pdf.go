package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/emersion/go-message/mail"
)

type StatementParser struct {
	BalanceTolerancePaise int64  `json:"balance_tolerance_paise"`
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	Adapter               string `json:"adapter"`
	PasswordSlot          int    `json:"password_slot"`
}

func (p StatementParser) Validate() error {
	if p.BalanceTolerancePaise < 0 || p.BalanceTolerancePaise > 99 {
		return errors.New("Balance tolerance must be between 0 and 99 paise")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 {
		return errors.New("Statement parser name is required (up to 100 characters)")
	}
	if p.Adapter != "hdfc-credit-card" {
		return errors.New("Unsupported statement layout; a dedicated adapter is needed")
	}
	if p.PasswordSlot < 0 || p.PasswordSlot > 32 {
		return errors.New("Invalid password slot")
	}
	return nil
}
func (s *Store) StatementParsers() ([]StatementParser, error) {
	rows, err := s.DB.Query("SELECT id,definition FROM statement_parsers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatementParser{}
	for rows.Next() {
		var p StatementParser
		var raw string
		if err = rows.Scan(&p.ID, &raw); err != nil {
			return nil, err
		}
		id := p.ID
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		p.ID = id
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveStatementParser(p StatementParser) (StatementParser, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if p.ID == 0 {
		r, e := s.DB.Exec("INSERT INTO statement_parsers(definition) VALUES(?)", string(raw))
		if e != nil {
			return p, e
		}
		p.ID, err = r.LastInsertId()
	} else {
		r, e := s.DB.Exec("UPDATE statement_parsers SET definition=? WHERE id=?", string(raw), p.ID)
		if e != nil {
			return p, e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return p, e
		}
		if n == 0 {
			return p, sql.ErrNoRows
		}
	}
	return p, err
}

type PDFPreview struct {
	Text         string     `json:"text"`
	PasswordSlot int        `json:"password_slot"`
	ParserName   string     `json:"parser_name,omitempty"`
	Statement    *Statement `json:"statement,omitempty"`
	ParseError   string     `json:"parse_error,omitempty"`
}

func (s *Store) previewPDF(ctx context.Context, id int64, part int, p *StatementParser, passwords []string) (PDFPreview, error) {
	var out PDFPreview
	if part < 0 {
		return out, errors.New("Invalid attachment")
	}
	raw, err := s.archivedRaw(id)
	if err != nil {
		return out, errors.New("Could not read archived email")
	}
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return out, errors.New("Could not decode archived email")
	}
	defer reader.Close()
	for index := 0; ; index++ {
		item, e := reader.NextPart()
		if e == io.EOF {
			return out, errors.New("PDF attachment not found")
		}
		if e != nil {
			return out, errors.New("Could not decode attachment")
		}
		if index != part {
			continue
		}
		kind, name := "", ""
		switch h := item.Header.(type) {
		case *mail.AttachmentHeader:
			kind, _, _ = h.ContentType()
			name, _ = h.Filename()
		case *mail.InlineHeader:
			kind, _, _ = h.ContentType()
		}
		if kind != "application/pdf" && !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			return out, errors.New("Selected attachment is not a PDF")
		}
		f, e := os.CreateTemp("", "ledger-attachment-*.pdf")
		if e != nil {
			return out, errors.New("Could not prepare PDF for extraction")
		}
		defer os.Remove(f.Name())
		n, e := io.Copy(f, io.LimitReader(item.Body, maxMail+1))
		closeErr := f.Close()
		if e != nil || closeErr != nil || n > maxMail {
			return out, errors.New("PDF exceeds size limit or could not be read")
		}
		slot := 0
		if p != nil {
			slot = p.PasswordSlot
		}
		out.Text, out.PasswordSlot, err = extractPDFPasswords(ctx, f.Name(), passwords, slot)
		if err != nil {
			return out, err
		}
		if p != nil {
			out.ParserName = p.Name
			statement, e := ParseHDFCStatementWithTolerance(out.Text, p.BalanceTolerancePaise)
			if e != nil {
				out.ParseError = e.Error()
			} else {
				out.Statement = &statement
			}
		}
		return out, nil
	}
}

func (s *Server) pdfRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pdf-config", func(w http.ResponseWriter, r *http.Request) {
		slots := []int{}
		for i, p := range s.PDFPasswords {
			if p != "" {
				slots = append(slots, i+1)
			}
		}
		jsonResponse(w, map[string]any{"password_slots": slots, "adapters": []map[string]string{{"id": "hdfc-credit-card", "name": "HDFC Credit Card", "description": "HDFC Tata Neu statement layout"}}})
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
		var p StatementParser
		if !decode(w, r, &p) {
			return
		}
		if err := p.Validate(); err != nil {
			failure(w, 400, err.Error())
			return
		}
		if p.PasswordSlot > len(s.PDFPasswords) || (p.PasswordSlot > 0 && s.PDFPasswords[p.PasswordSlot-1] == "") {
			failure(w, 400, "That password slot is not configured on the server")
			return
		}
		saved, err := s.Store.SaveStatementParser(p)
		if err != nil {
			failure(w, 500, "Could not save statement parser")
			return
		}
		jsonResponse(w, saved)
	})
	// PDF work is on demand, with one extraction at a time to bound memory/CPU.
	slots := make(chan struct{}, 1)
	mux.HandleFunc("POST /api/messages/{id}/pdf", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Part     int   `json:"part"`
			ParserID int64 `json:"parser_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var parser *StatementParser
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
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			failure(w, 429, "Another PDF is being processed. Try again shortly")
			return
		}
		result, err := s.Store.previewPDF(r.Context(), id, body.Part, parser, s.PDFPasswords)
		if err != nil {
			failure(w, 422, err.Error())
			return
		}
		jsonResponse(w, result)
	})
}
