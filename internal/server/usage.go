package server

import "net/http"

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	result, err := s.options.Library.Usage(r.Context(), actor)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
