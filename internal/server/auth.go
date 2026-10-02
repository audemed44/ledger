package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

const sessionCookie = "ledger_session"

// session is the cookie value: derived from the token, so changing the
// token signs every browser out.
func (s *Server) session() string {
	h := hmac.New(sha256.New, []byte(s.Token))
	h.Write([]byte("ledger-session-v1"))
	return hex.EncodeToString(h.Sum(nil))
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) authenticated(r *http.Request) bool {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if equal(bearer, s.Token) {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	return err == nil && equal(c.Value, s.session())
}

func (s *Server) authRoutes(mux *http.ServeMux) {
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
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    s.session(),
			Path:     "/",
			HttpOnly: true,
			Secure:   s.SecureCookies,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   86400 * 30,
		})
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.SecureCookies,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		jsonResponse(w, map[string]bool{"ok": true})
	})
}
