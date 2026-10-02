package ledger

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Server struct {
	Store         *Store
	Poller        *Poller
	Token         string
	SecureCookies bool
	Demo          bool
	Files         fs.FS
	PDFPasswords  []string
}

func (s *Server) cookie() string {
	h := hmac.New(sha256.New, []byte(s.Token))
	h.Write([]byte("ledger-session-v1"))
	return hex.EncodeToString(h.Sum(nil))
}
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		failure(w, 400, "Invalid request body")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		failure(w, 400, "Expected one JSON object")
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.pdfRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &body) {
			return
		}
		if !equal(body.Token, s.Token) {
			failure(w, 401, "Invalid access token")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "ledger_session", Value: s.cookie(), Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 86400 * 30})
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "ledger_session", Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/summary", s.summary)
	mux.HandleFunc("GET /api/transactions", func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.Transactions(filter(r))
		if err != nil {
			failure(w, 500, "Could not load transactions")
			return
		}
		jsonResponse(w, rows)
	})
	mux.HandleFunc("GET /api/transactions.csv", s.csv)
	mux.HandleFunc("GET /api/messages", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind != "" && kind != "all" && kind != "pdf" && kind != "text" {
			failure(w, 400, "Invalid inbox filter")
			return
		}
		rows, err := s.Store.FilteredMessages(kind)
		if err != nil {
			failure(w, 500, "Could not load queue")
			return
		}
		jsonResponse(w, rows)
	})
	mux.HandleFunc("GET /api/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		m, err := s.Store.ReviewMessage(id)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Message not found")
			return
		}
		if err != nil {
			failure(w, 500, "Could not load message")
			return
		}
		jsonResponse(w, m)
	})
	mux.HandleFunc("GET /api/parsers", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Store.Parsers()
		if err != nil {
			failure(w, 500, "Could not load parsers")
			return
		}
		jsonResponse(w, p)
	})
	mux.HandleFunc("POST /api/parsers", func(w http.ResponseWriter, r *http.Request) {
		var p Parser
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
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			failure(w, 400, "Invalid parser")
			return
		}
		s.Store.mu.Lock()
		defer s.Store.mu.Unlock()
		result, err := s.Store.DB.Exec("DELETE FROM parsers WHERE id=?", id)
		if err != nil {
			failure(w, 500, "Could not delete parser")
			return
		}
		count, err := result.RowsAffected()
		if err != nil {
			failure(w, 500, "Could not delete parser")
			return
		}
		if count == 0 {
			failure(w, 404, "Parser not found")
			return
		}
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/parsers/preview", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parser    Parser `json:"parser"`
			MessageID int64  `json:"message_id"`
			Sender    string `json:"sender"`
			Subject   string `json:"subject"`
			Body      string `json:"body"`
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
	mux.HandleFunc("POST /api/reprocess", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.Store.Reprocess()
		if err != nil {
			failure(w, 500, "Reprocessing stopped; remaining mail stays queued")
			return
		}
		jsonResponse(w, map[string]int{"processed": n})
	})
	mux.HandleFunc("GET /api/sync", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, s.Poller.Status()) })
	// Sync is bounded and synchronous; disconnects cancel network work safely.
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		if !s.Poller.Status().Configured {
			failure(w, 409, "Configure Gmail in the environment first")
			return
		}
		s.Poller.Sync(r.Context())
		jsonResponse(w, s.Poller.Status())
	})
	mux.HandleFunc("GET /api/foyer/widget", s.widget)
	if s.Files != nil {
		mux.Handle("GET /", http.FileServerFS(s.Files))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "GET" && r.Method != "HEAD" {
				origin := r.Header.Get("Origin")
				if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
					failure(w, 403, "Cross-site request refused")
					return
				}
				if origin != "" {
					u, err := url.Parse(origin)
					if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
						failure(w, 403, "Cross-origin request refused")
						return
					}
				}
			}
			if r.URL.Path != "/api/login" {
				bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				valid := equal(bearer, s.Token)
				if c, err := r.Cookie("ledger_session"); err == nil {
					valid = valid || equal(c.Value, s.cookie())
				}
				if !valid {
					failure(w, 401, "Sign in to Ledger")
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func filter(r *http.Request) Filter {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	return Filter{Search: q.Get("search"), Account: q.Get("account"), Status: q.Get("status"), From: q.Get("from"), To: q.Get("to"), Offset: offset}
}

type Total struct {
	Currency string `json:"currency"`
	Debit    int64  `json:"debit"`
	Credit   int64  `json:"credit"`
}
type Summary struct {
	Totals       []Total   `json:"totals"`
	Accounts     []Account `json:"accounts"`
	Transactions int       `json:"transactions"`
	Queued       int       `json:"queued"`
	Parsers      int       `json:"parsers"`
	Month        string    `json:"month"`
	Demo         bool      `json:"demo"`
}

func (s *Server) getSummary() (Summary, error) {
	out := Summary{Totals: []Total{}, Accounts: []Account{}, Month: time.Now().Format("2006-01"), Demo: s.Demo}
	for _, c := range []struct {
		query string
		dest  *int
	}{{"SELECT count(*) FROM transactions", &out.Transactions}, {"SELECT count(*) FROM messages WHERE state='queued'", &out.Queued}, {"SELECT count(*) FROM parsers", &out.Parsers}} {
		if err := s.Store.DB.QueryRow(c.query).Scan(c.dest); err != nil {
			return out, err
		}
	}
	rows, err := s.Store.DB.Query(`SELECT currency,SUM(CASE WHEN direction='debit' THEN amount ELSE 0 END),SUM(CASE WHEN direction='credit' THEN amount ELSE 0 END) FROM transactions WHERE substr(date,1,7)=? GROUP BY currency ORDER BY currency`, out.Month)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t Total
		if err = rows.Scan(&t.Currency, &t.Debit, &t.Credit); err != nil {
			rows.Close()
			return out, err
		}
		out.Totals = append(out.Totals, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Accounts, err = s.Store.accounts()
	return out, err
}
func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	out, err := s.getSummary()
	if err != nil {
		failure(w, 500, "Could not load summary")
		return
	}
	jsonResponse(w, out)
}
func (s *Server) widget(w http.ResponseWriter, r *http.Request) {
	out, err := s.getSummary()
	if err != nil {
		failure(w, 500, "Could not load widget")
		return
	}
	stats := []map[string]string{}
	for _, t := range out.Totals {
		stats = append(stats, map[string]string{"label": "Debits this month", "value": decimal(t.Debit), "unit": t.Currency, "caption": "Provisional · includes transfers"})
	}
	if len(stats) == 0 {
		stats = append(stats, map[string]string{"label": "Debits this month", "value": "—", "caption": "No alerts yet"})
	}
	stats = append(stats, map[string]string{"label": "Needs review", "value": strconv.Itoa(out.Queued)})
	jsonResponse(w, map[string]any{"version": 1, "stats": stats, "items_layout": "list", "items": []any{}, "progress": []any{}})
}
func decimal(v int64) string { return fmt.Sprintf("%d.%02d", v/100, v%100) }
func csvSafe(s string) string {
	if strings.HasPrefix(strings.TrimSpace(s), "=") || strings.HasPrefix(strings.TrimSpace(s), "+") || strings.HasPrefix(strings.TrimSpace(s), "-") || strings.HasPrefix(strings.TrimSpace(s), "@") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
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
			record := []string{t.Date, t.Issuer, t.Account, t.AccountKind, t.Merchant, decimal(t.Amount), t.Currency, t.Direction, t.Status, t.Reference}
			for i := range record {
				record[i] = csvSafe(record[i])
			}
			if writer.Write(record) != nil {
				return
			}
		}
		if len(rows) < 100 {
			return
		}
		f.Offset += len(rows)
	}
}
