// Package build packages an extracted article as an EPUB.
package build

import (
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	epub "github.com/go-shiori/go-epub"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/extract"
)

//go:embed chapter.tmpl
var chapterTmpl string

var chapter = template.Must(template.New("chapter").Parse(chapterTmpl))

// Build returns the EPUB for a. The result's WriteTo writes the book.
func Build(a *extract.Article) (*epub.Epub, error) {
	// go-epub stages files in a temporary folder that it removes after each
	// write. Don't switch it to epub.MemoryFS: on Windows that loses every
	// file except mimetype, producing an empty book.
	e, err := epub.NewEpub(a.Title)
	if err != nil {
		return nil, err
	}
	// Derive the identifier from the URL, so rebuilding the same page gives
	// the same book identity and readers treat it as the same book.
	e.SetIdentifier("urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(a.URL)).String())
	e.SetLang(a.Language)
	if a.Author != "" {
		e.SetAuthor(a.Author)
	}
	if a.Description != "" {
		e.SetDescription(a.Description)
	}

	body, err := chapterBody(a)
	if err != nil {
		return nil, err
	}
	if _, err := e.AddSection(body, a.Title, "chapter.xhtml", ""); err != nil {
		return nil, err
	}
	return e, nil
}

// chapterBody renders the chapter: a header with the title, byline and
// source link, followed by the article content as XHTML.
func chapterBody(a *extract.Article) (string, error) {
	content, err := renderChildren(a.Content)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = chapter.Execute(&b, struct {
		Title, Byline, URL string
		Content            template.HTML
	}{
		Title:   a.Title,
		Byline:  byline(a),
		URL:     a.URL,
		Content: template.HTML(content), // already serialized and escaped by html.Render
	})
	if err != nil {
		return "", fmt.Errorf("rendering chapter: %w", err)
	}
	return b.String(), nil
}

// byline joins the author, site name and publication date, skipping any
// that are unknown.
func byline(a *extract.Article) string {
	var parts []string
	if a.Author != "" {
		parts = append(parts, a.Author)
	}
	if a.SiteName != "" {
		parts = append(parts, a.SiteName)
	}
	if !a.Published.IsZero() {
		parts = append(parts, a.Published.Format("2 January 2006"))
	}
	return strings.Join(parts, " · ")
}

// renderChildren serializes the children of n. html.Render closes void
// elements (<br/>), quotes attributes and escapes text, which keeps the
// result well-formed XHTML for the markup go-trafilatura produces.
func renderChildren(n *html.Node) (string, error) {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}
