package server

import (
	"net/http"
	"siplatform/internal/ingest"
)

func (s *Server) catalogueImport(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var in ingest.CatalogueInput
	if e := readJSON(w, r, &in); e != nil {
		appError(w, e)
		return
	}
	u, e := s.options.Uploads.CreateCatalogue(r.Context(), a, in)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 201, u)
}
