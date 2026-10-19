// Package build packages an extracted article as an EPUB.
package build

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"slices"
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

// Chapter is one article in a book: its metadata, and its content cleaned
// by clean.Content with images already processed.
type Chapter struct {
	Article *extract.Article
	Body    *html.Node
}

// Book is everything needed to package a book of one or more articles.
type Book struct {
	Title    string
	Chapters []Chapter
	Images   []assets.Image // every chapter's images; duplicates are stored once
	Cover    []byte         // a JPEG cover, or nil for none
	// Made is when the book was made: the dc:date of a book of several
	// articles. A single article uses its publication date instead.
	Made time.Time
}

// Build returns the EPUB for a, whose cleaned content is body (from
// clean.Content) with images already processed into images. cover is a
// JPEG cover image, or nil for none.
func Build(a *extract.Article, body *html.Node, images []assets.Image, cover []byte) ([]byte, error) {
	return BuildBook(Book{Title: a.Title, Chapters: []Chapter{{a, body}}, Images: images, Cover: cover})
}

// BuildBook returns the EPUB for b. A book of one article is laid out
// exactly as by Build; with several, each article is its own chapter and
// the table of contents lists them all.
func BuildBook(b Book) ([]byte, error) {
	if len(b.Chapters) == 0 {
		return nil, errors.New("a book needs at least one article")
	}
	m := metadata(b)

	// go-epub stages files in a temporary folder that it removes after each
	// write. Don't switch it to epub.MemoryFS: on Windows that loses every
	// file except mimetype, producing an empty book.
	e, err := epub.NewEpub(b.Title)
	if err != nil {
		return nil, err
	}
	e.SetIdentifier(m.id)
	e.SetLang(m.lang)
	if m.author != "" {
		e.SetAuthor(m.author)
	}
	if m.description != "" {
		e.SetDescription(m.description)
	}

	css, err := e.AddCSS("data:text/css;base64,"+base64.StdEncoding.EncodeToString(styleCSS), "style.css")
	if err != nil {
		return nil, fmt.Errorf("adding stylesheet: %w", err)
	}
	if b.Cover != nil {
		p, err := e.AddImage("data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(b.Cover), "cover.jpg")
		if err != nil {
			return nil, fmt.Errorf("adding cover: %w", err)
		}
		if err := e.SetCover(p, ""); err != nil {
			return nil, fmt.Errorf("adding cover: %w", err)
		}
	}
	paths, err := addImages(e, b.Images)
	if err != nil {
		return nil, err
	}

	var contents []tocChapter
	for i, ch := range b.Chapters {
		linkImages(ch.Body, paths)
		file := chapterFile
		if len(b.Chapters) > 1 {
			file = fmt.Sprintf("chapter-%02d.xhtml", i+1)
		}
		headings := anchorHeadings(ch.Body)
		chapter, err := chapterBody(ch.Article, ch.Body)
		if err != nil {
			return nil, err
		}
		if _, err := e.AddSection(chapter, ch.Article.Title, file, css); err != nil {
			return nil, err
		}
		contents = append(contents, tocChapter{title: ch.Article.Title, file: file, headings: headings})
	}

	var raw bytes.Buffer
	if _, err := e.WriteTo(&raw); err != nil {
		return nil, err
	}
	return finish(raw.Bytes(), m, contents)
}

// bookMeta is the book-level metadata.
type bookMeta struct {
	id, title, lang     string
	author, description string
	publisher           string
	sources             []string
	date                time.Time // zero when unknown
}

// maxAuthors is how many authors are named before a book of several
// articles is credited to "Various authors".
const maxAuthors = 3

// metadata works out the book's metadata. A single article's own metadata
// is used as it is; a book of several combines theirs.
func metadata(b Book) bookMeta {
	if len(b.Chapters) == 1 {
		a := b.Chapters[0].Article
		return bookMeta{
			id: identifier(a.URL), title: b.Title, lang: a.Language,
			author: a.Author, description: a.Description, publisher: a.SiteName,
			sources: []string{a.URL}, date: a.Published,
		}
	}
	m := bookMeta{title: b.Title, date: b.Made}
	var urls, authors, sites []string
	langs := map[string]int{}
	for _, ch := range b.Chapters {
		a := ch.Article
		urls = append(urls, a.URL)
		if a.Author != "" && !slices.Contains(authors, a.Author) {
			authors = append(authors, a.Author)
		}
		if !slices.Contains(sites, a.SiteName) {
			sites = append(sites, a.SiteName)
		}
		langs[a.Language]++
		// The most common language; ties go to the earliest article.
		if m.lang == "" || langs[a.Language] > langs[m.lang] {
			m.lang = a.Language
		}
	}
	m.id = identifier(strings.Join(urls, "\n"))
	m.sources = urls
	switch {
	case len(authors) > maxAuthors:
		m.author = "Various authors"
	case len(authors) > 0:
		m.author = joinNames(authors)
	}
	if len(sites) == 1 {
		m.publisher = sites[0] // all from one site
	}
	return m
}

// identifier derives the book's identifier from its URLs, so rebuilding
// the same page or list gives the same book identity and readers treat it
// as the same book.
func identifier(urls string) string {
	return "urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(urls)).String()
}

// joinNames lists names as "A", "A and B" or "A, B and C".
func joinNames(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// addImages stores each image in the book and returns where each filename
// was stored. An image already stored under the same filename, which is a
// content hash, is skipped.
func addImages(e *epub.Epub, images []assets.Image) (map[string]string, error) {
	paths := map[string]string{}
	for _, img := range images {
		if _, ok := paths[img.Filename]; ok {
			continue
		}
		src := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		p, err := e.AddImage(src, img.Filename)
		if err != nil {
			return nil, fmt.Errorf("adding image %s: %w", img.Filename, err)
		}
		paths[img.Filename] = p
	}
	return paths, nil
}

// linkImages points the <img> elements in body, whose src is the image's
// filename, at the stored files. Only images processed by the assets
// package may be in the book; any other <img> would point at a remote
// resource, so it is removed.
func linkImages(body *html.Node, paths map[string]string) {
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
