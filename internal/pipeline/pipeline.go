// Package pipeline turns a web page into a book: fetch, extract, clean,
// process images and build the EPUB.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/bajdev/link2epub/internal/build"
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

// Run converts the page at u into a book using the default settings.
func Run(ctx context.Context, u *url.URL) (*Book, error) {
	return Pipeline{Fetcher: fetch.New()}.Run(ctx, u)
}

// Pipeline holds the settings for a conversion.
type Pipeline struct {
	Fetcher *fetch.Fetcher
}

// Run converts the page at u into a book.
func (p Pipeline) Run(ctx context.Context, u *url.URL) (*Book, error) {
	page, err := p.Fetcher.Fetch(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}

	article, err := extract.Extract(page.HTML, page.URL)
	if err != nil {
		return nil, err
	}

	e, err := build.Build(article)
	if err != nil {
		return nil, fmt.Errorf("building the book: %w", err)
	}
	return &Book{Title: article.Title, Content: e}, nil
}
