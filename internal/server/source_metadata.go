package server

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"siplatform/internal/library"
)

func (s *Server) sourceMetadataSave(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var in struct {
		ExternalID string                 `json:"external_id"`
		Epoch      int64                  `json:"epoch"`
		Metadata   library.SourceMetadata `json:"metadata"`
	}
	if e := strictJSON(http.MaxBytesReader(w, r.Body, 600<<10), &in); e != nil {
		appError(w, e)
		return
	}
	if e := s.options.Library.SaveSourceMetadata(r.Context(), a, in.ExternalID, in.Epoch, in.Metadata); e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) sourceCoverAttach(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var in struct {
		ExternalID string `json:"external_id"`
		Epoch      int64  `json:"epoch"`
		UploadID   string `json:"upload_id"`
		SourceURL  string `json:"source_url"`
	}
	if e := readJSON(w, r, &in); e != nil {
		appError(w, e)
		return
	}
	if e := s.options.Library.AttachSourceCover(r.Context(), a, in.ExternalID, in.Epoch, in.UploadID, in.SourceURL); e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) publicSourceMetadata(w http.ResponseWriter, r *http.Request) {
	d, e := s.options.Library.PublicSourceMetadata(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, d)
}
func (s *Server) publicCover(w http.ResponseWriter, r *http.Request) {
	v, e := s.options.Library.PublicCover(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	s.serveBlobAs(w, r, v.Key, v.MIME, "cover.png", "inline")
}
