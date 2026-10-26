package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Service) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(s.guard)
	r.Post("/register", s.registerHTTP)
	r.Post("/login", s.loginHTTP)
	r.Post("/verify-email", s.verifyEmailHTTP)
	r.Post("/forgot-password", s.forgotPasswordHTTP)
	r.Post("/reset-password", s.resetPasswordHTTP)
	r.Group(func(r chi.Router) {
		r.Use(s.requireSession)
		r.Post("/logout", s.logoutHTTP)
		r.Get("/me", s.meHTTP)
		r.Post("/password", s.passwordHTTP)
		r.Get("/export", s.exportHTTP)
		r.Delete("/me", s.deleteHTTP)
		r.Post("/tokens", s.createTokenHTTP)
		r.Get("/tokens", s.listTokensHTTP)
		r.Delete("/tokens/{id}", s.revokeTokenHTTP)
	})
	return r
}
func (s *Service) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !s.validOrigin(r) {
				writeError(w, ErrForbidden)
				return
			}
		}
		route := r.URL.Path
		if strings.HasSuffix(route, "/login") || strings.HasSuffix(route, "/register") || strings.HasSuffix(route, "/password") || strings.HasSuffix(route, "/verify-email") || strings.HasSuffix(route, "/forgot-password") || strings.HasSuffix(route, "/reset-password") || (r.Method == http.MethodDelete && strings.HasSuffix(route, "/me")) {
			if !s.admit(r) {
				w.Header().Set("Retry-After", "6")
				writeError(w, ErrRateLimited)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) validOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	} // CLI clients do not send browser Origin.
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	expected := s.options.Origin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	return origins[0] == expected
}

type identityKey struct{}

func (s *Service) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := s.FromRequest(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if identity.APIToken {
			writeError(w, ErrForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
	})
}
func identityFrom(r *http.Request) Identity { return r.Context().Value(identityKey{}).(Identity) }
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return ErrInvalidInput
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return ErrInvalidInput
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidInput
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, ErrUnauthorized):
		status, code = 401, "unauthorized"
	case errors.Is(err, ErrForbidden):
		status, code = 403, "forbidden"
	case errors.Is(err, ErrInvalidInput):
		status, code = 400, "invalid_input"
	case errors.Is(err, ErrUsernameTaken):
		status, code = 409, "username_taken"
	case errors.Is(err, ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, ErrRateLimited):
		status, code = 429, "rate_limited"
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func (s *Service) sessionCookie(w http.ResponseWriter, session Session) {
	http.SetCookie(w, &http.Cookie{Name: "si_session", Value: session.Token, Path: "/", HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: int(credentialTTL.Seconds())})
}
func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "si_session", Value: "", Path: "/", HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}
func (s *Service) registerHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Email != "" {
		if req.Username != "" {
			writeError(w, ErrInvalidInput)
			return
		}
		if err := s.RegisterEmail(r.Context(), req.Email, req.Password, req.DisplayName); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 202, map[string]bool{"email_sent": true})
		return
	}
	session, err := s.Register(r.Context(), req.Username, req.Password, req.DisplayName)
	if err != nil {
		writeError(w, err)
		return
	}
	s.sessionCookie(w, session)
	writeJSON(w, 201, session)
}
func (s *Service) loginHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	session, err := s.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	s.sessionCookie(w, session)
	writeJSON(w, 200, session)
}
func (s *Service) logoutHTTP(w http.ResponseWriter, r *http.Request) {
	token, _ := requestToken(r)
	if err := s.logout(r.Context(), token); err != nil {
		writeError(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) meHTTP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]User{"user": identityFrom(r).User})
}
func (s *Service) passwordHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	token, _ := requestToken(r)
	if err := s.changePassword(r.Context(), token, req.Current, req.New); err != nil {
		writeError(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) deleteHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	token, _ := requestToken(r)
	if err := s.deleteAccount(r.Context(), token, req.Password); err != nil {
		writeError(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) createTokenHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	token, _ := requestToken(r)
	out, err := s.createToken(r.Context(), token, req.Name, req.Scopes)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Service) listTokensHTTP(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.listTokens(r.Context(), identityFrom(r).User.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": tokens})
}
func (s *Service) revokeTokenHTTP(w http.ResponseWriter, r *http.Request) {
	token, _ := requestToken(r)
	if err := s.revokeToken(r.Context(), token, chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) exportHTTP(w http.ResponseWriter, r *http.Request) {
	identity := identityFrom(r)
	tokens, err := s.listTokens(r.Context(), identity.User.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	// Content exports belong to the application layer; this is account identity
	// and user-created token metadata only. Credentials never enter the payload.
	writeJSON(w, 200, map[string]any{"user": identity.User, "tokens": tokens})
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func (b *rateBucket) take(now time.Time, refill, burst float64) bool {
	if b.last.IsZero() {
		b.tokens = burst
	} else {
		b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*refill)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
func (s *Service) admit(r *http.Request) bool {
	// Forwarded headers are intentionally untrusted. A trusted reverse proxy may
	// set RemoteAddr itself; accepting client headers here would bypass limits.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(host) > 256 {
		return false
	}
	s.limitMu.Lock()
	defer s.limitMu.Unlock()
	now := time.Now()
	if !s.globalLimit.take(now, 4, 40) {
		return false
	}
	bucket := s.limits[host]
	if bucket == nil {
		if len(s.limits) >= 4096 {
			for key, value := range s.limits {
				if now.Sub(value.last) > 5*time.Minute {
					delete(s.limits, key)
				}
			}
			if len(s.limits) >= 4096 {
				return false
			}
		}
		bucket = &rateBucket{}
		s.limits[host] = bucket
	}
	return bucket.take(now, 1.0/6, 10)
}

func (s *Service) verifyEmailHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	session, err := s.VerifyEmail(r.Context(), req.Token)
	if err != nil {
		writeError(w, err)
		return
	}
	s.sessionCookie(w, session)
	writeJSON(w, 200, session)
}
func (s *Service) forgotPasswordHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.RequestReset(r.Context(), req.Email); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"email_sent": true})
}
func (s *Service) resetPasswordHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		writeError(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
