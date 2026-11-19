package server

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/audemed44/ledger/internal/logging"
	"github.com/audemed44/ledger/internal/store"
)

func (s *Server) categoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/categories", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.Store.Categories()
		if err != nil {
			serverError(w, r, err, "Could not load categories")
			return
		}
		jsonResponse(w, out)
	})
	saveCategory := func(w http.ResponseWriter, r *http.Request, id int64) {
		var body struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &body) {
			return
		}
		c, err := s.Store.SaveCategory(id, body.Name)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Category not found")
			return
		}
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		logging.Audit(r.Context(), "category.save", "category", c.ID)
		jsonResponse(w, c)
	}
	mux.HandleFunc("POST /api/categories", func(w http.ResponseWriter, r *http.Request) {
		saveCategory(w, r, 0)
	})
	mux.HandleFunc("PUT /api/categories/{id}", func(w http.ResponseWriter, r *http.Request) {
		saveCategory(w, r, pathID(r))
	})
	mux.HandleFunc("DELETE /api/categories/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := s.Store.DeleteCategory(pathID(r))
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Category not found")
			return
		}
		if err != nil {
			serverError(w, r, err, "Could not delete category")
			return
		}
		logging.Audit(r.Context(), "category.delete", "category", pathID(r))
		jsonResponse(w, map[string]bool{"ok": true})
	})
	// The Insights page for a month, this month by default.
	mux.HandleFunc("GET /api/insights", func(w http.ResponseWriter, r *http.Request) {
		month := r.URL.Query().Get("month")
		if month == "" {
			month = time.Now().Format("2006-01")
		}
		if _, err := time.Parse("2006-01", month); err != nil {
			failure(w, 400, "Choose a month (YYYY-MM)")
			return
		}
		out, err := s.Store.MonthInsights(month, time.Now())
		if err != nil {
			serverError(w, r, err, "Could not load insights")
			return
		}
		jsonResponse(w, out)
	})
	// Put one transaction in a category by hand (0 hands it back to the rules).
	mux.HandleFunc("POST /api/transactions/{id}/category", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CategoryID int64 `json:"category_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		err := s.Store.SetCategory(pathID(r), body.CategoryID)
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Transaction or category not found")
			return
		}
		if err != nil {
			serverError(w, r, err, "Could not update transaction")
			return
		}
		logging.Audit(r.Context(), "transaction.category", "transaction", pathID(r), "category", body.CategoryID)
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/category-rules", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.Store.CategoryRules()
		if err != nil {
			serverError(w, r, err, "Could not load category rules")
			return
		}
		jsonResponse(w, out)
	})
	mux.HandleFunc("POST /api/category-rules", func(w http.ResponseWriter, r *http.Request) {
		var body store.CategoryRule
		if !decode(w, r, &body) {
			return
		}
		rule, err := s.Store.SaveCategoryRule(store.CategoryRule{Issuer: body.Issuer, Contains: body.Contains, CategoryID: body.CategoryID})
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		logging.Audit(r.Context(), "category_rule.save", "rule", rule.ID)
		jsonResponse(w, rule)
	})
	mux.HandleFunc("DELETE /api/category-rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := s.Store.DeleteCategoryRule(pathID(r))
		if errors.Is(err, sql.ErrNoRows) {
			failure(w, 404, "Rule not found")
			return
		}
		if err != nil {
			serverError(w, r, err, "Could not delete rule")
			return
		}
		logging.Audit(r.Context(), "category_rule.delete", "rule", pathID(r))
		jsonResponse(w, map[string]bool{"ok": true})
	})
}
