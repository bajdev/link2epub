package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// maxURLs is the most articles one book can hold. That's plenty for a
// reading list, and it catches mistakes such as passing the wrong file.
const maxURLs = 50

// maxListBytes caps the size of a -URLS file; 50 URLs fit easily.
const maxListBytes = 1 << 20

// urlSource is one -URL or -URLS argument.
type urlSource struct {
	url  string // a -URL value
	list string // a -URLS file name, or "-" for standard input
}

// urlFlag and listFlag add -URL and -URLS values to one shared list, so
// mixed arguments keep their command-line order.
type urlFlag struct{ sources *[]urlSource }
type listFlag struct{ sources *[]urlSource }

func (f urlFlag) String() string { return "" }
func (f urlFlag) Set(v string) error {
	*f.sources = append(*f.sources, urlSource{url: v})
	return nil
}

func (f listFlag) String() string { return "" }
func (f listFlag) Set(v string) error {
	if v == "" {
		return errors.New("needs a file name, or - for standard input")
	}
	*f.sources = append(*f.sources, urlSource{list: v})
	return nil
}

// flagError marks a mistake in a -URL value, which is reported with the
// usage text. Problems with a -URLS list, or too many URLs, are reported
// on their own, so the message stands out.
type flagError struct{ error }

// tooManyError reports a list of more than maxURLs URLs. Its message is
// two sentences, ending with what to do about it.
type tooManyError int

func (n tooManyError) Error() string {
	return fmt.Sprintf("too many URLs: %d given, but a book can hold at most %d. Split the list into several books.", int(n), maxURLs)
}

// collectURLs reads the -URLS lists, checks every URL and drops repeats,
// keeping the order given. note receives a message for each repeat.
func collectURLs(sources []urlSource, stdin io.Reader, note func(string)) ([]*url.URL, error) {
	var urls []*url.URL
	seen := map[string]bool{}
	add := func(raw, where string) error {
		u, err := parseURL(raw)
		if err != nil {
			return fmt.Errorf("%s %q: %v", where, raw, err)
		}
		// The #fragment only picks a place on the page, so it doesn't make
		// a different article.
		key := *u
		key.Fragment, key.RawFragment = "", ""
		if seen[key.String()] {
			note(fmt.Sprintf("%s is listed more than once; including it once", raw))
			return nil
		}
		seen[key.String()] = true
		urls = append(urls, u)
		return nil
	}

	for _, s := range sources {
		if s.list == "" {
			if err := add(s.url, "-URL"); err != nil {
				return nil, flagError{err}
			}
			continue
		}
		name, lines, err := readList(s.list, stdin)
		if err != nil {
			return nil, err
		}
		found := false
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			found = true
			if err := add(line, fmt.Sprintf("-URLS %s, line %d:", name, i+1)); err != nil {
				return nil, err
			}
		}
		if !found {
			return nil, fmt.Errorf("-URLS %s has no URLs in it", name)
		}
	}

	if len(urls) > maxURLs {
		return nil, tooManyError(len(urls))
	}
	return urls, nil
}

// readList returns the lines of a -URLS list and a name for it in
// messages. "-" reads standard input.
func readList(path string, stdin io.Reader) (name string, lines []string, err error) {
	name = path
	var r io.Reader
	if path == "-" {
		name, r = "standard input", stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", nil, fmt.Errorf("-URLS: %w", err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxListBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("-URLS %s: %w", name, err)
	}
	if len(data) > maxListBytes {
		return "", nil, fmt.Errorf("-URLS %s is too large to be a list of URLs", name)
	}
	// Windows Notepad may start the file with a byte order mark.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	return name, strings.Split(string(data), "\n"), nil
}
