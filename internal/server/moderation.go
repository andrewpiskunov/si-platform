package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/library"
)

func moderationPageLimit(r *http.Request) (int, error) {
	values, exists := r.URL.Query()["limit"]
	if !exists {
		return 50, nil
	}
	if len(values) != 1 {
		return 0, library.ErrInvalid
	}
	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > 50 {
		return 0, library.ErrInvalid
	}
	return limit, nil
}
func (s *Server) decisionHistory(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	limit, err := moderationPageLimit(r)
	if err != nil {
		appError(w, err)
		return
	}
	var after int64
	if values, exists := r.URL.Query()["after_id"]; exists {
		if len(values) != 1 {
			appError(w, library.ErrInvalid)
			return
		}
		after, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || after < 0 {
			appError(w, library.ErrInvalid)
			return
		}
	}
	page, err := s.options.Library.DecisionHistory(r.Context(), actor, chi.URLParam(r, "id"), after, limit)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *Server) reports(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	limit, err := moderationPageLimit(r)
	if err != nil {
		appError(w, err)
		return
	}
	query := r.URL.Query()
	if len(query["status"]) > 1 || len(query["after"]) > 1 {
		appError(w, library.ErrInvalid)
		return
	}
	status := query.Get("status")
	if status == "" {
		status = "open"
	}
	page, err := s.options.Library.ReportPage(r.Context(), actor, status, query.Get("after"), limit)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *Server) resolveReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	var request struct {
		Disposition string `json:"disposition"`
		Reason      string `json:"reason"`
	}
	if err := readJSON(w, r, &request); err != nil {
		appError(w, err)
		return
	}
	result, err := s.options.Library.ResolveReport(r.Context(), actor, chi.URLParam(r, "id"), request.Disposition, request.Reason)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
