package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/blob"
	"siplatform/internal/library"
	"siplatform/internal/siq"
)

func (s *Server) exportDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	rev, e := queryInt(r, "revision", 0)
	if e != nil {
		appError(w, e)
		return
	}
	d, e := s.options.Library.ForDownload(r.Context(), a, chi.URLParam(r, "id"), rev)
	if e != nil {
		appError(w, e)
		return
	}
	s.export(w, r, d)
}
func (s *Server) publicDownload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.optionalIdentity(w, r)
	if !ok {
		return
	}
	d, e := s.options.Library.ForDownload(r.Context(), a, chi.URLParam(r, "id"), 0)
	if e != nil {
		appError(w, e)
		return
	}
	s.export(w, r, d)
}
func (s *Server) export(w http.ResponseWriter, r *http.Request, d library.Draft) {
	select {
	case s.exports <- struct{}{}:
		defer func() { <-s.exports }()
	default:
		appError(w, library.ErrQuota)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	// Stage on disk before sending headers: unsupported exports never become a
	// truncated successful download, and large archives do not occupy RAM.
	f, e := os.CreateTemp("", "si-export-*")
	if e != nil {
		appError(w, e)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	assets := make([]siq.Asset, 0, len(d.Assets))
	for _, a := range d.Assets {
		assets = append(assets, siq.Asset{Name: a.Name, Size: a.Size, SHA256: a.Key, Open: func() (io.ReadCloser, error) { return s.options.Objects.Get(r.Context(), a.Key, 0, -1) }})
	}
	if e = siq.Write(r.Context(), f, d.Document, assets); e != nil {
		appError(w, library.ErrInvalid)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="package.siq"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "package.siq", time.Time{}, f)
}
func (s *Server) originalDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	d, e := s.options.Library.ForFile(r.Context(), a, chi.URLParam(r, "id"), 0, true)
	if e != nil {
		appError(w, e)
		return
	}
	if d.OriginalKey == "" {
		appError(w, library.ErrNotFound)
		return
	}
	s.serveBlob(w, r, d.OriginalKey, "application/octet-stream", "original.siq")
}
func (s *Server) draftAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	rev, e := queryInt(r, "revision", 0)
	if e != nil {
		appError(w, e)
		return
	}
	d, e := s.options.Library.ForFile(r.Context(), a, chi.URLParam(r, "id"), rev, false)
	if e != nil {
		appError(w, e)
		return
	}
	key := chi.URLParam(r, "key")
	for _, asset := range d.Assets {
		if asset.Key == key {
			s.serveBlob(w, r, key, safeMIME(asset.ContentType), "asset")
			return
		}
	}
	appError(w, library.ErrNotFound)
}
func safeMIME(value string) string {
	switch value {
	case "image/png", "image/jpeg", "image/webp", "audio/mpeg", "audio/wav", "audio/ogg", "video/mp4", "video/webm":
		return value
	default:
		return "application/octet-stream"
	}
}
func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, key, contentType, name string) {
	if s.options.Objects == nil {
		appError(w, errors.New("storage unavailable"))
		return
	}
	m, e := s.options.Objects.Stat(r.Context(), key)
	if e != nil {
		if errors.Is(e, blob.ErrNotFound) {
			e = library.ErrNotFound
		}
		appError(w, e)
		return
	}
	reader := &blobSeeker{ctx: r.Context(), store: s.options.Objects, key: key, size: m.Size}
	defer reader.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", strconv.Quote(key))
	http.ServeContent(w, r, name, time.Time{}, reader)
}

// ServeContent implements Range/conditional responses against this bounded
// seekable adapter. Only the requested object tail is opened and streamed.
type blobSeeker struct {
	ctx       context.Context
	store     blob.Store
	key       string
	size, pos int64
	reader    io.ReadCloser
}

func (b *blobSeeker) Close() error {
	if b.reader != nil {
		e := b.reader.Close()
		b.reader = nil
		return e
	}
	return nil
}
func (b *blobSeeker) Read(p []byte) (int, error) {
	if b.pos >= b.size {
		return 0, io.EOF
	}
	if b.reader == nil {
		r, e := b.store.Get(b.ctx, b.key, b.pos, -1)
		if e != nil {
			return 0, e
		}
		b.reader = r
	}
	n, e := b.reader.Read(p)
	b.pos += int64(n)
	return n, e
}
func (b *blobSeeker) Seek(offset int64, whence int) (int64, error) {
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = b.pos
	case io.SeekEnd:
		base = b.size
	default:
		return b.pos, blob.ErrInvalidRange
	}
	if offset < -base || offset > b.size-base {
		return b.pos, blob.ErrInvalidRange
	}
	if e := b.Close(); e != nil {
		return b.pos, e
	}
	b.pos = base + offset
	return b.pos, nil
}
