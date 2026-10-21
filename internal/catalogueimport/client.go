// Package catalogueimport uploads an authorized local manifest without fetching external URLs.
package catalogueimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"siplatform/internal/ingest"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ingest.CatalogueInput
	File string `json:"file"`
}
type Manifest struct {
	Items []Item `json:"items"`
}
type Result struct {
	Namespace  string `json:"namespace"`
	ExternalID string `json:"external_id"`
	UploadID   string `json:"upload_id,omitempty"`
	DraftID    string `json:"draft_id,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

func readManifest(ctx context.Context, name string) (*os.Root, Manifest, error) {
	var m Manifest
	if e := ctx.Err(); e != nil {
		return nil, m, e
	}
	f, e := openRegular(nil, name)
	if e != nil {
		return nil, m, errors.New("manifest_unavailable")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(contextReader{ctx, f}, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return nil, m, errors.New("manifest_too_large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return nil, m, errors.New("invalid_manifest")
	}
	if e = d.Decode(new(any)); e != io.EOF || len(m.Items) < 1 || len(m.Items) > 100 {
		return nil, m, errors.New("invalid_manifest")
	}
	root, e := os.OpenRoot(filepath.Dir(name))
	if e != nil {
		return nil, m, errors.New("manifest_directory_unavailable")
	}
	keys := map[string]bool{}
	for i := range m.Items {
		if e := ctx.Err(); e != nil {
			root.Close()
			return nil, m, e
		}
		v := &m.Items[i]
		v.Name = filepath.Base(v.File)
		f, e := openRegular(root, v.File)
		if e != nil {
			root.Close()
			return nil, m, fmt.Errorf("item_%d_file_unavailable", i+1)
		}
		stat, e := f.Stat()
		if e != nil || !stat.Mode().IsRegular() {
			f.Close()
			root.Close()
			return nil, m, fmt.Errorf("item_%d_invalid_file", i+1)
		}
		v.Size = stat.Size()
		if e = v.Validate(); e == nil {
			e = verify(ctx, f, *v)
		}
		f.Close()
		key := v.Namespace + "\x00" + v.ExternalID
		if e != nil || keys[key] {
			root.Close()
			return nil, m, fmt.Errorf("item_%d_invalid_metadata_or_checksum", i+1)
		}
		keys[key] = true
	}
	return root, m, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	return r.reader.Read(p)
}
func verify(ctx context.Context, f *os.File, v Item) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	stat, e := f.Stat()
	if e != nil || !stat.Mode().IsRegular() || stat.Size() != v.Size {
		return errors.New("file_changed")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(contextReader{ctx, f}, v.Size+1))
	if e != nil || n != v.Size || hex.EncodeToString(h.Sum(nil)) != v.SHA256 {
		return errors.New("checksum_mismatch")
	}
	_, e = f.Seek(0, io.SeekStart)
	return e
}

// Check validates every local archive before any network mutation.
func Check(ctx context.Context, name string) (int, error) {
	r, m, e := readManifest(ctx, name)
	if e != nil {
		return 0, e
	}
	defer r.Close()
	return len(m.Items), nil
}
func endpoint(base string) (string, error) {
	u, e := url.Parse(base)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid_api_url")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("https_required")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

type client struct {
	base, token string
	http        *http.Client
}

func (c client) call(ctx context.Context, method, path string, body []byte, offset *int64) (ingest.Upload, error) {
	var u ingest.Upload
	r, e := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if e != nil {
		return u, errors.New("request_invalid")
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	if offset != nil {
		r.Header.Set("Upload-Offset", strconv.FormatInt(*offset, 10))
		r.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, e := c.http.Do(r)
	if e != nil {
		return u, errors.New("network_or_timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return u, fmt.Errorf("http_%d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if e != nil || len(raw) > 64<<10 {
		return u, errors.New("invalid_response")
	}
	if e = json.Unmarshal(raw, &u); e != nil {
		return u, errors.New("invalid_response")
	}
	if len(u.ID) != 32 || strings.Trim(u.ID, "0123456789abcdef") != "" {
		return u, errors.New("invalid_upload_id")
	}
	return u, nil
}
func (c client) item(ctx context.Context, root *os.Root, v Item) (ingest.Upload, error) {
	f, e := openRegular(root, v.File)
	if e != nil {
		return ingest.Upload{}, errors.New("file_unavailable")
	}
	defer f.Close()
	if e = verify(ctx, f, v); e != nil {
		return ingest.Upload{}, errors.New("file_changed")
	}
	raw, _ := json.Marshal(v.CatalogueInput)
	u, e := c.call(ctx, "POST", "/api/catalogue-imports", raw, nil)
	if e != nil {
		return u, e
	}
	path := "/api/uploads/" + u.ID
	if u.Size != v.Size || u.Offset < 0 || u.Offset > v.Size {
		return u, errors.New("invalid_server_offset")
	}
	if u.Status == "uploading" {
		if _, e = f.Seek(u.Offset, io.SeekStart); e != nil {
			return u, errors.New("file_unavailable")
		}
		for u.Offset < v.Size {
			n := min(int64(4<<20), v.Size-u.Offset)
			chunk := make([]byte, n)
			if _, e = io.ReadFull(contextReader{ctx, f}, chunk); e != nil {
				return u, errors.New("file_changed")
			}
			before := u.Offset
			next, err := c.call(ctx, "PATCH", path, chunk, &before)
			if err != nil {
				return u, err
			}
			if next.ID != u.ID || next.Offset != before+n || next.Size != v.Size {
				return u, errors.New("invalid_server_offset")
			}
			u = next
		}
		u, e = c.call(ctx, "POST", path+"/complete", nil, nil)
		if e != nil {
			return u, e
		}
	}
	for u.Status == "queued" || u.Status == "processing" {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return u, errors.New("timeout")
		case <-timer.C:
		}
		u, e = c.call(ctx, "GET", path, nil, nil)
		if e != nil {
			return u, e
		}
	}
	if u.Status == "failed" {
		return u, errors.New("processing_failed")
	}
	if u.Status != "ready" || u.DraftID == "" {
		return u, errors.New("invalid_response")
	}
	return u, nil
}

// Run is sequential and repeatable. Redirects are never followed with credentials.
func Run(ctx context.Context, name, base, token string) ([]Result, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	base, e := endpoint(base)
	if e != nil {
		return nil, e
	}
	if len(token) < 1 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("missing_or_invalid_token")
	}
	root, m, e := readManifest(ctx, name)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	c := client{base: base, token: token, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	out := make([]Result, 0, len(m.Items))
	failed := false
	for _, v := range m.Items {
		if ctx.Err() != nil {
			failed = true
			out = append(out, Result{Namespace: v.Namespace, ExternalID: v.ExternalID, Status: "not_attempted", Error: "cancelled_or_timeout"})
			continue
		}
		u, e := c.item(ctx, root, v)
		r := Result{Namespace: v.Namespace, ExternalID: v.ExternalID, UploadID: u.ID, DraftID: u.DraftID, Status: u.Status}
		if e != nil {
			failed = true
			r.Status = "failed"
			r.Error = e.Error()
			if u.Status == "failed" {
				r.Error = "processing_failed"
			}
		}
		out = append(out, r)
	}
	if failed {
		return out, errors.New("batch_incomplete: inspect report and rerun unchanged manifest for uncertain network outcomes")
	}
	return out, nil
}
