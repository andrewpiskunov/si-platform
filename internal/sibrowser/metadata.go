package sibrowser

import (
	"context"
	"golang.org/x/net/html"
	"io"
	"net/url"
	"siplatform/internal/library"
	"strconv"
	"strings"
)

// ownWalk excludes recommendation articles, scripts and SVG text.
func ownWalk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Data != "article" && c.Data != "script" && c.Data != "style" && c.Data != "svg" {
			ownWalk(c, fn)
		}
	}
}
func cleanText(n *html.Node) string {
	var b strings.Builder
	ownWalk(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
func links(n *html.Node) []library.SourceLink {
	out := []library.SourceLink{}
	ownWalk(n, func(c *html.Node) {
		if c.Data == "a" {
			u, e := url.Parse(attr(c, "href"))
			if e != nil {
				return
			}
			base, _ := url.Parse(Origin)
			raw := base.ResolveReference(u).String()
			name := cleanText(c)
			if name != "" && library.SafeSourceLink(raw) {
				out = append(out, library.SourceLink{Name: name, URL: raw})
			}
		}
	})
	return out
}
func parseMetadata(r io.Reader, id string) (library.SourceMetadata, error) {
	var m library.SourceMetadata
	doc, e := html.Parse(r)
	if e != nil {
		return m, fault("invalid_metadata", false)
	}
	var main *html.Node
	walk(doc, func(n *html.Node) {
		if n.Data != "article" || main != nil {
			return
		}
		ownWalk(n, func(c *html.Node) {
			if c.Data == "a" && attr(c, "href") == "/packages/"+id+"/direct_download" {
				main = n
			}
		})
	})
	if main == nil {
		return m, fault("metadata_markup_changed", true)
	}
	stats := false
	roundNames := map[string]string{}
	ownWalk(main, func(n *html.Node) {
		prop := attr(n, "itemprop")
		text := cleanText(n)
		switch {
		case n.Data == "h1" && strings.Contains(prop, "headline"):
			m.Name = text
		case prop == "alternateName":
			m.Filename = text
		case prop == "contentSize":
			m.FileSizeLabel = text
		case n.Data == "time" && prop == "datePublished":
			m.PublishedDate = attr(n, "datetime")
		case n.Data == "img" && prop == "image":
			raw := Origin + attr(n, "src")
			if library.ValidSourceCover(id, raw) {
				m.CoverURL = raw
			}
		case n.Data == "a" && strings.HasPrefix(attr(n, "href"), "/authors/"):
			m.Authors = append(m.Authors, links(n)...)
		case n.Data == "a" && strings.HasPrefix(attr(n, "href"), "/tags/"):
			m.Tags = append(m.Tags, links(n)...)
		case attr(n, "data-packages--download_link--component-target") == "count":
			if v, e := strconv.ParseInt(strings.ReplaceAll(text, " ", ""), 10, 64); e == nil {
				m.Downloads = &v
			}
		case n.Data == "p" && strings.Contains(" "+attr(n, "class")+" ", " pt-2 "):
			m.Description = append(m.Description, text)
			m.Links = append(m.Links, links(n)...)
		case n.Data == "table" && strings.Contains(text, "Оригинальное сообщение"):
			m.Originals = links(n)
		case n.Data == "label" && strings.HasPrefix(attr(n, "for"), "round-tab-"):
			roundNames[strings.TrimPrefix(attr(n, "for"), "round-tab-")] = text
		case n.Data == "table" && !stats && strings.Contains(text, "Категории"):
			stats = true
			category := false
			ownWalk(n, func(tr *html.Node) {
				if tr.Data != "tr" {
					return
				}
				cells := []string{}
				for c := tr.FirstChild; c != nil; c = c.NextSibling {
					if c.Data == "td" {
						cells = append(cells, cleanText(c))
					}
				}
				if len(cells) == 1 {
					if strings.Contains(cells[0], "вопрос") {
						v, e := strconv.Atoi(strings.Fields(cells[0])[0])
						if e == nil {
							m.QuestionCount = &v
						}
					}
					if cells[0] == "Категории" {
						category = true
					}
					return
				}
				for i := 0; i+1 < len(cells); i += 2 {
					v, e := strconv.Atoi(strings.TrimSuffix(cells[i+1], "%"))
					if e == nil && strings.HasSuffix(cells[i+1], "%") {
						p := library.SourcePercent{Name: cells[i], Percent: v}
						if category {
							m.Categories = append(m.Categories, p)
						} else {
							m.Media = append(m.Media, p)
						}
					}
				}
			})
		}
	})
	ownWalk(main, func(n *html.Node) {
		if !strings.HasPrefix(attr(n, "id"), "round-content-") {
			return
		}
		round := library.SourceRound{Name: roundNames[strings.TrimPrefix(attr(n, "id"), "round-content-")]}
		ownWalk(n, func(d *html.Node) {
			if d.Data != "details" {
				return
			}
			theme := library.SourceTheme{}
			ownWalk(d, func(c *html.Node) {
				if c.Data == "summary" {
					theme.Name = cleanText(c)
				}
				if c.Data == "li" {
					theme.Questions = append(theme.Questions, cleanText(c))
				}
			})
			round.Themes = append(round.Themes, theme)
		})
		m.Rounds = append(m.Rounds, round)
	})
	if !stats || m.Validate(id) != nil {
		return m, fault("metadata_markup_changed", true)
	}
	return m, nil
}
func (s *Source) Metadata(ctx context.Context, id string) (library.SourceMetadata, error) {
	if !packagePath.MatchString("/packages/" + id) {
		return library.SourceMetadata{}, fault("invalid_source_id", false)
	}
	resp, e := s.get(ctx, "/packages/"+id)
	if e != nil {
		return library.SourceMetadata{}, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if e != nil || len(b) > 4<<20 {
		return library.SourceMetadata{}, fault("metadata_too_large", true)
	}
	return parseMetadata(strings.NewReader(string(b)), id)
}
func (s *Source) Cover(ctx context.Context, id, raw string) ([]byte, error) {
	if raw == "" || !library.ValidSourceCover(id, raw) {
		return nil, fault("invalid_cover_url", false)
	}
	resp, e := s.get(ctx, "/packages/"+id+"/logo.webp")
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, (5<<20)+1))
	if e != nil {
		return nil, fault("cover_read", true)
	}
	if len(b) == 0 || len(b) > 5<<20 {
		return nil, fault("cover_too_large", false)
	}
	return b, nil
}
