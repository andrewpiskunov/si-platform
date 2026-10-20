package siq

import (
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"siplatform/internal/packs"
)

func parseXML(ctx context.Context, data []byte, l Limits) (packs.Document, error) {
	root, issues, err := readXML(ctx, data, l)
	if err != nil {
		return packs.Document{}, err
	}
	a := root.attrs
	d := packs.Document{ID: a["id"], Name: a["name"], Version: "5.0", SourceVersion: a["version"], Date: a["date"], Publisher: a["publisher"], Language: a["language"], Restriction: a["restriction"], Logo: a["logo"], ContactURI: a["contactUri"], Generator: a["generator"], Info: parseInfo(root.child("info")), ImportIssues: issues}
	if a["version"] != "4" && a["version"] != "4.0" && a["version"] != "5" && a["version"] != "5.0" {
		d.ImportIssues = append(d.ImportIssues, packs.Issue{Severity: "error", Code: "unsupported_version", Path: "package/@version", Message: "Поддерживаются SIQ 4 и 5"})
	}
	if a["difficulty"] != "" {
		d.Difficulty, err = strconv.Atoi(a["difficulty"])
		if err != nil {
			return d, fmt.Errorf("invalid difficulty: %w", err)
		}
	}
	for _, n := range root.child("tags").childrenNamed("tag") {
		d.Tags = append(d.Tags, n.value())
	}
	if root.present("global") && len(root.child("global").children) > 0 {
		d.GlobalXML = nodeXML(root.child("global"))
	}
	for _, f := range root.child("files").childrenNamed("file") {
		d.Files = append(d.Files, packs.FileHash{Name: f.attrs["name"], Hash: f.attrs["hash"]})
	}
	for ri, rn := range root.child("rounds").childrenNamed("round") {
		r := packs.Round{ID: fmt.Sprintf("r%d", ri+1), Name: rn.attrs["name"], Type: rn.attrs["type"], Info: parseInfo(rn.child("info"))}
		for ti, tn := range rn.child("themes").childrenNamed("theme") {
			t := packs.Theme{ID: fmt.Sprintf("%s-t%d", r.ID, ti+1), Name: tn.attrs["name"], Info: parseInfo(tn.child("info"))}
			for qi, qn := range tn.child("questions").childrenNamed("question") {
				q, err := parseQuestion(qn, fmt.Sprintf("%s-q%d", t.ID, qi+1))
				if err != nil {
					return d, err
				}
				t.Questions = append(t.Questions, q)
			}
			r.Themes = append(r.Themes, t)
		}
		d.Rounds = append(d.Rounds, r)
	}
	return d, nil
}
func parseInfo(n *node) packs.Info {
	i := packs.Info{Comments: n.child("comments").value(), ShowmanComments: n.child("showmanComments").value(), Extension: n.child("extension").value()}
	for _, a := range n.child("authors").childrenNamed("author") {
		i.Authors = append(i.Authors, a.value())
	}
	for _, s := range n.child("sources").childrenNamed("source") {
		i.Sources = append(i.Sources, s.value())
	}
	return i
}
func parseQuestion(n *node, id string) (packs.Question, error) {
	q := packs.Question{ID: id, Type: n.attrs["type"], Info: parseInfo(n.child("info")), HasScript: n.present("script")}
	price, err := strconv.Atoi(n.attrs["price"])
	if err != nil {
		return q, fmt.Errorf("%s: invalid question price", id)
	}
	q.Price = price
	for _, a := range n.child("right").childrenNamed("answer") {
		q.Right = append(q.Right, a.value())
	}
	for _, a := range n.child("wrong").childrenNamed("answer") {
		q.Wrong = append(q.Wrong, a.value())
	}
	if n.present("params") {
		for _, pn := range n.child("params").childrenNamed("param") {
			p, err := parseParameter(pn)
			if err != nil {
				return q, err
			}
			q.Params = append(q.Params, p)
		}
		if n.present("scenario") || n.present("type") {
			return q, fmt.Errorf("%s: ambiguous legacy and modern question representations", id)
		}
	} else {
		if err := upgradeLegacy(&q, n); err != nil {
			return q, err
		}
	}
	return q, nil
}
func parseParameter(n *node) (packs.Parameter, error) {
	p := packs.Parameter{Name: n.attrs["name"], Type: n.attrs["type"], Value: n.value()}
	if len(n.children) > 0 && strings.TrimSpace(p.Value) == "" {
		p.Value = ""
	}
	for _, c := range n.childrenNamed("item") {
		item := packs.Content{Type: c.attrs["type"], Value: c.value(), Placement: c.attrs["placement"]}
		if item.Type == "" {
			item.Type = "text"
		}
		var err error
		item.IsRef, err = parseBool(c.attrs["isRef"])
		if err != nil {
			return p, err
		}
		item.WaitForFinish, err = parseBool(c.attrs["waitForFinish"])
		if err != nil {
			return p, err
		}
		item.DurationMS, err = parseDuration(c.attrs["duration"])
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, item)
	}
	for _, c := range n.childrenNamed("param") {
		child, err := parseParameter(c)
		if err != nil {
			return p, err
		}
		p.Params = append(p.Params, child)
	}
	if n.present("numberSet") {
		ns := n.child("numberSet")
		set := &packs.NumberSet{Value: strings.TrimSpace(ns.value())}
		var err error
		set.Minimum, err = optionalInt(ns.attrs["minimum"])
		if err != nil {
			return p, err
		}
		set.Maximum, err = optionalInt(ns.attrs["maximum"])
		if err != nil {
			return p, err
		}
		set.Step, err = optionalInt(ns.attrs["step"])
		if err != nil {
			return p, err
		}
		p.NumberSet = set
	}
	return p, nil
}
func optionalInt(s string) (*int, error) {
	if s == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(s)
	return &v, err
}
func parseBool(s string) (bool, error) {
	if s == "" {
		return false, nil
	}
	switch s {
	case "true", "True", "1":
		return true, nil
	case "false", "False", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean: %s", s)
	}
}
func parseDuration(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid duration: %s", s)
	}
	h, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || h < 0 || h > 24 {
		return 0, fmt.Errorf("invalid duration hours")
	}
	m, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid duration minutes")
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= 60 {
		return 0, fmt.Errorf("invalid duration seconds")
	}
	return (h*3600+m*60)*1000 + int64(math.Round(seconds*1000)), nil
}

