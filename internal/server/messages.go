package server

import (
	"database/sql"
	"errors"
	"net/http"
)

func (s *Server) messageRoutes(mux *http.ServeMux) {
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
		m, err := s.Store.ReviewMessage(pathID(r))
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
	// Ignore saves a rule ignoring emails like this one, then retries the
	// backlog so others like it leave the inbox too.
	mux.HandleFunc("POST /api/messages/{id}/ignore", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Store.IgnoreLike(pathID(r))
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Message not found")
			return
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		n, err := s.Store.Reprocess()
		if err != nil {
			failure(w, 500, "Rule saved, but retrying the backlog stopped")
			return
		}
		jsonResponse(w, map[string]any{"parser": p, "processed": n})
	})
	mux.HandleFunc("POST /api/reprocess", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.Store.Reprocess()
		if err != nil {
			failure(w, 500, "Reprocessing stopped; remaining mail stays queued")
			return
		}
		jsonResponse(w, map[string]int{"processed": n})
	})
	mux.HandleFunc("GET /api/sync", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, s.Poller.Status())
	})
	// Sync is bounded and synchronous; a disconnect cancels the network work.
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		if !s.Poller.Status().Configured {
			failure(w, 409, "Configure Gmail in the environment first")
			return
		}
		s.Poller.Sync(r.Context())
		jsonResponse(w, s.Poller.Status())
	})
}
