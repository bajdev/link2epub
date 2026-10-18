// Command link2epub converts a web article into an EPUB for e-ink readers.
//
//	link2epub -URL <url> -SAVE <path> [-FORCE] [-KEPUB]
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

const usage = `Usage: link2epub -URL <url> -SAVE <path> [-FORCE] [-KEPUB]

Convert a web article into an EPUB for e-ink readers.

  -URL string
        Web page to convert (required). Must be http:// or https://.
  -SAVE string
        Where to write the book (required). Missing folders are created.
          - Ends in "/"          → a folder: <folder>/<article-title>.epub
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

// convertFunc turns a URL into a book. It is pipeline.Run in production and
// a fake in tests.
type convertFunc func(ctx context.Context, u *url.URL, opts pipeline.Options) (*pipeline.Book, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, pipeline.Run)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, convert convertFunc) int {
	fs := flag.NewFlagSet("link2epub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	urlArg := fs.String("URL", "", "web page to convert (required)")
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
	if *urlArg == "" {
		return usageError("-URL is required")
	}
	if *saveArg == "" {
		return usageError("-SAVE is required")
	}
	pageURL, err := parseURL(*urlArg)
	if err != nil {
		return usageError("-URL %q: %v", *urlArg, err)
	}
	target, err := save.Resolve(*saveArg, *kepub)
	if err != nil {
		return usageError("-SAVE: %v", err)
	}
	if target.Note != "" {
		fmt.Fprintf(stderr, "note: %s\n", target.Note)
	}

	// Fail before downloading anything when the filename is already known.
	if target.Known() && !*force {
		if err := save.CheckExists(target.Path("")); err != nil {
			return saveFailure(fail, err)
		}
	}

	opts := pipeline.Options{
		Warn:  func(msg string) { fmt.Fprintf(stderr, "warning: %s\n", msg) },
		KEPUB: *kepub,
	}
	if !target.Known() && !*force {
		// For folder targets the filename comes from the title; check it
		// before downloading images.
		opts.BeforeImages = func(title string) error { return save.CheckExists(target.Path(title)) }
	}
	book, err := convert(ctx, pageURL, opts)
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
