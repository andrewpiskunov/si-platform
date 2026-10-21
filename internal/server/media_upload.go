package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
	"siplatform/internal/library"
)

func (s *Server) mediaUploadCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		Kind string `json:"kind"`
	}
	if err := readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	upload, err := s.options.Uploads.CreateMedia(r.Context(), actor, req.Name, req.Size, req.Kind)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 201, upload)
}
func (s *Server) attachMedia(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		UploadID     string `json:"upload_id"`
		BaseRevision int    `json:"base_revision"`
	}
	if err := readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	if !game.ValidID(req.UploadID) || req.BaseRevision < 1 {
		appError(w, library.ErrInvalid)
		return
	}
	upload, err := s.options.Uploads.Get(r.Context(), actor, req.UploadID)
	if err != nil {
		appError(w, err)
		return
	}
	if upload.Purpose != "media" || upload.Status != "ready" || upload.Asset == nil {
		appError(w, library.ErrConflict)
		return
	}
	draft, err := s.options.Library.AttachAsset(r.Context(), actor, chi.URLParam(r, "id"), req.BaseRevision, req.UploadID, *upload.Asset)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, draft)
}
