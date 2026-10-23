package sibrowser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"strconv"
	"strings"
	"time"
)

type apiClient struct {
	base, token string
	http        *http.Client
}

func newAPI(base, token string, insecure bool) (*apiClient, error) {
	u, e := url.Parse(base)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, ErrConfig
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && (insecure || loopback)) {
		return nil, ErrConfig
	}
	return &apiClient{strings.TrimSuffix(base, "/"), token, &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *apiClient) call(ctx context.Context, method, path string, raw []byte, offset *int64, out any) error {
	req, e := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(raw))
	if e != nil {
		return fault("api_request_invalid", false)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	if offset != nil {
		req.Header.Set("Upload-Offset", strconv.FormatInt(*offset, 10))
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return fault("api_network", true)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 403 {
			var body struct {
				Error string `json:"error"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) == nil && body.Error == "blocked" {
				return fault("api_blocked", false)
			}
		}
		return &Fault{Code: fmt.Sprintf("api_http_%d", resp.StatusCode), Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500, Delay: retryAfter(resp.Header.Get("Retry-After"))}
	}
	raw, e = io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, out) != nil {
		return fault("api_invalid_response", true)
	}
	return nil
}
func validUpload(u ingest.Upload, in ingest.ExternalInput) bool {
	return len(u.ID) == 32 && strings.Trim(u.ID, "0123456789abcdef") == "" && u.Size == in.Size && u.Offset >= 0 && u.Offset <= in.Size
}
func (c *apiClient) begin(ctx context.Context, in ingest.ExternalInput) (ingest.Upload, error) {
	var u ingest.Upload
	raw, _ := json.Marshal(in)
	e := c.call(ctx, "POST", "/api/external-imports/sibrowser", raw, nil, &u)
	var f *Fault
	if errors.As(e, &f) {
		if f.Code == "api_http_404" || f.Code == "api_blocked" {
			return u, fault("receipt_suppressed", false)
		}
		if f.Code == "api_http_409" {
			return u, fault("source_receipt_conflict", false)
		}
	}
	if e == nil && !validUpload(u, in) {
		e = fault("api_invalid_upload", true)
	}
	return u, e
}
func (c *apiClient) upload(ctx context.Context, name string, in ingest.ExternalInput, u ingest.Upload) (string, error) {
	if !validUpload(u, in) {
		return "", fault("api_invalid_upload", true)
	}
	if u.Status == "uploading" {
		f, e := os.Open(name)
		if e != nil {
			return "", fault("scratch_unavailable", true)
		}
		defer f.Close()
		stat, e := f.Stat()
		if e != nil || !stat.Mode().IsRegular() || stat.Size() != in.Size {
			return "", fault("local_file_changed", false)
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, in.Size+1))
		if e != nil || n != in.Size || hex.EncodeToString(h.Sum(nil)) != in.SHA256 {
			return "", fault("local_checksum_changed", false)
		}
		if _, e = f.Seek(u.Offset, 0); e != nil {
			return "", fault("scratch_unavailable", true)
		}
		for u.Offset < in.Size {
			chunk := make([]byte, min(int64(4<<20), in.Size-u.Offset))
			if _, e = io.ReadFull(f, chunk); e != nil {
				return "", fault("scratch_unavailable", true)
			}
			before := u.Offset
			oldID := u.ID
			if e = c.call(ctx, "PATCH", "/api/uploads/"+u.ID, chunk, &before, &u); e != nil {
				return "", uploadError(e)
			}
			if !validUpload(u, in) || u.ID != oldID || u.Offset != before+int64(len(chunk)) {
				return "", fault("api_invalid_offset", false)
			}
		}
		oldID := u.ID
		if e = c.call(ctx, "POST", "/api/uploads/"+u.ID+"/complete", nil, nil, &u); e != nil {
			return "", uploadError(e)
		}
		if u.ID != oldID {
			return "", fault("api_invalid_upload", false)
		}
	}
	for u.Status == "queued" || u.Status == "processing" {
		if e := wait(ctx, time.Second); e != nil {
			return "", uploadError(e)
		}
		oldID := u.ID
		if e := c.call(ctx, "GET", "/api/uploads/"+u.ID, nil, nil, &u); e != nil {
			return "", uploadError(e)
		}
		if u.ID != oldID {
			return "", fault("api_invalid_upload", false)
		}
	}
	if u.Status == "failed" {
		return "", fault("processing_"+safeCode(u.Error), u.Error == "expired" || u.Error == "staging_unavailable")
	}
	if !validUpload(u, in) || u.Status != "ready" || u.DraftID == "" {
		return "", fault("api_invalid_upload", true)
	}
	return u.DraftID, nil
}
func safeCode(s string) string {
	if s == "" || len(s) > 80 || strings.Trim(s, "abcdefghijklmnopqrstuvwxyz_0123456789") != "" {
		return "failed"
	}
	return s
}
func (c *apiClient) publish(ctx context.Context, id string) (string, error) {
	raw, _ := json.Marshal(map[string]string{"external_id": id})
	var card library.Card
	e := c.call(ctx, "POST", "/api/external-imports/sibrowser/publish", raw, nil, &card)
	if e != nil {
		var f *Fault
		if errors.As(e, &f) && (f.Code == "api_http_404" || f.Code == "api_http_409" || f.Code == "api_blocked") {
			return "", fault("publication_suppressed", false)
		}
		return "", e
	}
	if card.ID == "" || card.Status != "published" || card.Revision != 1 {
		return "", errors.New("api_invalid_publication")
	}
	return card.ID, nil
}
func (c *apiClient) retry(ctx context.Context, in ingest.ExternalInput) (ingest.Upload, error) {
	var u ingest.Upload
	raw, _ := json.Marshal(in)
	e := c.call(ctx, "POST", "/api/external-imports/sibrowser/retry", raw, nil, &u)
	if e == nil && !validUpload(u, in) {
		e = fault("api_invalid_upload", false)
	}
	return u, e
}

// Upload rows may expire between receipt lookup and chunk/status requests.
// These conflicts are transient and say nothing about moderation of a pack.
func uploadError(e error) error {
	var f *Fault
	if errors.As(e, &f) && (f.Code == "api_http_404" || f.Code == "api_http_409") {
		return fault("upload_interrupted", true)
	}
	return e
}
