package server

import (
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/game"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/themes"
)

func (s *Server) platformRoutes(r chi.Router) {
	if s.options.Accounts != nil {
		r.Mount("/account", s.options.Accounts.Handler())
	}
	if s.options.Themes != nil {
		s.themeRoutes(r)
	}
	if s.options.Library == nil {
		return
	}
	r.Get("/privacy/subjects", s.privacySubjects)
	r.Get("/privacy/export", s.privacyExport)
	r.Get("/usage", s.usage)
	r.Get("/catalogue", s.catalogue)
	r.Get("/catalogue/{id}", s.publicCard)
	r.Get("/catalogue/{id}/download", s.publicDownload)
	r.Get("/drafts", s.myDrafts)
	r.Post("/drafts", s.newDraft)
	r.Get("/drafts/{id}", s.getDraft)
	r.Put("/drafts/{id}", s.updateDraft)
	r.Delete("/drafts/{id}", s.deleteDraft)
	r.Get("/drafts/{id}/history", s.history)
	r.Get("/drafts/{id}/decisions", s.decisionHistory)
	r.Get("/drafts/{id}/export", s.exportDraft)
	r.Get("/drafts/{id}/original", s.originalDraft)
	r.Get("/drafts/{id}/assets/{key}", s.draftAsset)
	r.Get("/drafts/{id}/prepared/{key}", s.preparedAsset)
	r.Post("/drafts/{id}/publish", s.publish)
	r.Post("/drafts/{id}/withdraw", s.withdraw)
	r.Get("/moderation", s.moderation)
	r.Post("/moderation/{id}", s.decide)
	r.Get("/reports", s.reports)
	r.Post("/reports/{id}/resolution", s.resolveReport)
	r.Post("/catalogue/{id}/reports", s.report)
	r.Get("/rooms/{room}/board", s.board)
	r.Post("/rooms/{room}/select", s.selectQuestion)
	r.Post("/rooms/{room}/round", s.startRound)
	r.Post("/rooms/{room}/media-ticket", s.mediaTicket)
	r.Get("/rooms/{room}/media/{media}", s.roomMedia)
	r.Head("/rooms/{room}/media/{media}", s.roomMedia)
	if s.options.Uploads != nil {
		r.Post("/media-uploads", s.mediaUploadCreate)
		r.Post("/drafts/{id}/assets", s.attachMedia)
		r.Post("/uploads", s.uploadCreate)
		r.Get("/uploads/{id}", s.uploadGet)
		r.Patch("/uploads/{id}", s.uploadAppend)
		r.Post("/uploads/{id}/complete", s.uploadComplete)
	}
}
func (s *Server) identity(w http.ResponseWriter, r *http.Request) (accounts.Identity, bool) {
	if s.options.Accounts == nil {
		appError(w, accounts.ErrUnauthorized)
		return accounts.Identity{}, false
	}
	a, err := s.options.Accounts.FromRequest(r)
	if err != nil {
		appError(w, err)
		return a, false
	}
	return a, true
}
func (s *Server) optionalIdentity(w http.ResponseWriter, r *http.Request) (accounts.Identity, bool) {
	if r.Header.Get("Authorization") == "" {
		if _, err := r.Cookie("si_session"); err != nil {
			return accounts.Identity{}, true
		}
	}
	return s.identity(w, r)
}
func appError(w http.ResponseWriter, err error) {
	status, code := 503, "unavailable"
	switch {
	case errors.Is(err, accounts.ErrUnauthorized), errors.Is(err, library.ErrUnauthorized), errors.Is(err, themes.ErrUnauthorized), errors.Is(err, ingest.ErrUnauthorized):
		status, code = 401, "unauthorized"
	case errors.Is(err, accounts.ErrForbidden), errors.Is(err, library.ErrForbidden), errors.Is(err, themes.ErrForbidden), errors.Is(err, ingest.ErrForbidden):
		status, code = 403, "forbidden"
	case errors.Is(err, library.ErrNotFound), errors.Is(err, themes.ErrNotFound), errors.Is(err, ingest.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, library.ErrConflict), errors.Is(err, themes.ErrConflict), errors.Is(err, ingest.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(err, library.ErrBlocked), errors.Is(err, themes.ErrBlocked):
		status, code = 403, "blocked"
	case errors.Is(err, library.ErrQuota), errors.Is(err, themes.ErrQuota), errors.Is(err, ingest.ErrQuota):
		status, code = 429, "quota"
	case errors.Is(err, library.ErrInvalid), errors.Is(err, themes.ErrInvalid), errors.Is(err, ingest.ErrInvalid), errors.Is(err, accounts.ErrInvalidInput):
		status, code = 400, "invalid_input"
	default:
		if c := game.Code(err); c != "unavailable" {
			httpError(w, err)
			return
		}
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func documentJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	m, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || m != "application/json" {
		return library.ErrInvalid
	}
	return strictJSON(http.MaxBytesReader(w, r.Body, 10<<20), dst)
}
func queryInt(r *http.Request, name string, def int) (int, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return def, nil
	}
	n, e := strconv.Atoi(value)
	if e != nil || n < 0 {
		return 0, library.ErrInvalid
	}
	return n, nil
}
func (s *Server) catalogue(w http.ResponseWriter, r *http.Request) {
	f := library.Filter{Query: r.URL.Query().Get("q"), Language: r.URL.Query().Get("language"), Tag: r.URL.Query().Get("tag"), Age: r.URL.Query().Get("age"), Limit: 50}
	var err error
	f.Offset, err = queryInt(r, "offset", 0)
	if err != nil {
		appError(w, err)
		return
	}
	if r.URL.Query().Has("difficulty") {
		n, e := queryInt(r, "difficulty", 0)
		if e != nil {
			appError(w, e)
			return
		}
		f.Difficulty = &n
	}
	if r.URL.Query().Has("media") {
		b, e := strconv.ParseBool(r.URL.Query().Get("media"))
		if e != nil {
			appError(w, library.ErrInvalid)
			return
		}
		f.HasMedia = &b
	}
	if r.URL.Query().Has("max_bytes") {
		n, e := strconv.ParseInt(r.URL.Query().Get("max_bytes"), 10, 64)
		if e != nil || n < 0 {
			appError(w, library.ErrInvalid)
			return
		}
		f.MaxBytes = n
	}
	list, err := s.options.Library.Catalogue(r.Context(), f)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}
func (s *Server) publicCard(w http.ResponseWriter, r *http.Request) {
	c, e := s.options.Library.Public(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, c)
}
func (s *Server) myDrafts(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	v, e := s.options.Library.Mine(r.Context(), a)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"items": v})
}
func emptyDocument() packs.Document {
	return packs.Document{Name: "Новый пакет", Version: "5.0", Language: "ru", Rounds: []packs.Round{{ID: "r1", Name: "Первый раунд", Themes: []packs.Theme{{ID: "t1", Name: "Новая тема", Questions: []packs.Question{{ID: "q1", Price: 100, Params: []packs.Parameter{{Name: "question", Type: "content", Items: []packs.Content{{Type: "text"}}}}, Right: []string{""}}}}}}}}
}
func (s *Server) newDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Document *packs.Document `json:"document"`
	}
	if e := documentJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	d := emptyDocument()
	if req.Document != nil {
		d = *req.Document
	}
	d.ImportIssues = nil
	v, e := s.options.Library.Create(r.Context(), a, library.Save{Document: d})
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) getDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	rev, e := queryInt(r, "revision", 0)
	if e != nil {
		appError(w, e)
		return
	}
	d, e := s.options.Library.Get(r.Context(), a, chi.URLParam(r, "id"), rev)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) updateDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Base     int            `json:"base_revision"`
		Document packs.Document `json:"document"`
	}
	if e := documentJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	id := chi.URLParam(r, "id")
	d, e := s.options.Library.UpdateDocument(r.Context(), a, id, req.Base, req.Document)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	d, e := s.options.Library.History(r.Context(), a, chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"items": d})
}
func (s *Server) deleteDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	if e := s.options.Library.Delete(r.Context(), a, chi.URLParam(r, "id")); e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision int            `json:"revision"`
		Rights   library.Rights `json:"rights"`
	}
	if e := readJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	c, e := s.options.Library.Submit(r.Context(), a, chi.URLParam(r, "id"), req.Revision, req.Rights)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, c)
}
func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	if e := s.options.Library.Withdraw(r.Context(), a, chi.URLParam(r, "id")); e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) moderation(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	v, e := s.options.Library.Queue(r.Context(), a)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"items": v})
}
func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision int    `json:"revision"`
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if e := readJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	c, e := s.options.Library.Decide(r.Context(), a, chi.URLParam(r, "id"), req.Revision, req.Decision, req.Reason)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, c)
}
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision int    `json:"revision"`
		Reason   string `json:"reason"`
	}
	if e := readJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	v, e := s.options.Library.Report(r.Context(), a, chi.URLParam(r, "id"), req.Revision, req.Reason)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) uploadCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if e := readJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	u, e := s.options.Uploads.Create(r.Context(), a, req.Name, req.Size)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 201, u)
}
func (s *Server) uploadGet(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	u, e := s.options.Uploads.Get(r.Context(), a, chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, u)
}
func (s *Server) uploadAppend(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	offset, e := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if e != nil || offset < 0 || r.ContentLength < 0 || r.ContentLength > 4<<20 {
		appError(w, ingest.ErrInvalid)
		return
	}
	if m, _, e := mime.ParseMediaType(r.Header.Get("Content-Type")); e != nil || m != "application/octet-stream" {
		appError(w, ingest.ErrInvalid)
		return
	}
	u, e := s.options.Uploads.Append(r.Context(), a, chi.URLParam(r, "id"), offset, http.MaxBytesReader(w, r.Body, 4<<20), r.ContentLength)
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, u)
}
func (s *Server) uploadComplete(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	u, e := s.options.Uploads.Complete(r.Context(), a, chi.URLParam(r, "id"))
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 202, u)
}
