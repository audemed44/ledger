// Package server is Ledger's HTTP API and the embedded frontend. Every /api/
// call needs the token, as a bearer or the session cookie derived from it,
// and state-changing requests from another origin are refused.
package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/audemed44/ledger/internal/gmail"
	"github.com/audemed44/ledger/internal/reminders"
	"github.com/audemed44/ledger/internal/store"
)

// Server serves the API and frontend.
type Server struct {
	Store         *store.Store
	Poller        *gmail.Poller
	Token         string
	SecureCookies bool
	Demo          bool
	Files         fs.FS
	// Reminders sends card payment reminders; nil or without a URL, none.
	Reminders *reminders.Notifier
}

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	s.authRoutes(mux)
	s.transactionRoutes(mux)
	s.messageRoutes(mux)
	s.parserRoutes(mux)
	s.statementRoutes(mux)
	s.dueRoutes(mux)
	mux.HandleFunc("GET /api/foyer/widget", s.widget)
	if s.Files != nil {
		mux.Handle("GET /", http.FileServerFS(s.Files))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "GET" && r.Method != "HEAD" && !sameOrigin(r) {
				failure(w, 403, "Cross-origin request refused")
				return
			}
			if r.URL.Path != "/api/login" && !s.authenticated(r) {
				failure(w, 401, "Sign in to Ledger")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// sameOrigin refuses browser requests from another site. Requests without
// an Origin (curl, Foyer) pass; they still need the token.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
}

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func failure(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// decode reads exactly one JSON object of at most 2 MiB, with no unknown
// fields. On failure it has already written the 400.
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

// pathID is the {id} path value, or 0 when it isn't a positive integer.
func pathID(r *http.Request) int64 {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}
