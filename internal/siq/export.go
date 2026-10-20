package siq

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"siplatform/internal/packs"
)

// Write emits a v5 archive. Callers must discard partial output on any error.
func Write(ctx context.Context, output io.Writer, doc packs.Document, assets []Asset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if issues := packs.Validate(doc); !packs.Playable(issues) {
		return fmt.Errorf("package has blocking compatibility diagnostics: %s", issues[0].Message)
	}
	limits := DefaultLimits()
	if len(assets)+1 > limits.Files {
		return errors.New("file count limit exceeded")
	}
	normalized := append([]Asset(nil), assets...)
	seen := map[string]bool{"content.xml": true}
	var total int64
	for i := range normalized {
		a := &normalized[i]
		key, err := canonicalName(a.Name)
		if err != nil {
			return err
		}
		a.Key = key
		if seen[key] || a.Open == nil || a.Size < 0 || a.Size > limits.MediaBytes {
			return errors.New("invalid export asset")
		}
		seen[key] = true
		total += a.Size
		if total > limits.UnpackedBytes {
			return errors.New("unpacked size limit exceeded")
		}
	}
	if issues := mediaIssues(doc, normalized); !packs.Playable(issues) {
		return errors.New("package references unavailable media")
	}
	root, err := encodeDocument(ctx, doc)
	if err != nil {
		return err
	}
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>` + "\n" + nodeXML(root))
	if int64(len(content)) > limits.XMLBytes || total+int64(len(content)) > limits.UnpackedBytes {
		return errors.New("export XML/total size limit exceeded")
	}
	// Validate the final XML too, including raw global metadata supplied by an editor.
	if _, issues, err := readXML(ctx, content, limits); err != nil {
		return err
	} else if !packs.Playable(issues) {
		return errors.New("unsupported export XML")
	}
	zw := zip.NewWriter(&boundedWriter{writer: output, remaining: limits.ArchiveBytes})
	w, err := zw.Create("content.xml")
	if err != nil {
		return err
	}
	if _, err = w.Write(content); err != nil {
		return err
	}
	for _, a := range normalized {
		if err := ctx.Err(); err != nil {
			return err
		}
		dst, err := zw.Create(a.Name)
		if err != nil {
			return err
		}
		src, err := a.Open()
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(contextReader{ctx, src}, a.Size+1))
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != a.Size {
			return errors.New("asset changed size during export")
		}
		if a.SHA256 != "" && a.SHA256 != hex.EncodeToString(hash.Sum(nil)) {
			return errors.New("asset hash changed during export")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return zw.Close()
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("archive size limit exceeded")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func element(name string) *node            { return &node{name: name, attrs: map[string]string{}} }
func textElement(name, value string) *node { n := element(name); n.text.WriteString(value); return n }
func attr(n *node, name, value string) {
	if value != "" {
		n.attrs[name] = value
	}
}
func infoNode(i packs.Info) *node {
	n := element("info")
	if len(i.Authors) > 0 {
		a := element("authors")
		for _, value := range i.Authors {
			a.children = append(a.children, textElement("author", value))
		}
		n.children = append(n.children, a)
	}
	if len(i.Sources) > 0 {
		s := element("sources")
		for _, value := range i.Sources {
			s.children = append(s.children, textElement("source", value))
		}
		n.children = append(n.children, s)
	}
	for _, p := range []struct{ name, value string }{{"comments", i.Comments}, {"showmanComments", i.ShowmanComments}, {"extension", i.Extension}} {
		if p.value != "" {
			n.children = append(n.children, textElement(p.name, p.value))
		}
	}
	return n
}
func encodeDocument(ctx context.Context, d packs.Document) (*node, error) {
	root := element("package")
	root.attrs = map[string]string{"name": d.Name, "version": "5.0", "xmlns": "https://github.com/VladimirKhil/SI/blob/master/assets/siq_5.xsd"}
	for _, p := range []struct{ name, value string }{{"id", d.ID}, {"date", d.Date}, {"publisher", d.Publisher}, {"language", d.Language}, {"restriction", d.Restriction}, {"logo", d.Logo}, {"contactUri", d.ContactURI}, {"generator", d.Generator}} {
		attr(root, p.name, p.value)
	}
	if d.Difficulty != 0 {
		attr(root, "difficulty", strconv.Itoa(d.Difficulty))
	}
	root.children = append(root.children, infoNode(d.Info))
	if len(d.Tags) > 0 {
		tags := element("tags")
		for _, t := range d.Tags {
			tags.children = append(tags.children, textElement("tag", t))
		}
		root.children = append(root.children, tags)
	}
	if d.GlobalXML != "" {
		wrapped := []byte(`<package name="global" version="5.0">` + d.GlobalXML + `</package>`)
		if len(wrapped) > 10<<20 {
			return nil, errors.New("global metadata too large")
		}
		n, issues, err := readXML(ctx, wrapped, DefaultLimits())
		if err != nil {
			return nil, err
		}
		if !packs.Playable(issues) || len(n.children) != 1 || n.children[0].name != "global" {
			return nil, errors.New("invalid global metadata")
		}
		root.children = append(root.children, n.children[0])
	}
	rounds := element("rounds")
	root.children = append(root.children, rounds)
	for _, r := range d.Rounds {
		rn := element("round")
		attr(rn, "name", r.Name)
		attr(rn, "type", r.Type)
		rn.children = append(rn.children, infoNode(r.Info))
		rounds.children = append(rounds.children, rn)
		themes := element("themes")
		rn.children = append(rn.children, themes)
		for _, t := range r.Themes {
			tn := element("theme")
			attr(tn, "name", t.Name)
			tn.children = append(tn.children, infoNode(t.Info))
			themes.children = append(themes.children, tn)
			questions := element("questions")
			tn.children = append(tn.children, questions)
			for _, q := range t.Questions {
				qn := element("question")
				qn.attrs["price"] = strconv.Itoa(q.Price)
				attr(qn, "type", q.Type)
				qn.children = append(qn.children, infoNode(q.Info))
				questions.children = append(questions.children, qn)
				params := element("params")
				for _, p := range q.Params {
					params.children = append(params.children, parameterNode(p))
				}
				qn.children = append(qn.children, params)
				right := element("right")
				for _, a := range q.Right {
					right.children = append(right.children, textElement("answer", a))
				}
				qn.children = append(qn.children, right)
				if len(q.Wrong) > 0 {
					wrong := element("wrong")
					for _, a := range q.Wrong {
						wrong.children = append(wrong.children, textElement("answer", a))
					}
					qn.children = append(qn.children, wrong)
				}
			}
		}
	}
	return root, nil
}
func parameterNode(p packs.Parameter) *node {
	n := textElement("param", p.Value)
	attr(n, "name", p.Name)
	attr(n, "type", p.Type)
	for _, c := range p.Items {
		item := textElement("item", c.Value)
		attr(item, "type", c.Type)
		attr(item, "placement", c.Placement)
		if c.IsRef {
			attr(item, "isRef", "true")
		}
		if c.WaitForFinish {
			attr(item, "waitForFinish", "true")
		}
		if c.DurationMS > 0 {
			ms := c.DurationMS
			duration := fmt.Sprintf("%02d:%02d:%02d", ms/3600000, (ms/60000)%60, (ms/1000)%60)
			if ms%1000 != 0 {
				duration += "." + strings.TrimRight(fmt.Sprintf("%03d", ms%1000), "0")
			}
			attr(item, "duration", duration)
		}
		n.children = append(n.children, item)
	}
	for _, child := range p.Params {
		n.children = append(n.children, parameterNode(child))
	}
	if p.NumberSet != nil {
		ns := textElement("numberSet", p.NumberSet.Value)
		if p.NumberSet.Minimum != nil {
			attr(ns, "minimum", strconv.Itoa(*p.NumberSet.Minimum))
		}
		if p.NumberSet.Maximum != nil {
			attr(ns, "maximum", strconv.Itoa(*p.NumberSet.Maximum))
		}
		if p.NumberSet.Step != nil {
			attr(ns, "step", strconv.Itoa(*p.NumberSet.Step))
		}
		n.children = append(n.children, ns)
	}
	return n
}
