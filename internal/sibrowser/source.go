// Package sibrowser discovers source packages and synchronizes their immutable imports.
package sibrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const Origin = "https://www.sibrowser.ru"
const maxArchiveBytes int64 = 250 << 20

type Item struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Author    string `json:"author"`
	SourceURL string `json:"source_url"`
}
type Page struct {
	Items []Item
	Next  int
}
type Fault struct {
	Code      string
	Retryable bool
	Delay     time.Duration
}

func (e *Fault) Error() string            { return e.Code }
func fault(code string, retry bool) error { return &Fault{Code: code, Retryable: retry} }

type Source struct {
	base       string
	http       *http.Client
	interval   time.Duration
	maxArchive int64
}

func NewSource(interval time.Duration) *Source {
	if interval < time.Second {
		interval = time.Second
	}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, DialContext: publicDial}
	c := &http.Client{Transport: &pacedTransport{base: tr, interval: interval}, Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("redirect_limit")
		}
		return allowedURL(r.URL.String())
	}}
	return &Source{base: Origin, http: c, interval: interval, maxArchive: maxArchiveBytes}
}

type pacedTransport struct {
	base     http.RoundTripper
	interval time.Duration
	last     time.Time
}

// The source is used sequentially by the runner, including redirects.
func (p *pacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if e := allowedURL(r.URL.String()); e != nil {
		return nil, e
	}
	if e := wait(r.Context(), time.Until(p.last.Add(p.interval))); e != nil {
		return nil, e
	}
	p.last = time.Now()
	return p.base.RoundTrip(r)
}
func allowedURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return errors.New("download_url_denied")
	}
	h := strings.ToLower(u.Hostname())
	if h == "www.sibrowser.ru" || h == "sibrowser.ru" || h == "vk.com" || h == "vk.ru" || strings.HasSuffix(h, ".userapi.com") || strings.HasSuffix(h, ".vk.com") || strings.HasSuffix(h, ".vk.ru") {
		return nil
	}
	return errors.New("download_host_denied")
}
func publicIP(raw string) bool {
	ip, e := netip.ParseAddr(raw)
	if e != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, errors.New("invalid_address")
	}
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil || len(ips) == 0 {
		return nil, errors.New("dns_failed")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP.String()) {
			return nil, errors.New("private_address_denied")
		}
	}
	d := net.Dialer{Timeout: 20 * time.Second}
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("connection_failed")
}
func wait(ctx context.Context, d time.Duration) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func retryAfter(s string) time.Duration {
	if n, e := strconv.ParseInt(s, 10, 32); e == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(s); e == nil {
		return max(0, time.Until(t))
	}
	return 0
}
func (s *Source) get(ctx context.Context, path string) (*http.Response, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		req, e := http.NewRequestWithContext(ctx, "GET", s.base+path, nil)
		if e != nil {
			return nil, fault("invalid_source_url", false)
		}
		req.Header.Set("User-Agent", "SIPlatform-SIBrowserSync/1.0")
		resp, e := s.http.Do(req)
		if e == nil && resp.StatusCode == 200 {
			return resp, nil
		}
		code := "source_network"
		retry := true
		delay := time.Second * time.Duration(1<<attempt)
		if resp != nil {
			resp.Body.Close()
			code = fmt.Sprintf("source_http_%d", resp.StatusCode)
			retry = resp.StatusCode == 429 || resp.StatusCode >= 500
			delay = max(delay, retryAfter(resp.Header.Get("Retry-After")))
		}
		if !retry || attempt == 2 || delay > time.Minute {
			return nil, &Fault{Code: code, Retryable: retry, Delay: delay}
		}
		if e = wait(ctx, delay); e != nil {
			return nil, e
		}
	}
	return nil, fault("source_network", true)
}
func (s *Source) Page(ctx context.Context, page int) (Page, error) {
	if page < 1 {
		return Page{}, fault("invalid_page", false)
	}
	resp, e := s.get(ctx, "/?page="+strconv.Itoa(page))
	if e != nil {
		return Page{}, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if e != nil {
		return Page{}, fault("source_read", true)
	}
	if len(b) > 4<<20 {
		return Page{}, fault("listing_too_large", false)
	}
	return parsePage(strings.NewReader(string(b)), page)
}

var packagePath = regexp.MustCompile(`^/packages/([1-9][0-9]{0,17})$`)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
func parsePage(r io.Reader, current int) (Page, error) {
	doc, e := html.Parse(r)
	if e != nil {
		return Page{}, fault("invalid_listing", false)
	}
	var list, pager *html.Node
	walk(doc, func(n *html.Node) {
		if attr(n, "id") == "packages" {
			list = n
		}
		if attr(n, "id") == "pagination" {
			pager = n
		}
	})
	if list == nil || pager == nil {
		return Page{}, fault("listing_markup_changed", false)
	}
	p := Page{}
	seen := map[string]bool{}
	invalid := false
	walk(list, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "article" {
			return
		}
		item := Item{}
		walk(n, func(c *html.Node) {
			if c.Data != "a" {
				return
			}
			href := attr(c, "href")
			if m := packagePath.FindStringSubmatch(href); m != nil {
				if item.ID != "" && item.ID != m[1] {
					invalid = true
				}
				item.ID = m[1]
				if c.Parent != nil && (c.Parent.Data == "h1" || c.Parent.Data == "h2") {
					item.Name = nodeText(c)
				}
			}
			if strings.HasPrefix(href, "/authors/") {
				item.Author = nodeText(c)
			}
		})
		if item.ID == "" || item.Name == "" {
			invalid = true
			return
		}
		if item.Author == "" {
			item.Author = "Автор не указан"
		}
		if len(item.Name) > 4000 || len(item.Author) > 1000 {
			invalid = true
			return
		}
		item.SourceURL = Origin + "/packages/" + item.ID
		if !seen[item.ID] {
			seen[item.ID] = true
			p.Items = append(p.Items, item)
		}
	})
	walk(pager, func(n *html.Node) {
		if n.Data != "a" || !strings.Contains(" "+attr(n, "rel")+" ", " next ") {
			return
		}
		u, e := url.Parse(attr(n, "href"))
		if e != nil || u.IsAbs() || u.Host != "" || u.Path != "/" || u.Fragment != "" || len(u.Query()) != 1 {
			invalid = true
			return
		}
		v, e := strconv.Atoi(u.Query().Get("page"))
		if e != nil || v != current+1 || p.Next != 0 {
			invalid = true
			return
		}
		p.Next = v
	})
	if invalid || len(p.Items) == 0 {
		return Page{}, fault("listing_markup_changed", false)
	}
	return p, nil
}
func (s *Source) Download(ctx context.Context, item Item, dir string) (name, sha string, size int64, err error) {
	if !packagePath.MatchString("/packages/" + item.ID) {
		return "", "", 0, fault("invalid_source_id", false)
	}
	resp, e := s.get(ctx, "/packages/"+item.ID+"/direct_download")
	if e != nil {
		return "", "", 0, e
	}
	defer resp.Body.Close()
	limit := s.maxArchive
	if limit <= 0 {
		limit = maxArchiveBytes
	}
	if resp.ContentLength > limit {
		return "", "", 0, fault("archive_too_large", false)
	}
	f, e := os.CreateTemp(dir, "sibrowser-*.siq")
	if e != nil {
		return "", "", 0, fault("scratch_unavailable", true)
	}
	name = f.Name()
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(name)
		}
	}()
	head := make([]byte, 4)
	if _, e = io.ReadFull(resp.Body, head); e != nil || string(head) != "PK\x03\x04" {
		return name, "", 0, fault("download_not_siq", false)
	}
	h := sha256.New()
	w := io.MultiWriter(f, h)
	if _, e = w.Write(head); e != nil {
		return name, "", 0, fault("scratch_unavailable", true)
	}
	n, e := io.Copy(w, io.LimitReader(resp.Body, limit-4+1))
	if e != nil {
		return name, "", 0, fault("download_interrupted", true)
	}
	size = n + 4
	if size > limit {
		return name, "", 0, fault("archive_too_large", false)
	}
	if e = f.Sync(); e != nil {
		return name, "", 0, fault("scratch_unavailable", true)
	}
	if e = f.Close(); e != nil {
		return name, "", 0, fault("scratch_unavailable", true)
	}
	return name, hex.EncodeToString(h.Sum(nil)), size, nil
}
