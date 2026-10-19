// Package pipeline turns a web page into a book: fetch, extract, clean,
// process images and build the EPUB.
package pipeline

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/assets"
	"github.com/bajdev/link2epub/internal/build"
	"github.com/bajdev/link2epub/internal/clean"
	"github.com/bajdev/link2epub/internal/cover"
	"github.com/bajdev/link2epub/internal/device"
	"github.com/bajdev/link2epub/internal/extract"
	"github.com/bajdev/link2epub/internal/fetch"
	"github.com/bajdev/link2epub/internal/kepub"
)

// Errors that Run wraps, so callers can choose an exit code.
var (
	ErrFetch     = errors.New("couldn't fetch the page")
	ErrNoContent = extract.ErrNoContent
)

// Book is a finished book, ready to be saved.
type Book struct {
	// Title is the article title, used to name the file for folder targets.
	Title string
	// Content writes the complete EPUB.
	Content io.WriterTo
}

// Options adjust a conversion.
type Options struct {
	// BeforeImages, if set, is called with the book title once it is known
	// and before any image is downloaded. An error stops the conversion and
	// is returned unchanged. The CLI uses it to stop early when the output
	// file already exists. It is only called for a book of one article
	// without Title; otherwise the title is known before anything is
	// fetched.
	BeforeImages func(title string) error
	// Warn, if set, receives problems that don't stop the conversion, such
	// as an image that couldn't be downloaded or a URL that was skipped.
	Warn func(msg string)
	// Progress, if set, receives a line for each article of a book of
	// several, and a summary at the end.
	Progress func(msg string)
	// Title is the book's title. With one article it replaces the article's
	// own title as the book title; a book of several articles without one
	// is called ListTitle.
	Title string
	// KEPUB converts the finished book to a Kobo KEPUB.
	KEPUB bool
}

// ListTitle is the title of a book of several articles made at t, when no
// title is given.
func ListTitle(t time.Time) string {
	return "Reading list, " + build.DisplayDate(t)
}

// Run converts the pages at urls into one book using the default settings.
// One URL makes a book of that article; several make a book with one
// chapter per article, in order, skipping any that fail. Images and
// redirects may lead to local and private addresses only when the
// article's own URL is local.
func Run(ctx context.Context, urls []*url.URL, opts Options) (*Book, error) {
	switch len(urls) {
	case 0:
		return nil, errors.New("no URLs to convert")
	case 1:
		return forURL(ctx, urls[0]).Run(ctx, urls[0], opts)
	}
	return runList(ctx, urls, opts)
}

// forURL returns a Pipeline with the default settings for fetching u.
func forURL(ctx context.Context, u *url.URL) Pipeline {
	return New(fetch.IsLocal(ctx, u.Hostname()))
}

// Pipeline holds the settings for conversions.
type Pipeline struct {
	Fetcher *fetch.Fetcher
	Images  *assets.Processor
}

// New returns a Pipeline with the default settings. Pages and images share
// one HTTP client, which connects to local and private addresses only if
// allowLocal is set.
func New(allowLocal bool) Pipeline {
	f := fetch.New(allowLocal)
	return Pipeline{Fetcher: f, Images: assets.New(f.Client, f.UserAgent)}
}

// Run converts the page at u into a book.
func (p Pipeline) Run(ctx context.Context, u *url.URL, opts Options) (*Book, error) {
	ch, err := p.chapter(ctx, u, opts)
	if err != nil {
		return nil, err
	}
	title := cmp.Or(opts.Title, ch.article.Title)

	images := p.processor(opts)
	photo, err := coverPhoto(ctx, images, ch.article, ch.page)
	if err != nil {
		return nil, fmt.Errorf("processing images: %w", err)
	}
	coverJPEG, err := cover.Render(cover.Details{
		Title:    title,
		SiteName: ch.article.SiteName,
		Date:     build.DisplayDate(ch.article.Published),
	}, photo, images.Profile)
	if err != nil {
		return nil, fmt.Errorf("drawing the cover: %w", err)
	}

	data, err := build.BuildBook(build.Book{
		Title:    title,
		Chapters: []build.Chapter{{Article: ch.article, Body: ch.body}},
		Images:   ch.images,
		Cover:    coverJPEG,
	})
	if err != nil {
		return nil, fmt.Errorf("building the book: %w", err)
	}
	return finishBook(ctx, title, data, opts)
}

