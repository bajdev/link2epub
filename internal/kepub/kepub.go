// Package kepub converts a finished EPUB into a Kobo KEPUB.
//
// Kobo devices open files ending in .kepub.epub with their own WebKit-based
// renderer instead of Adobe's RMSDK, which gives faster page turns, reading
// statistics and more reliable highlights. kepubify does the conversion: it
// wraps each sentence in a koboSpan (which the statistics and highlights
// need) and adds Kobo's style hooks.
package kepub

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"

	"github.com/pgaskin/kepubify/v4/kepub"
)

// Convert returns epub converted to a KEPUB.
func Convert(ctx context.Context, epub []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		return nil, fmt.Errorf("reading the EPUB: %w", err)
	}
	c := kepub.NewConverterWithOptions(
		// Hyphenation helps justified text on the Clara HD's 6" screen.
		kepub.ConverterOptionHyphenate(true),
		// Smartypants is left off: it rewrites quotes and dashes, which
		// would change the article's text.
	)
	var out bytes.Buffer
	if err := c.Convert(ctx, &out, zr); err != nil {
		return nil, fmt.Errorf("converting to KEPUB: %w", err)
	}
	return out.Bytes(), nil
}