var numberRange = regexp.MustCompile(`^\[(-?\d+);(-?\d+)\](?:/(\d+))?$`)

func legacyPrice(value string) (*packs.NumberSet, error) {
	if value == "" {
		return &packs.NumberSet{}, nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		return &packs.NumberSet{Minimum: &n, Maximum: &n}, nil
	}
	m := numberRange.FindStringSubmatch(value)
	if m == nil {
		return nil, fmt.Errorf("unsupported legacy price: %s", value)
	}
	lo, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, err
	}
	hi, err := strconv.Atoi(m[2])
	if err != nil {
		return nil, err
	}
	// Legacy ranges without /step mean the two endpoints, unlike a native
	// numeric step=0, which permits every integer in the interval.
	gap := 0
	if hi >= lo {
		if lo < 0 && hi > int(^uint(0)>>1)+lo {
			return nil, fmt.Errorf("legacy price range overflow")
		}
		gap = hi - lo
	}
	step := &gap
	if m[3] != "" {
		step, err = optionalInt(m[3])
		if err != nil {
			return nil, err
		}
	}
	return &packs.NumberSet{Minimum: &lo, Maximum: &hi, Step: step}, nil
}
func upgradeLegacy(q *packs.Question, n *node) error {
	old := n.child("type")
	kind := old.attrs["name"]
	if kind == "" {
		kind = q.Type
	}
	values := map[string]string{}
	for _, p := range old.childrenNamed("param") {
		if _, exists := values[p.attrs["name"]]; exists {
			return fmt.Errorf("duplicate legacy parameter")
		}
		values[p.attrs["name"]] = p.value()
	}
	q.Type = kind
	switch kind {
	case "simple":
		q.Type = ""
	case "auction":
		q.Type = "stake"
	case "sponsored":
		q.Type = "noRisk"
	case "cat", "bagcat":
		q.Type = "secret"
		switch values["knows"] {
		case "", "after":
		case "before":
			q.Type = "secretPublicPrice"
		case "never":
			q.Type = "secretNoQuestion"
		default:
			return fmt.Errorf("unknown legacy secret visibility")
		}
		mode := "exceptCurrent"
		switch values["self"] {
		case "", "false":
		case "true":
			mode = "any"
		default:
			return fmt.Errorf("unknown legacy secret recipient rule")
		}
		price, err := legacyPrice(values["cost"])
		if err != nil {
			return err
		}
		q.Params = append(q.Params, packs.Parameter{Name: "theme", Value: values["theme"]}, packs.Parameter{Name: "price", Type: "numberSet", NumberSet: price}, packs.Parameter{Name: "selectionMode", Value: mode})
	}
	// Carry unknown legacy params into validation, so no unhandled parameter disappears.
	for _, p := range old.childrenNamed("param") {
		if (kind == "cat" || kind == "bagcat") && includes("theme cost self knows", p.attrs["name"]) {
			continue
		}
		q.Params = append(q.Params, packs.Parameter{Name: p.attrs["name"], Value: p.value()})
	}
	body := packs.Parameter{Name: "question", Type: "content"}
	answer := packs.Parameter{Name: "answer", Type: "content"}
	current := &body
	for _, atom := range n.child("scenario").childrenNamed("atom") {
		kind := atom.attrs["type"]
		if kind == "marker" {
			current = &answer
			continue
		}
		if kind == "" {
			kind = "text"
		}
		item := packs.Content{Type: kind, Value: atom.value(), Placement: "screen"}
		if kind == "say" {
			item.Type = "text"
			item.Placement = "replic"
		}
		if kind == "voice" {
			item.Type = "audio"
			item.Placement = "background"
		}
		if strings.HasPrefix(item.Value, "@") {
			item.IsRef = true
			item.Value = strings.TrimPrefix(item.Value, "@")
		}
		ts := atom.attrs["time"]
		if ts == "" {
			ts = "0"
		}
		if ts != "-1" {
			v, err := strconv.ParseFloat(ts, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 600 {
				return fmt.Errorf("invalid legacy content duration")
			}
			item.DurationMS = int64(math.Round(v * 1000))
			item.WaitForFinish = true
		}
		current.Items = append(current.Items, item)
	}
	q.Params = append(q.Params, body)
	if len(answer.Items) > 0 {
		q.Params = append(q.Params, answer)
	}
	return nil
}
func mediaIssues(doc packs.Document, assets []Asset) []packs.Issue {
	available := map[string]bool{}
	for _, a := range assets {
		available[a.Key] = true
	}
	var issues []packs.Issue
	if doc.Logo != "" {
		if !strings.HasPrefix(doc.Logo, "@") {
			issues = append(issues, packs.Issue{Severity: "error", Code: "external_media", Path: "logo", Message: "Логотип должен ссылаться на локальное изображение"})
		} else {
			key, err := canonicalName("Images/" + strings.TrimPrefix(doc.Logo, "@"))
			if err != nil || !available[key] {
				issues = append(issues, packs.Issue{Severity: "error", Code: "missing_media", Path: "logo", Message: "Файл логотипа отсутствует"})
			}
		}
	}
	var check func(packs.Parameter, string)
	check = func(p packs.Parameter, prefix string) {
		for i, c := range p.Items {
			if c.Type == "text" {
				continue
			}
			loc := fmt.Sprintf("%s.items[%d]", prefix, i)
			if !c.IsRef {
				issues = append(issues, packs.Issue{Severity: "error", Code: "external_media", Path: loc, Message: "Внешнее медиа нужно заменить проверенной локальной копией"})
				continue
			}
			folder := map[string]string{"image": "Images/", "audio": "Audio/", "video": "Video/"}[c.Type]
			key, err := canonicalName(folder + c.Value)
			if err != nil || !available[key] {
				issues = append(issues, packs.Issue{Severity: "error", Code: "missing_media", Path: loc, Message: "Файл медиа отсутствует или имеет недопустимое имя"})
			}
		}
		for _, child := range p.Params {
			check(child, prefix+"."+child.Name)
		}
	}
	for _, r := range doc.Rounds {
		for _, t := range r.Themes {
			for _, q := range t.Questions {
				for _, p := range q.Params {
					check(p, q.ID+".params."+p.Name)
				}
			}
		}
	}
	return issues
}
func nodeXML(n *node) string {
	var b strings.Builder
	e := xml.NewEncoder(&b)
	var walk func(*node)
	walk = func(n *node) {
		start := xml.StartElement{Name: xml.Name{Local: n.name}}
		keys := make([]string, 0, len(n.attrs))
		for key := range n.attrs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: key}, Value: n.attrs[key]})
		}
		_ = e.EncodeToken(start)
		_ = e.EncodeToken(xml.CharData(n.value()))
		for _, c := range n.children {
			walk(c)
		}
		_ = e.EncodeToken(start.End())
	}
	walk(n)
	_ = e.Flush()
	return b.String()
}
