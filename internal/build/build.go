// Package build packages an extracted article as an EPUB.
package build

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	epub "github.com/go-shiori/go-epub"
	"github.com/google/uuid"

	"github.com/bajdev/link2epub/internal/clean"
	"github.com/bajdev/link2epub/internal/extract"
)

//go:embed chapter.tmpl
var chapterTmpl string

//go:embed style.css
var styleCSS []byte

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

	css, err := e.AddCSS("data:text/css;base64,"+base64.StdEncoding.EncodeToString(styleCSS), "style.css")
	if err != nil {
		return nil, fmt.Errorf("adding stylesheet: %w", err)
	}
	body, err := chapterBody(a)
	if err != nil {
		return nil, err
	}
	if _, err := e.AddSection(body, a.Title, "chapter.xhtml", css); err != nil {
		return nil, err
	}
	return e, nil
}

// chapterBody renders the chapter: a header with the title, byline and
// source link, followed by the cleaned article content.
func chapterBody(a *extract.Article) (string, error) {
	base, err := url.Parse(a.URL)
	if err != nil {
		return "", err
	}
	content, err := clean.Content(a.Content, base)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = chapter.Execute(&b, struct {
		Lang, Title, Byline, URL string
		Content                  template.HTML
	}{
		Lang:    a.Language,
		Title:   a.Title,
		Byline:  byline(a),
		URL:     a.URL,
		Content: template.HTML(content), // sanitized and checked by clean.Content
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
