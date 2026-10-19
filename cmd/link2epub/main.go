// Command link2epub converts web articles into an EPUB for e-ink readers.
//
//	link2epub -URL <url> [-URL <url>...] [-URLS <file>] -SAVE <path> [-TITLE <title>] [-FORCE] [-KEPUB]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/bajdev/link2epub/internal/pipeline"
	"github.com/bajdev/link2epub/internal/save"
)

// Exit codes, as documented in the README.
const (
	exitOK        = 0
	exitFetch     = 1 // page couldn't be fetched
	exitUsage     = 2 // invalid arguments
	exitNoContent = 3 // no readable content found
	exitBuild     = 4 // book couldn't be built or written
	exitExists    = 5 // output exists and -FORCE wasn't given

	// exitInterrupted is the conventional code for a program stopped
	// with Ctrl-C (128 + SIGINT).
	exitInterrupted = 130
)

const usage = `Usage: link2epub -URL <url> -SAVE <path> [options]
       link2epub -URL <url> -URL <url>... -SAVE <path> [options]
       link2epub -URLS <file> -SAVE <path> [options]

Convert web articles into an EPUB for e-ink readers. Several URLs make one
book with a chapter per article.

  -URL string
        Web page to convert. Must be http:// or https://. Repeat it to put
        several articles in one book.
  -URLS file
        A text file listing web pages to put in one book, one per line.
        Blank lines and lines starting with # are ignored. Use - to read
        the list from standard input. Can be combined with -URL.
        A book holds at most 50 URLs.
  -TITLE string
        The book's title. Defaults to the article's title, or with several
        articles to "Reading list, <date>".
  -SAVE string
        Where to write the book (required). Missing folders are created.
          - Ends in "/"          → a folder: <folder>/<book-title>.epub
          - An existing folder   → same as above
          - Ends in .epub        → written to exactly that file
          - Anything else        → ".epub" is appended
        With -KEPUB the extension is ".kepub.epub" instead.
  -FORCE
        Overwrite the output file if it already exists.
  -KEPUB
        Write a Kobo KEPUB (.kepub.epub) instead of a plain EPUB.
  -VERSION
        Print the version and exit.
`

// convertFunc turns URLs into a book. It is pipeline.Run in production and
// a fake in tests.
type convertFunc func(ctx context.Context, urls []*url.URL, opts pipeline.Options) (*pipeline.Book, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, pipeline.Run)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, convert convertFunc) int {
	fs := flag.NewFlagSet("link2epub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	var sources []urlSource
	fs.Var(urlFlag{&sources}, "URL", "web page to convert; repeat for several")
	fs.Var(listFlag{&sources}, "URLS", "file listing web pages, one per line")
	titleArg := fs.String("TITLE", "", "the book's title")
	saveArg := fs.String("SAVE", "", "file or folder to write the book to (required)")
	force := fs.Bool("FORCE", false, "overwrite the output file if it already exists")
	kepub := fs.Bool("KEPUB", false, "write a Kobo KEPUB (.kepub.epub) instead of a plain EPUB")
	showVersion := fs.Bool("VERSION", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage // flag has already printed the error and usage
	}

	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, "error: "+format+"\n", a...)
		return code
	}
	usageError := func(format string, a ...any) int {
		fail(exitUsage, format, a...)
		fmt.Fprint(stderr, "\n"+usage)
		return exitUsage
	}

	if *showVersion {
		fmt.Fprintln(stdout, "link2epub", versionString())
		return exitOK
	}
	if fs.NArg() > 0 {
		return usageError("unexpected argument %q", fs.Arg(0))
	}
	if len(sources) == 0 {
		return usageError("-URL or -URLS is required")
	}
	if *saveArg == "" {
		return usageError("-SAVE is required")
	}
	urls, err := collectURLs(sources, stdin, func(msg string) { fmt.Fprintf(stderr, "note: %s\n", msg) })
	if errors.As(err, new(flagError)) {
		return usageError("%v", err)
	}
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	target, err := save.Resolve(*saveArg, *kepub)
	if err != nil {
		return usageError("-SAVE: %v", err)
	}
	if target.Note != "" {
		fmt.Fprintf(stderr, "note: %s\n", target.Note)
	}

	// A book of several articles is named before anything is fetched.
	title := strings.TrimSpace(*titleArg)
	if title == "" && len(urls) > 1 {
		title = pipeline.ListTitle(time.Now())
	}

	// Fail before downloading anything when the filename is already known.
	known := target.Known() || title != ""
	if known && !*force {
		if err := save.CheckExists(target.Path(title)); err != nil {
			return saveFailure(fail, err)
		}
	}

	opts := pipeline.Options{
		Warn:     func(msg string) { fmt.Fprintf(stderr, "warning: %s\n", msg) },
		Progress: func(msg string) { fmt.Fprintln(stderr, msg) },
		Title:    title,
		KEPUB:    *kepub,
	}
	if !known && !*force {
		// For folder targets the filename comes from the article title;
		// check it before downloading images.
		opts.BeforeImages = func(title string) error { return save.CheckExists(target.Path(title)) }
	}
	book, err := convert(ctx, urls, opts)
	switch {
	case errors.Is(err, context.Canceled):
		return fail(exitInterrupted, "interrupted")
	case errors.Is(err, save.ErrExists):
		return saveFailure(fail, err)
	case errors.Is(err, pipeline.ErrFetch):
		return fail(exitFetch, "%v", err)
	case errors.Is(err, pipeline.ErrNoContent):
		return fail(exitNoContent, "%v", err)
	case err != nil:
		return fail(exitBuild, "%v", err)
	}

	path := target.Path(book.Title)
	if !*force {
		if err := save.CheckExists(path); err != nil {
			return saveFailure(fail, err)
		}
	}
	if err := save.Write(path, book.Content, *force); err != nil {
		return saveFailure(fail, err)
	}
	fmt.Fprintln(stdout, path)
	return exitOK
}

// saveFailure reports an error from the save package with its exit code.
func saveFailure(fail func(int, string, ...any) int, err error) int {
	if errors.Is(err, save.ErrExists) {
		return fail(exitExists, "%v (use -FORCE to overwrite)", err)
	}
	return fail(exitBuild, "saving: %v", err)
}

// parseURL accepts absolute http and https URLs only.
func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("must start with http:// or https://")
	}
	if u.Host == "" {
		return nil, errors.New("missing host name")
	}
	return u, nil
}
