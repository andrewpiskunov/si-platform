package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"siplatform/internal/accounts"
	"siplatform/internal/privacy"
)

func (s *Server) privacyIdentity(w http.ResponseWriter, r *http.Request) (accounts.Identity, bool) {
	if s.options.Accounts == nil {
		privacyError(w, r, privacy.ErrUnauthorized)
		return accounts.Identity{}, false
	}
	actor, err := s.options.Accounts.FromRequest(r)
	if err != nil {
		privacyError(w, r, err)
		return actor, false
	}
	if actor.APIToken {
		privacyError(w, r, privacy.ErrForbidden)
		return actor, false
	}
	return actor, true
}

func privacyQuery(r *http.Request, allowed ...string) (url.Values, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, privacy.ErrInvalid
	}
	for key, values := range query {
		known := false
		for _, name := range allowed {
			if name == key {
				known = true
				break
			}
		}
		if !known || len(values) != 1 || values[0] == "" {
			return nil, privacy.ErrInvalid
		}
	}
	return query, nil
}

func (s *Server) privacySubjects(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.privacyIdentity(w, r)
	if !ok {
		return
	}
	query, err := privacyQuery(r, "kind", "after")
	if err != nil {
		privacyError(w, r, err)
		return
	}
	page, err := privacy.New(s.store.Pool(), s.options.Objects).Subjects(r.Context(), actor, query.Get("kind"), query.Get("after"))
	if err != nil {
		privacyError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) privacyExport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.privacyIdentity(w, r)
	if !ok {
		return
	}
	query, err := privacyQuery(r, "kind", "id", "metadata_only")
	if err != nil {
		privacyError(w, r, err)
		return
	}
	metadata := query.Get("metadata_only")
	if metadata != "" && metadata != "true" && metadata != "false" {
		privacyError(w, r, privacy.ErrInvalid)
		return
	}
	select {
	case s.exports <- struct{}{}:
		defer func() { <-s.exports }()
	default:
		privacyError(w, r, privacy.ErrBusy)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	file, err := os.CreateTemp(s.options.ExportDirectory, "si-account-export-*")
	if err != nil {
		privacyError(w, r, privacy.ErrUnavailable)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	err = privacy.New(s.store.Pool(), s.options.Objects).Export(ctx, actor, privacy.Options{Kind: query.Get("kind"), ID: query.Get("id"), MetadataOnly: metadata == "true"}, file)
	if err != nil {
		privacyError(w, r, err)
		return
	}
	if ctx.Err() != nil {
		privacyError(w, r, privacy.ErrUnavailable)
		return
	}
	// Preparation is finished; the staged file no longer depends on a DB lease.
	cancel()
	// Bound slow downloads so they cannot retain both export slots indefinitely.
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(time.Hour))
	defer controller.SetWriteDeadline(time.Time{})
	// Never commit a successful response until the complete, authorized archive exists.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="account-data.zip"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A fresh archive has no stable validator; ignore conditional/range requests.
	// Retrying always revalidates credentials and creates a new snapshot.
	r = r.Clone(r.Context())
	r.Header.Del("Range")
	r.Header.Del("If-Range")
	r.Header.Del("If-Modified-Since")
	r.Header.Del("If-Unmodified-Since")
	r.Header.Del("If-None-Match")
	r.Header.Del("If-Match")
	http.ServeContent(w, r, "account-data.zip", time.Time{}, file)
}

var privacyErrorPage = template.Must(template.New("privacy-error").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Выгрузка данных</title><body><main><h1>Не удалось подготовить выгрузку</h1><p>{{.}}</p><p><a href="/account">Вернуться в аккаунт</a></p></main></body></html>`))

func privacyError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 503, "unavailable", "Не удалось подготовить архив. Попробуйте позже."
	switch {
	case errors.Is(err, privacy.ErrUnauthorized), errors.Is(err, accounts.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Войдите в аккаунт и повторите выгрузку."
	case errors.Is(err, privacy.ErrForbidden), errors.Is(err, accounts.ErrForbidden):
		status, code, message = 403, "forbidden", "Для выгрузки нужна действующая сессия владельца. Доступ к материалам мог измениться; обновите страницу."
	case errors.Is(err, privacy.ErrNotFound):
		status, code, message = 404, "not_found", "Выбранный материал недоступен в вашем аккаунте. Обновите список."
	case errors.Is(err, privacy.ErrInvalid):
		status, code, message = 400, "invalid_input", "Проверьте параметры выгрузки и выберите материал из списка в аккаунте."
	case errors.Is(err, privacy.ErrBusy):
		status, code, message = 429, "busy", "Выгрузка уже выполняется или сервер занят. Дождитесь завершения и попробуйте снова."
		w.Header().Set("Retry-After", "5")
	case errors.Is(err, privacy.ErrTooLarge):
		status, code, message = 413, "too_large", "Архив слишком большой. Выберите отдельный пакет, оформление или загрузку либо выгрузите только метаданные."
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.WriteHeader(status)
		_ = privacyErrorPage.Execute(w, message)
		return
	}
	writeJSON(w, status, map[string]string{"error": code})
}
