package sibrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const pageHTML = `<div data-controller="packages_paginated--component"><div id="pagination"><a rel="next" href="/?page=2">Next</a></div><div id="packages"><article><h1><a href="/packages/123">Quiz</a></h1><a href="/authors/alice"><span>Alice</span></a><a href="/packages/123/direct_download">Download</a></article></div></div><article><a href="/packages/999">Related</a></article>`

func TestParseMainListAndPagination(t *testing.T) {
	p, e := parsePage(strings.NewReader(pageHTML), 1)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Items) != 1 || p.Items[0].ID != "123" || p.Items[0].Author != "Alice" || p.Next != 2 {
		t.Fatalf("%+v", p)
	}
	for _, html := range []string{`<html>login</html>`, strings.Replace(pageHTML, `/?page=2`, `https://evil.test/?page=2`, 1), strings.Replace(pageHTML, `/?page=2`, `/?page=1`, 1), strings.Replace(pageHTML, `id="packages"`, `id="related"`, 1)} {
		if _, e := parsePage(strings.NewReader(html), 1); e == nil {
			t.Fatal("accepted invalid listing", html)
		}
	}
	p, e = parsePage(strings.NewReader(strings.Replace(pageHTML, `<a rel="next" href="/?page=2">Next</a>`, "", 1)), 2)
	if e != nil || p.Next != 0 {
		t.Fatal(p, e)
	}
}
func TestDownloadRejectsHTMLAndLimitsAndRetries(t *testing.T) {
	calls := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("PK\x03\x04test archive"))
	}))
	defer h.Close()
	s := &Source{base: h.URL, http: h.Client(), interval: time.Millisecond, maxArchive: 100}
	file, sha, size, e := s.Download(context.Background(), Item{ID: "123"}, t.TempDir())
	if e != nil || len(sha) != 64 || size != 16 || calls != 2 {
		t.Fatal(file, sha, size, calls, e)
	}
	if _, e = os.Stat(file); e != nil {
		t.Fatal(e)
	}
	s.maxArchive = 5
	if _, _, _, e = s.Download(context.Background(), Item{ID: "123"}, t.TempDir()); e == nil {
		t.Fatal("oversize accepted")
	}
	h.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>Login</html>")) })
	if _, _, _, e = s.Download(context.Background(), Item{ID: "123"}, t.TempDir()); e == nil {
		t.Fatal("HTML accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Page(ctx, 1); e == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestExternalURLPolicy(t *testing.T) {
	for _, u := range []string{"http://vk.com/a", "https://127.0.0.1/a", "https://vk.com.evil.test/a", "https://user:pw@vk.com/a", "https://vk.com:8443/a"} {
		if allowedURL(u) == nil {
			t.Fatal(u)
		}
	}
	for _, u := range []string{"https://www.sibrowser.ru/packages/1/direct_download", "https://vk.com/doc123", "https://psv4.userapi.com/file"} {
		if e := allowedURL(u); e != nil {
			t.Fatal(u, e)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.2", "169.254.169.254", "::1", "fc00::1", "100.64.0.1", "192.0.0.1"} {
		if publicIP(ip) {
			t.Fatal(ip)
		}
	}
}
