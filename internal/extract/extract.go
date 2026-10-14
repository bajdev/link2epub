// Package extract finds the readable article in a web page, along with its
// metadata.
package extract

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	trafilatura "github.com/markusmobius/go-trafilatura"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// minTextLen is the shortest article text, in characters, worth turning
// into a book. Anything shorter is usually a JavaScript shell, an error page
// or a cookie wall.
const minTextLen = 200

// ErrNoContent reports that no readable article was found.
var ErrNoContent = errors.New("no readable article content found (the page may need JavaScript, which isn't supported yet)")

// Article is the extracted article.
type Article struct {
	URL         string // final page URL
	Title       string
	Author      string
	SiteName    string
	Description string
	Language    string // BCP 47 code, "en" when unknown
	Published   time.Time
	Content     *html.Node // the article body; its children are the content
}

// Extract finds the article in html, which was downloaded from pageURL.
func Extract(htmlText string, pageURL *url.URL) (*Article, error) {
	res, err := trafilatura.Extract(strings.NewReader(htmlText), trafilatura.Options{
		OriginalURL:     pageURL,
		EnableFallback:  true,
		IncludeLinks:    true,
		ExcludeComments: true,
	})
	if err != nil || res.ContentNode == nil ||
		utf8.RuneCountInString(strings.TrimSpace(res.ContentText)) < minTextLen {
		return nil, ErrNoContent
	}

	m := res.Metadata
	a := &Article{
		URL:         pageURL.String(),
		Title:       strings.TrimSpace(m.Title),
		Author:      strings.TrimSpace(m.Author),
		SiteName:    strings.TrimSpace(m.Sitename),
		Description: strings.TrimSpace(m.Description),
		Language:    strings.TrimSpace(m.Language),
		Published:   m.Date,
		Content:     res.ContentNode,
	}
	if a.Title == "" {
		a.Title = pageURL.Hostname()
	}
	if a.Language == "" {
		a.Language = "en"
	}
	removeLeadingTitle(a.Content, a.Title)
	return a, nil
}

// removeLeadingTitle drops the first element of the content when it is a
// heading repeating the title, which the book already shows.
func removeLeadingTitle(content *html.Node, title string) {
	first := content.FirstChild
	for first != nil && first.Type == html.TextNode && strings.TrimSpace(first.Data) == "" {
		first = first.NextSibling
	}
	if first == nil || first.Type != html.ElementNode {
		return
	}
	switch first.DataAtom {
	case atom.H1, atom.H2:
		if normalize(textContent(first)) == normalize(title) {
			content.RemoveChild(first)
		}
	}
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// normalize lowercases s and collapses whitespace, for comparing titles.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
