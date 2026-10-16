// Package pipeline turns a web page into a book: fetch, extract, clean,
// process images and build the EPUB.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/assets"
	"github.com/bajdev/link2epub/internal/build"
	"github.com/bajdev/link2epub/internal/clean"
	"github.com/bajdev/link2epub/internal/extract"
	"github.com/bajdev/link2epub/internal/fetch"
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
}

// Run converts the page at u into a book using the default settings.
func Run(ctx context.Context, u *url.URL, opts Options) (*Book, error) {
	return New().Run(ctx, u, opts)
}

// Pipeline holds the settings for conversions.
type Pipeline struct {
	Fetcher *fetch.Fetcher
	Images  *assets.Processor
}

// New returns a Pipeline with the default settings. Pages and images share
// one HTTP client.
func New() Pipeline {
	f := fetch.New()
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

	e, err := build.Build(article, body, imgs)
	if err != nil {
		return nil, fmt.Errorf("building the book: %w", err)
	}
	return &Book{Title: article.Title, Content: e}, nil
}
