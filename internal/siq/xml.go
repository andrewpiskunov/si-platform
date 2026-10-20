package siq

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"siplatform/internal/packs"
)

type node struct {
	name     string
	attrs    map[string]string
	text     strings.Builder
	children []*node
}

func (n *node) child(name string) *node {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return &node{attrs: map[string]string{}}
}
func (n *node) childrenNamed(name string) []*node {
	var out []*node
	for _, c := range n.children {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}
func (n *node) value() string { return n.text.String() }
func (n *node) present(name string) bool {
	for _, c := range n.children {
		if c.name == name {
			return true
		}
	}
	return false
}

func readXML(ctx context.Context, data []byte, l Limits) (*node, []packs.Issue, error) {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	var stack []*node
	var root *node
	count := 0
	var issues []packs.Issue
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("invalid XML: %w", err)
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, nil, errors.New("XML directives and DTD are forbidden")
		case xml.ProcInst:
			if t.Target != "xml" || root != nil {
				return nil, nil, errors.New("XML processing instructions are forbidden")
			}
		case xml.StartElement:
			count++
			if count > l.XMLNodes || len(stack) >= l.XMLDepth {
				return nil, nil, errors.New("XML complexity limit exceeded")
			}
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			if t.Name.Space != "" && t.Name.Space != "http://vladimirkhil.com/siq" && t.Name.Space != "https://github.com/VladimirKhil/SI/blob/master/assets/siq_5.xsd" {
				issues = append(issues, packs.Issue{Severity: "error", Code: "unsupported_namespace", Path: n.name, Message: "Неизвестное пространство имён XML"})
			}
			for _, a := range t.Attr {
				if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
					continue
				}
				if _, ok := n.attrs[a.Name.Local]; ok {
					return nil, nil, errors.New("duplicate XML attribute")
				}
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) == 0 {
				if root != nil || n.name != "package" {
					return nil, nil, errors.New("XML must contain one package root")
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, nil, errors.New("invalid XML nesting")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			} else if strings.TrimSpace(string(t)) != "" {
				return nil, nil, errors.New("text outside XML root")
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, nil, errors.New("incomplete XML")
	}
	inspectXML(root, "package", &issues)
	return root, issues, nil
}

// This grammar is deliberately explicit: unhandled elements/attributes block conversion instead of vanishing.
var elements = map[string]string{
	"package": "tags files info global rounds", "tags": "tag", "files": "file", "info": "authors sources comments showmanComments extension", "authors": "author", "sources": "source", "rounds": "round", "round": "info themes", "themes": "theme", "theme": "info questions", "questions": "question", "question": "info type scenario script params right wrong", "type": "param", "scenario": "atom", "script": "step", "step": "param", "params": "param", "param": "item param numberSet", "right": "answer", "wrong": "answer", "global": "Authors Sources", "Authors": "Author Name SecondName Surname Country City", "Sources": "Source Author Title Year Publish City", "Author": "Name SecondName Surname Country City", "Source": "Author Title Year Publish City",
}
var attributes = map[string]string{
	"package": "id name version restriction date publisher difficulty logo language generator contactUri", "file": "name hash", "round": "name type", "theme": "name", "question": "price type", "type": "name", "param": "name type", "atom": "type time", "step": "type", "item": "type isRef placement duration waitForFinish", "numberSet": "minimum maximum step", "Authors": "id", "Sources": "id", "Author": "id", "Source": "id",
}

func includes(list, value string) bool {
	for _, v := range strings.Fields(list) {
		if v == value {
			return true
		}
	}
	return false
}
func inspectXML(n *node, p string, issues *[]packs.Issue) {
	for attr := range n.attrs {
		if !includes(attributes[n.name], attr) {
			*issues = append(*issues, packs.Issue{Severity: "error", Code: "unsupported_attribute", Path: p + "/@" + attr, Message: "Неизвестный атрибут XML"})
		}
	}
	counts := map[string]int{}
	for _, c := range n.children {
		counts[c.name]++
		repeated := map[string]string{"tags": "tag", "files": "file", "authors": "author", "sources": "source", "rounds": "round", "themes": "theme", "questions": "question", "type": "param", "scenario": "atom", "script": "step", "step": "param", "params": "param", "param": "item param", "right": "answer", "wrong": "answer", "Authors": "Author", "Sources": "Source"}
		if counts[c.name] > 1 && !includes(repeated[n.name], c.name) {
			*issues = append(*issues, packs.Issue{Severity: "error", Code: "duplicate_element", Path: p + "/" + c.name, Message: "Однозначный элемент XML повторяется"})
		}
		cp := fmt.Sprintf("%s/%s[%d]", p, c.name, counts[c.name])
		if !includes(elements[n.name], c.name) {
			*issues = append(*issues, packs.Issue{Severity: "error", Code: "unsupported_xml", Path: cp, Message: "Неизвестный элемент XML"})
		}
		inspectXML(c, cp, issues)
	}
}
