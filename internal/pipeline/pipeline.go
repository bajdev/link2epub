// Package pipeline turns a web page into a book: fetch, extract, clean,
// process images and build the EPUB.
package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/assets"
	"github.com/bajdev/link2epub/internal/build"
	"github.com/bajdev/link2epub/internal/clean"
	"github.com/bajdev/link2epub/internal/cover"
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

// Options adjust a single conversion.
type Options struct {
	// BeforeImages, if set, is called with the article title once it is
	// known and before any image is downloaded. An error stops the
	// conversion and is returned unchanged. The CLI uses it to stop early
	// when the output file already exists.
	BeforeImages func(title string) error
	// Warn, if set, receives problems that don't stop the conversion, such
	// as an image that couldn't be downloaded.
	Warn func(msg string)
	// KEPUB converts the finished book to a Kobo KEPUB.
	KEPUB bool
}

// Run converts the page at u into a book using the default settings.
// Images and redirects may lead to local and private addresses only when
// u itself is local.
func Run(ctx context.Context, u *url.URL, opts Options) (*Book, error) {
	return New(fetch.IsLocal(ctx, u.Hostname())).Run(ctx, u, opts)
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

	if opts.BeforeImages != nil {
		if err := opts.BeforeImages(article.Title); err != nil {
			return nil, err
		}
	}

	body, err := clean.Content(article.Content, page.URL)
	if err != nil {
		return nil, fmt.Errorf("cleaning the article: %w", err)
	}
	images := *p.Images
	images.Warn = opts.Warn
	imgs, err := images.Process(ctx, body, page.URL)
	if err != nil {
		return nil, fmt.Errorf("processing images: %w", err)
	}

	photo, err := coverPhoto(ctx, &images, article, page.URL)
	if err != nil {
		return nil, fmt.Errorf("processing images: %w", err)
	}
	coverJPEG, err := cover.Render(cover.Details{
		Title:    article.Title,
		SiteName: article.SiteName,
		Date:     build.DisplayDate(article.Published),
	}, photo, images.Profile)
	if err != nil {
		return nil, fmt.Errorf("drawing the cover: %w", err)
	}

	data, err := build.Build(article, body, imgs, coverJPEG)
	if err != nil {
		return nil, fmt.Errorf("building the book: %w", err)
	}
	if opts.KEPUB {
		if data, err = kepub.Convert(ctx, data); err != nil {
			return nil, err
		}
	}
	return &Book{Title: article.Title, Content: bytes.NewReader(data)}, nil
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
