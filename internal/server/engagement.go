package server

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"siplatform/internal/library"
)

func (s *Server) myPackageVote(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	v, e := s.options.Library.MyVote(r.Context(), a, chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, v)
}
func (s *Server) setPackageVote(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var v library.Vote
	if e := readJSON(w, r, &v); e != nil {
		appError(w, e)
		return
	}
	if e := s.options.Library.SetVote(r.Context(), a, chi.URLParam(r, "id"), v); e != nil {
		appError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, v)
}