// runList makes a book of several articles. A URL that fails is skipped
// with a warning; it is an error only if all of them fail.
func runList(ctx context.Context, urls []*url.URL, opts Options) (*Book, error) {
	title := cmp.Or(opts.Title, ListTitle(time.Now()))
	var chapters []build.Chapter
	var images []assets.Image
	var titles []string
	var firstErr error
	for i, u := range urls {
		report(opts.Progress, fmt.Sprintf("[%d/%d] %s", i+1, len(urls), u))
		ch, err := forURL(ctx, u).chapter(ctx, u, Options{Warn: opts.Warn})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			report(opts.Warn, fmt.Sprintf("skipped %s: %v", u, err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		chapters = append(chapters, build.Chapter{Article: ch.article, Body: ch.body})
		images = append(images, ch.images...)
		titles = append(titles, ch.article.Title)
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("none of the %d URLs could be used; the first failed with: %w", len(urls), firstErr)
	}

	coverJPEG, err := cover.Render(cover.Details{Title: title, Contents: titles}, nil, device.EInk)
	if err != nil {
		return nil, fmt.Errorf("drawing the cover: %w", err)
	}
	data, err := build.BuildBook(build.Book{
		Title:    title,
		Chapters: chapters,
		Images:   images,
		Cover:    coverJPEG,
		Made:     time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("building the book: %w", err)
	}
	book, err := finishBook(ctx, title, data, opts)
	if err != nil {
		return nil, err
	}
	report(opts.Progress, fmt.Sprintf("made a book from %d of %d articles", len(chapters), len(urls)))
	return book, nil
}

// chapter is one article, extracted, cleaned and with its images processed.
type chapter struct {
	article *extract.Article
	body    *html.Node
	images  []assets.Image
	page    *url.URL // final URL, after redirects
}

// chapter fetches the page at u and turns it into a chapter.
func (p Pipeline) chapter(ctx context.Context, u *url.URL, opts Options) (*chapter, error) {
	page, err := p.Fetcher.Fetch(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}

	doc, err := html.Parse(strings.NewReader(page.HTML))
	if err != nil {
		return nil, fmt.Errorf("parsing the page: %w", err)
	}
	clean.Prepare(doc, page.URL)
	article, err := extract.Extract(doc, page.URL)
	if err != nil {
		return nil, err
	}

	if opts.BeforeImages != nil && opts.Title == "" {
		if err := opts.BeforeImages(article.Title); err != nil {
			return nil, err
		}
	}

	body, err := clean.Content(article.Content, page.URL)
	if err != nil {
		return nil, fmt.Errorf("cleaning the article: %w", err)
	}
	imgs, err := p.processor(opts).Process(ctx, body, page.URL)
	if err != nil {
		return nil, fmt.Errorf("processing images: %w", err)
	}
	return &chapter{article: article, body: body, images: imgs, page: page.URL}, nil
}

// processor returns a copy of the image processor that reports to opts.
func (p Pipeline) processor(opts Options) *assets.Processor {
	images := *p.Images
	images.Warn = opts.Warn
	return &images
}

// finishBook converts the book to KEPUB if asked.
func finishBook(ctx context.Context, title string, data []byte, opts Options) (*Book, error) {
	if opts.KEPUB {
		var err error
		if data, err = kepub.Convert(ctx, data); err != nil {
			return nil, err
		}
	}
	return &Book{Title: title, Content: bytes.NewReader(data)}, nil
}

// report sends msg to f, if set.
func report(f func(string), msg string) {
	if f != nil {
		f(msg)
	}
}

// minCoverPhoto is the smallest side, in pixels, of a preview image worth
// putting on the cover. Smaller ones are usually site logos, and a plain
// typographic cover looks better than a blown-up logo.
const minCoverPhoto = 300

// coverPhoto downloads the page's preview image for the cover. It returns
// nil, and the cover is typographic, when there is no usable image; only
// cancellation is an error.
func coverPhoto(ctx context.Context, images *assets.Processor, a *extract.Article, page *url.URL) (image.Image, error) {
	if a.ImageURL == "" {
		return nil, nil
	}
	img, err := images.Decode(ctx, a.ImageURL, page)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		if images.Warn != nil {
			images.Warn(fmt.Sprintf("cover image %s skipped, using a plain cover: %v", a.ImageURL, err))
		}
		return nil, nil
	}
	if b := img.Bounds(); min(b.Dx(), b.Dy()) < minCoverPhoto {
		return nil, nil
	}
	return img, nil
}
