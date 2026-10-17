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
	"golang.org/x/text/language"
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
	ImageURL    string     // the page's preview image, for the cover; may be empty
	Content     *html.Node // the article body; its children are the content
}

// Extract finds the article in doc, a parsed page downloaded from pageURL.
// It may modify doc.
func Extract(doc *html.Node, pageURL *url.URL) (*Article, error) {
	// Read the page's own metadata first: extraction modifies doc.
	meta := readPageMeta(doc, pageURL)

	res, err := trafilatura.ExtractDocument(doc, trafilatura.Options{
		OriginalURL:     pageURL,
		EnableFallback:  true,
		IncludeLinks:    true,
		IncludeImages:   true,
		ExcludeComments: true,
		// The default date search also scans body text and invents dates
		// from stray numbers (an image width of 2000 became 1 January 2000).
		// Fast mode only trusts metadata, URLs and date-like markup.
		HtmlDateMode: trafilatura.Fast,
	})
	if err != nil || res.ContentNode == nil ||
		utf8.RuneCountInString(strings.TrimSpace(res.ContentText)) < minTextLen {
		return nil, ErrNoContent
	}

	// go-trafilatura's metadata comes first; the page's own declarations
	// fill the gaps.
	m := res.Metadata
	a := &Article{
		URL:         pageURL.String(),
		Title:       first(m.Title, meta.Title, pageURL.Hostname()),
		Author:      first(m.Author, meta.Author),
		SiteName:    first(m.Sitename, meta.SiteName),
		Description: first(m.Description, meta.Description),
		Language:    languageTag(m.Language),
		Published:   m.Date,
		ImageURL:    meta.Image,
		Content:     res.ContentNode,
	}
	if a.Published.IsZero() {
		a.Published = meta.Published
	}
	removeLeadingTitle(a.Content, a.Title)
	return a, nil
}

// languageTag returns s as a well-formed BCP 47 tag ("en_US" → "en-US"),
// or "en" when s is missing or invalid.
func languageTag(s string) string {
	tag, err := language.Parse(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"))
	if err != nil || tag == language.Und {
		return "en"
	}
	return tag.String()
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
	// Match on the tag name: go-trafilatura creates some output elements
	// without setting DataAtom.
	switch first.Data {
	case "h1", "h2":
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
