package server

import (
	"net/http"
	"siplatform/internal/ingest"
)

func (s *Server) externalImport(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var in ingest.ExternalInput
	if e := readJSON(w, r, &in); e != nil {
		appError(w, e)
		return
	}
	u, e := s.options.Uploads.CreateExternal(r.Context(), a, in)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 201, u)
}
func (s *Server) externalPublish(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var in struct {
		ExternalID string `json:"external_id"`
	}
	if e := readJSON(w, r, &in); e != nil {
		appError(w, e)
		return
	}
	c, e := s.options.Library.PublishExternal(r.Context(), a, "sibrowser", in.ExternalID)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) externalRetry(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var in ingest.ExternalInput
	if e := readJSON(w, r, &in); e != nil {
		appError(w, e)
		return
	}
	u, e := s.options.Uploads.RetryExternal(r.Context(), a, in)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, u)
}
