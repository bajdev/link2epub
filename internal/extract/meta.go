package extract

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// pageMeta is metadata the page declares about itself, used to fill in
// anything go-trafilatura didn't find, and for the cover image, which it
// doesn't look for reliably.
type pageMeta struct {
	Title, Author, SiteName, Description, Image string
	Published                                   time.Time
}

// articleTypes are the schema.org types whose JSON-LD describes an article.
var articleTypes = map[string]bool{
	"Article": true, "NewsArticle": true, "BlogPosting": true, "TechArticle": true,
	"ReportageNewsArticle": true, "AnalysisNewsArticle": true, "OpinionNewsArticle": true,
	"ScholarlyArticle": true, "Report": true,
}

// readPageMeta collects Open Graph, article: and Twitter meta tags and
// schema.org JSON-LD from doc. JSON-LD wins over meta tags, and meta tags
// over <title>. Relative image URLs are resolved against base.
func readPageMeta(doc *html.Node, base *url.URL) pageMeta {
	tags := map[string]string{} // first value of each property/name, lowercased key
	var title string
	var ld []map[string]any
	walk(doc, func(n *html.Node) {
		switch n.DataAtom {
		case atom.Meta:
			key := strings.ToLower(attr(n, "property"))
			if key == "" {
				key = strings.ToLower(attr(n, "name"))
			}
			if v := strings.TrimSpace(attr(n, "content")); key != "" && v != "" {
				if _, seen := tags[key]; !seen {
					tags[key] = v
				}
			}
		case atom.Title:
			if title == "" {
				title = strings.TrimSpace(textContent(n))
			}
		case atom.Script:
			if strings.Contains(strings.ToLower(attr(n, "type")), "ld+json") {
				ld = append(ld, articleObjects(textContent(n))...)
			}
		}
	})

	m := pageMeta{
		Title:       first(tags["og:title"], tags["twitter:title"], title),
		Author:      first(tags["article:author"], tags["author"], tags["twitter:creator"]),
		SiteName:    first(tags["og:site_name"], tags["application-name"]),
		Description: first(tags["og:description"], tags["description"], tags["twitter:description"]),
		Image:       first(tags["og:image"], tags["og:image:url"], tags["twitter:image"], tags["twitter:image:src"]),
		Published:   parseDate(first(tags["article:published_time"], tags["date"], tags["dc.date"])),
	}
	// An article:author that is a profile URL isn't a name.
	if strings.HasPrefix(m.Author, "http") {
		m.Author = ""
	}

	for _, o := range ld {
		if s := str(o["headline"]); s != "" {
			m.Title = s
		}
		if s := names(o["author"]); s != "" {
			m.Author = s
		}
		if s := names(o["publisher"]); s != "" && m.SiteName == "" {
			m.SiteName = s
		}
		if s := str(o["description"]); s != "" && m.Description == "" {
			m.Description = s
		}
		if s := imageURL(o["image"]); s != "" && m.Image == "" {
			m.Image = s
		}
		if t := parseDate(str(o["datePublished"])); !t.IsZero() {
			m.Published = t
		}
	}

	if m.Image != "" {
		if u, err := base.Parse(m.Image); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			m.Image = u.String()
		} else {
			m.Image = ""
		}
	}
	return m
}

// articleObjects returns the article objects in a JSON-LD script, looking
// inside arrays and @graph. Invalid JSON yields nothing.
func articleObjects(script string) []map[string]any {
	var v any
	if json.Unmarshal([]byte(script), &v) != nil {
		return nil
	}
	var out []map[string]any
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, e := range v {
				visit(e)
			}
		case map[string]any:
			if isArticle(v["@type"]) {
				out = append(out, v)
			}
			visit(v["@graph"])
		}
	}
	visit(v)
	return out
}

func isArticle(t any) bool {
	switch t := t.(type) {
	case string:
		return articleTypes[t]
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && articleTypes[s] {
				return true
			}
		}
	}
	return false
}

// names reads an author or publisher: a string, an object with a name, or
// a list of those, joined with ", ".
func names(v any) string {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		return str(v["name"])
	case []any:
		var out []string
		for _, e := range v {
			if s := names(e); s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, ", ")
	}
	return ""
}

// imageURL reads a schema.org image: a URL, an ImageObject, or a list.
func imageURL(v any) string {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		return first(str(v["url"]), str(v["contentUrl"]))
	case []any:
		for _, e := range v {
			if s := imageURL(e); s != "" {
				return s
			}
		}
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// parseDate accepts the ISO 8601 forms pages use for publication dates.
func parseDate(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func walk(n *html.Node, f func(*html.Node)) {
	if n.Type == html.ElementNode {
		f(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}
