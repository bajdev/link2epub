// Package build packages an extracted article as an EPUB.
package build

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"
	"time"

	epub "github.com/go-shiori/go-epub"
	"github.com/google/uuid"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/bajdev/link2epub/internal/assets"
	"github.com/bajdev/link2epub/internal/clean"
	"github.com/bajdev/link2epub/internal/extract"
)

//go:embed chapter.tmpl
var chapterTmpl string

//go:embed style.css
var styleCSS []byte

var chapter = template.Must(template.New("chapter").Parse(chapterTmpl))

// DisplayDate formats a publication date as shown in the byline and on the
// cover, or returns "" for the zero time.
func DisplayDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2 January 2006")
}

// Build returns the EPUB for a, whose cleaned content is body (from
// clean.Content) with images already processed into images. cover is a
// JPEG cover image, or nil for none.
func Build(a *extract.Article, body *html.Node, images []assets.Image, cover []byte) ([]byte, error) {
	// go-epub stages files in a temporary folder that it removes after each
	// write. Don't switch it to epub.MemoryFS: on Windows that loses every
	// file except mimetype, producing an empty book.
	e, err := epub.NewEpub(a.Title)
	if err != nil {
		return nil, err
	}
	// Derive the identifier from the URL, so rebuilding the same page gives
	// the same book identity and readers treat it as the same book.
	id := "urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(a.URL)).String()
	e.SetIdentifier(id)
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
	if cover != nil {
		p, err := e.AddImage("data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(cover), "cover.jpg")
		if err != nil {
			return nil, fmt.Errorf("adding cover: %w", err)
		}
		if err := e.SetCover(p, ""); err != nil {
			return nil, fmt.Errorf("adding cover: %w", err)
		}
	}
	if err := addImages(e, body, images); err != nil {
		return nil, err
	}
	headings := anchorHeadings(body)
	chapter, err := chapterBody(a, body)
	if err != nil {
		return nil, err
	}
	if _, err := e.AddSection(chapter, a.Title, chapterFile, css); err != nil {
		return nil, err
	}

	var raw bytes.Buffer
	if _, err := e.WriteTo(&raw); err != nil {
		return nil, err
	}
	return finish(raw.Bytes(), id, a, headings)
}

// addImages stores each image in the book and points the <img> elements,
// whose src is the image's filename, at the stored file.
func addImages(e *epub.Epub, body *html.Node, images []assets.Image) error {
	paths := map[string]string{}
	for _, img := range images {
		src := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		p, err := e.AddImage(src, img.Filename)
		if err != nil {
			return fmt.Errorf("adding image %s: %w", img.Filename, err)
		}
		paths[img.Filename] = p
	}
	// Only images processed by the assets package may be in the book; any
	// other <img> would point at a remote resource, so it is removed.
	var unknown []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Img {
			known := false
			for i, a := range n.Attr {
				if p, ok := paths[a.Val]; ok && a.Key == "src" {
					n.Attr[i].Val, known = p, true
				}
			}
			if !known {
				unknown = append(unknown, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)
	for _, n := range unknown {
		n.Parent.RemoveChild(n)
	}
	return nil
}

// chapterBody renders the chapter: a header with the title, byline and
// source link, followed by the article content.
func chapterBody(a *extract.Article, body *html.Node) (string, error) {
	content, err := clean.Render(body)
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
	if d := DisplayDate(a.Published); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, " · ")
}
