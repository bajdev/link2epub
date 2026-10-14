// Package pipeline turns a web page into a book: fetch, extract, clean,
// process images and build the EPUB.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
)

// ErrNotImplemented is returned by stages that have not been built yet.
var ErrNotImplemented = errors.New("not implemented yet")

// Book is a finished book, ready to be saved.
type Book struct {
	// Title is the article title, used to name the file for folder targets.
	Title string
	// Content writes the complete EPUB.
	Content io.WriterTo
}

// Run converts the page at u into a book.
func Run(ctx context.Context, u *url.URL) (*Book, error) {
	return nil, fmt.Errorf("converting web pages is %w (milestone M1)", ErrNotImplemented)
}
