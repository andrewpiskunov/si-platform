package server

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"siplatform/internal/game"
	"siplatform/internal/media"
	"siplatform/internal/themes"
	"strconv"
)

func (s *Server) themeRoutes(r chi.Router) {
	r.Get("/themes", s.listThemes)
	r.Post("/themes", s.saveTheme)
	r.Get("/themes/{id}", s.getTheme)
	r.Delete("/themes/{id}", s.deleteTheme)
	r.Post("/themes/{id}/versions", s.saveTheme)
	r.Post("/themes/{id}/block", s.blockTheme)
	r.Get("/themes/{id}/assets/{index}", s.ownerThemeAsset)
	r.Head("/themes/{id}/assets/{index}", s.ownerThemeAsset)
	r.Get("/themes/{id}/archive", s.themeArchive)
	r.Post("/theme-uploads", s.themeUpload)
	r.Get("/rooms/{room}/appearance", s.roomAppearance)
	r.Post("/rooms/{room}/appearance", s.setAppearance)
	r.Get("/rooms/{room}/appearance/assets/{index}", s.roomThemeAsset)
	r.Head("/rooms/{room}/appearance/assets/{index}", s.roomThemeAsset)
}
func (s *Server) listThemes(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	cards, err := s.options.Themes.List(r.Context(), a)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"themes": cards})
}
func (s *Server) getTheme(w http.ResponseWriter, r *http.Request) {
	v, ok := s.ownerTheme(w, r)
	if ok {
		writeJSON(w, 200, v)
	}
}
func (s *Server) ownerTheme(w http.ResponseWriter, r *http.Request) (themes.Version, bool) {
	a, ok := s.identity(w, r)
	if !ok {
		return themes.Version{}, false
	}
	revision, err := queryInt(r, "revision", 0)
	if err != nil {
		appError(w, err)
		return themes.Version{}, false
	}
	v, err := s.options.Themes.Get(r.Context(), a, chi.URLParam(r, "id"), revision)
	if err != nil {
		appError(w, err)
		return v, false
	}
	return v, true
}
func (s *Server) saveTheme(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
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
	id := chi.URLParam(r, "id")
	if (id != "" && req.BaseRevision < 1) || (id == "" && req.BaseRevision != 0) {
		appError(w, themes.ErrInvalid)
		return
	}
	v, err := s.options.Themes.Attach(r.Context(), a, id, req.BaseRevision, req.UploadID)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) deleteTheme(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	if err := s.options.Themes.Delete(r.Context(), a, chi.URLParam(r, "id")); err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) blockTheme(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Blocked *bool  `json:"blocked"`
		Reason  string `json:"reason"`
	}
	if err := readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	if req.Blocked == nil {
		appError(w, themes.ErrInvalid)
		return
	}
	if err := s.options.Themes.Block(r.Context(), a, chi.URLParam(r, "id"), *req.Blocked, req.Reason); err != nil {
		appError(w, err)
		return
	}
	// Delivery rechecks immediately; the bounded background checker notifies UI.
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) themeUpload(w http.ResponseWriter, r *http.Request) {
	if s.options.Uploads == nil {
		appError(w, themes.ErrUnavailable)
		return
	}
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	u, err := s.options.Uploads.CreateTheme(r.Context(), a, req.Name, req.Size)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 201, u)
}
func themeVariant(r *http.Request, v themes.Version) (media.Variant, error) {
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil || index < 0 || index >= len(v.Prepared.Assets) {
		return media.Variant{}, themes.ErrNotFound
	}
	p := v.Prepared.Assets[index].Prepared
	switch r.URL.Query().Get("quality") {
	case "standard":
		return p.Standard, nil
	case "economy":
		return p.Economy, nil
	default:
		return media.Variant{}, themes.ErrInvalid
	}
}
func (s *Server) ownerThemeAsset(w http.ResponseWriter, r *http.Request) {
	v, ok := s.ownerTheme(w, r)
	if !ok {
		return
	}
	variant, err := themeVariant(r, v)
	if err != nil {
		appError(w, err)
		return
	}
	s.serveBlobAs(w, r, variant.Key, variant.MIME, "theme.png", "inline")
}
func (s *Server) themeArchive(w http.ResponseWriter, r *http.Request) {
	v, ok := s.ownerTheme(w, r)
	if !ok {
		return
	}
	s.serveBlob(w, r, v.Prepared.OriginalKey, "application/zip", "theme.zip")
}
func appearanceOf(state game.State) game.Appearance {
	if state.Appearance != nil {
		return *state.Appearance
	}
	return game.Appearance{Builtin: "classic"}
}
