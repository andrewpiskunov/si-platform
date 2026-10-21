package server

import (
	"errors"
	"mime"
	"net/http"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/ai"
	"siplatform/internal/aiformat"
)

func (s *Server) aiRoutes(r chi.Router) {
	r.Get("/ai/status", s.aiStatus)
	r.Post("/ai/quotes", s.aiQuote)
	r.Post("/ai/jobs", s.aiStart)
	r.Get("/ai/jobs", s.aiList)
	r.Get("/ai/jobs/{id}", s.aiGet)
	r.Post("/ai/jobs/{id}/cancel", s.aiCancel)
	r.Post("/ai/jobs/{id}/preview", s.aiPreview)
	r.Post("/ai/jobs/{id}/draft", s.aiDraft)
}
func (s *Server) aiIdentity(w http.ResponseWriter, r *http.Request) (accounts.Identity, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	actor, ok := s.identity(w, r)
	if !ok {
		return actor, false
	}
	if s.options.AI == nil {
		aiError(w, ai.ErrDisabled)
		return actor, false
	}
	return actor, true
}
func aiError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ai.ErrDisabled):
		writeJSON(w, 503, map[string]string{"error": "ai_disabled"})
	case errors.Is(err, ai.ErrLimit):
		writeJSON(w, 429, map[string]string{"error": "limit_exceeded"})
	case errors.Is(err, ai.ErrInvalid), errors.Is(err, aiformat.ErrInvalid):
		writeJSON(w, 400, map[string]string{"error": "invalid_input"})
	case errors.Is(err, ai.ErrConflict):
		writeJSON(w, 409, map[string]string{"error": "conflict"})
	case errors.Is(err, ai.ErrNotFound):
		writeJSON(w, 404, map[string]string{"error": "not_found"})
	default:
		appError(w, err)
	}
}
func aiJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return ai.ErrInvalid
	}
	if err = strictJSON(http.MaxBytesReader(w, r.Body, limit), dst); err != nil {
		return ai.ErrInvalid
	}
	return nil
}
func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	out, err := s.options.AI.Status(r.Context(), actor)
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) aiQuote(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	var in aiformat.Request
	if err := aiJSON(w, r, &in, 16<<10); err != nil {
		aiError(w, err)
		return
	}
	out, err := s.options.AI.Quote(r.Context(), actor, in)
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) aiStart(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	var in struct {
		QuoteID string `json:"quote_id"`
		Key     string `json:"idempotency_key"`
	}
	if err := aiJSON(w, r, &in, 16<<10); err != nil {
		aiError(w, err)
		return
	}
	out, err := s.options.AI.Start(r.Context(), actor, in.QuoteID, in.Key)
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) aiList(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	query, err := privacyQuery(r, "after")
	if err != nil {
		aiError(w, ai.ErrInvalid)
		return
	}
	out, err := s.options.AI.List(r.Context(), actor, query.Get("after"))
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) aiGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	out, err := s.options.AI.Get(r.Context(), actor, chi.URLParam(r, "id"))
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) aiCancel(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	var in struct{}
	if err := aiJSON(w, r, &in, 1024); err != nil {
		aiError(w, err)
		return
	}
	out, err := s.options.AI.Cancel(r.Context(), actor, chi.URLParam(r, "id"))
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

type aiReviewed struct {
	Reviewed bool            `json:"reviewed"`
	Result   aiformat.Result `json:"result"`
}

func (s *Server) aiPreview(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	var in aiReviewed
	if err := aiJSON(w, r, &in, 1<<20); err != nil {
		aiError(w, err)
		return
	}
	if !in.Reviewed {
		aiError(w, ai.ErrInvalid)
		return
	}
	job, err := s.options.AI.Get(r.Context(), actor, chi.URLParam(r, "id"))
	if err != nil {
		aiError(w, err)
		return
	}
	if job.Status != "ready" {
		aiError(w, ai.ErrConflict)
		return
	}
	if err = aiformat.ValidateResult(job.Request, in.Result); err != nil {
		aiError(w, ai.ErrInvalid)
		return
	}
	generated := job.CreatedAt
	if job.CompletedAt != nil {
		generated = *job.CompletedAt
	}
	writeJSON(w, 200, map[string]any{"document": aiformat.Document(in.Result, job.ID, job.Model, generated)})
}
func (s *Server) aiDraft(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.aiIdentity(w, r)
	if !ok {
		return
	}
	var in aiReviewed
	if err := aiJSON(w, r, &in, 1<<20); err != nil {
		aiError(w, err)
		return
	}
	out, err := s.options.Library.CreateAIDraft(r.Context(), actor, chi.URLParam(r, "id"), in.Reviewed, in.Result)
	if err != nil {
		aiError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
