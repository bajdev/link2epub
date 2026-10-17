package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bajdev/link2epub/internal/epubtest"
	"github.com/bajdev/link2epub/internal/pipeline"
)

// fakeConverter returns a fixed book and counts how often it was called, so
// tests can check that nothing is fetched when arguments are rejected.
type fakeConverter struct {
	title string
	err   error
	calls int
}

func (f *fakeConverter) convert(ctx context.Context, u *url.URL, opts pipeline.Options) (*pipeline.Book, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if opts.BeforeImages != nil {
		if err := opts.BeforeImages(f.title); err != nil {
			return nil, err
		}
	}
	return &pipeline.Book{Title: f.title, Content: strings.NewReader("epub:" + f.title)}, nil
}

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, conv *fakeConverter, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr, conv.convert)
	return result{code, stdout.String(), stderr.String()}
}

const testURL = "https://example.com/article"

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "a.epub")
	tests := []struct {
		name string
		args []string
		want string // expected in stderr
	}{
		{"no arguments", nil, "-URL is required"},
		{"missing -SAVE", []string{"-URL", testURL}, "-SAVE is required"},
		{"missing -URL", []string{"-SAVE", out}, "-URL is required"},
		{"lowercase flag", []string{"-url", testURL, "-SAVE", out}, "flag provided but not defined: -url"},
		{"extra argument", []string{"-URL", testURL, "-SAVE", out, "extra"}, `unexpected argument "extra"`},
		{"ftp URL", []string{"-URL", "ftp://example.com/a", "-SAVE", out}, "must start with http:// or https://"},
		{"no scheme", []string{"-URL", "example.com/a", "-SAVE", out}, "must start with http:// or https://"},
		{"no host", []string{"-URL", "https://", "-SAVE", out}, "missing host name"},
		{"invalid URL", []string{"-URL", "https://exa mple.com/%zz", "-SAVE", out}, "not a valid URL"},
		{"kepub name without -KEPUB", []string{"-URL", testURL, "-SAVE", filepath.Join(dir, "a.kepub.epub")}, "needs -KEPUB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := &fakeConverter{title: "T"}
			r := runCLI(t, conv, tt.args...)
			if r.code != exitUsage {
				t.Errorf("exit code = %d, want %d", r.code, exitUsage)
			}
			if !strings.Contains(r.stderr, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", r.stderr, tt.want)
			}
			if !strings.Contains(r.stderr, "Usage: link2epub") {
				t.Errorf("stderr does not show usage: %q", r.stderr)
			}
			if conv.calls != 0 {
				t.Errorf("converter called %d times, want 0", conv.calls)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	r := runCLI(t, &fakeConverter{}, "-h")
	if r.code != exitOK {
		t.Errorf("exit code = %d, want %d", r.code, exitOK)
	}
	if !strings.Contains(r.stderr, "Usage: link2epub") {
		t.Errorf("stderr = %q, want usage", r.stderr)
	}
}

func TestSaveToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.epub")
	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", path)
	if r.code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != path {
		t.Errorf("stdout = %q, want %q", r.stdout, path)
	}
	assertFile(t, path, "epub:Title")
}

func TestSaveToFolderCreatesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Books", "Articles")
	r := runCLI(t, &fakeConverter{title: "My Great Article"}, "-URL", testURL, "-SAVE", dir+"/")
	if r.code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	want := filepath.Join(dir, "my-great-article.epub")
	if strings.TrimSpace(r.stdout) != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
	assertFile(t, want, "epub:My Great Article")
}

func TestExistingFileStopsBeforeFetching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.epub")
	writeFile(t, path, "old")
	conv := &fakeConverter{title: "Title"}

	r := runCLI(t, conv, "-URL", testURL, "-SAVE", path)
	if r.code != exitExists {
		t.Errorf("exit code = %d, want %d", r.code, exitExists)
	}
	if !strings.Contains(r.stderr, "already exists (use -FORCE to overwrite)") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if conv.calls != 0 {
		t.Errorf("converter called %d times, want 0", conv.calls)
	}
	assertFile(t, path, "old")
}

func TestExistingFileInFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "title.epub")
	writeFile(t, path, "old")

	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", dir)
	if r.code != exitExists {
		t.Errorf("exit code = %d, want %d; stderr = %q", r.code, exitExists, r.stderr)
	}
	assertFile(t, path, "old")
}

func TestForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.epub")
	writeFile(t, path, "old")

	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", path, "-FORCE")
	if r.code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	assertFile(t, path, "epub:Title")

	// -FORCE also applies to folder targets.
	writeFile(t, filepath.Join(dir, "title.epub"), "old")
	r = runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", dir, "-FORCE")
	if r.code != exitOK {
		t.Fatalf("folder: exit code = %d, stderr = %q", r.code, r.stderr)
	}
	assertFile(t, filepath.Join(dir, "title.epub"), "epub:Title")
}

func TestConversionFailureWritesNothing(t *testing.T) {
	dir := t.TempDir()
	conv := &fakeConverter{err: errors.New("boom")}

	r := runCLI(t, conv, "-URL", testURL, "-SAVE", filepath.Join(dir, "a.epub"))
	if r.code != exitBuild {
		t.Errorf("exit code = %d, want %d", r.code, exitBuild)
	}
	if !strings.Contains(r.stderr, "error: boom") {
		t.Errorf("stderr = %q", r.stderr)
	}
	assertEmptyDir(t, dir)
}

func TestSavePathThroughFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Books"), "not a folder")

	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", filepath.Join(dir, "Books", "a.epub"))
	if r.code != exitBuild {
		t.Errorf("exit code = %d, want %d; stderr = %q", r.code, exitBuild, r.stderr)
	}
}

func TestKEPUBExtensionNote(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.kepub.epub"), "old")

	// The overwrite check uses the adjusted name, before anything is fetched.
	conv := &fakeConverter{title: "Title"}
	r := runCLI(t, conv, "-URL", testURL, "-SAVE", filepath.Join(dir, "a.epub"), "-KEPUB")
	if !strings.Contains(r.stderr, "note: saving as a.kepub.epub") {
		t.Errorf("stderr = %q, want a note about the new name", r.stderr)
	}
	if r.code != exitExists || conv.calls != 0 {
		t.Errorf("exit code = %d, converter calls = %d; want %d and 0", r.code, conv.calls, exitExists)
	}
}

func TestKEPUBNotImplemented(t *testing.T) {
	// Until milestone M5, -KEPUB is accepted but conversion fails cleanly.
	dir := t.TempDir()
	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", dir, "-KEPUB")
	if r.code != exitBuild || !strings.Contains(r.stderr, "KEPUB conversion is not implemented yet") {
		t.Errorf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	assertEmptyDir(t, dir)
}

// assertCover checks the book has a 1404x1872 grayscale cover, listed
// first in the spine. With photo, the cover's upper half must show the
// photo (mid-grays) rather than the plain cover's black band and white
// space.
func assertCover(t *testing.T, book *epubtest.Book, photo bool) {
	t.Helper()
	opf := book.File(t, ".opf")
	if !strings.Contains(opf, `href="images/cover.jpg" media-type="image/jpeg" properties="cover-image"`) {
		t.Fatalf("OPF has no cover image:\n%s", opf)
	}
	if strings.Index(opf, `<itemref idref="cover.xhtml">`) > strings.Index(opf, `<itemref idref="chapter.xhtml">`) {
		t.Errorf("cover isn't first in the spine:\n%s", opf)
	}
	img, err := jpeg.Decode(strings.NewReader(book.Files["EPUB/images/cover.jpg"]))
	if err != nil {
		t.Fatal(err)
	}
	gray, ok := img.(*image.Gray)
	if !ok || gray.Bounds().Dx() != 1404 || gray.Bounds().Dy() != 1872 {
		t.Fatalf("cover is %T %v, want 1404x1872 grayscale", img, img.Bounds())
	}
	mid := 0
	for y := 300; y < 900; y += 10 {
		for x := 0; x < 1404; x += 10 {
			if v := gray.GrayAt(x, y).Y; v > 40 && v < 215 {
				mid++
			}
		}
	}
	if hasPhoto := mid > 2000; hasPhoto != photo {
		t.Errorf("cover photo = %v (%d mid-gray samples), want %v", hasPhoto, mid, photo)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s contains %q, want %q", path, got, want)
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%s is not empty: %v", dir, entries)
	}
}

// TestEndToEnd runs the real pipeline against a local web server.
func TestEndToEnd(t *testing.T) {
	blog, err := os.ReadFile("../../testdata/pages/blog.html")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := os.ReadFile("../../testdata/pages/docs.html")
	if err != nil {
		t.Fatal(err)
	}
	essay, err := os.ReadFile("../../testdata/pages/photo-essay.html")
	if err != nil {
		t.Fatal(err)
	}
	lightbox, err := os.ReadFile("../../testdata/pages/lightbox.html")
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]string{ // served path → file in testdata/images
		"/img/press.jpg":   "press.jpg",
		"/img/diagram.png": "diagram.png",
		"/img/type.webp":   "gradient.webp",
		// lightbox.html; srcset should select the 1536w file.
		"/wp-content/uploads/2026/10/upgrade-dialog-1536x864.png": "diagram.png",
		"/wp-content/uploads/2026/10/settings-640x480.jpg":        "press.jpg",
		"/wp-content/uploads/2026/10/hero.jpg":                    "press.jpg", // og:image, for the cover
	}
	var imageRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/img/") || strings.HasPrefix(r.URL.Path, "/wp-content/") {
			imageRequests.Add(1)
			name, ok := images[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join("../../testdata/images", name))
			return
		}
		switch r.URL.Path {
		case "/news/2026/10/kde-turns-30/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(lightbox)
		case "/essays/print-shop":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(essay)
		case "/posts/eink":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(blog)
		case "/docs/config":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(docs)
		case "/app":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body><div id="root"></div><script src="/app.js"></script></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	realRun := func(args ...string) result {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), args, &stdout, &stderr, pipeline.Run)
		return result{code, stdout.String(), stderr.String()}
	}

	t.Run("article", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/posts/eink", "-SAVE", dir)
		if r.code != exitOK {
			t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		path := filepath.Join(dir, "why-e-ink-is-easier-on-the-eyes.epub")
		if strings.TrimSpace(r.stdout) != path {
			t.Errorf("stdout = %q, want %q", r.stdout, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		book := epubtest.Open(t, data)
		book.AssertWellFormed(t)
		chapter := book.File(t, "chapter.xhtml")
		for _, want := range []string{
			"<h1>Why E-Ink Is Easier on the Eyes</h1>",
			"Ada Lovelace · The Reading Desk · 14 March 2025",
			"Electronic paper does not shine light into your eyes.",
			`▶ Video: <a href="https://www.youtube.com/watch?v=dQw4w9WgXcQ">How E Ink works</a>`,
		} {
			if !strings.Contains(chapter, want) {
				t.Errorf("chapter is missing %q", want)
			}
		}
		if strings.Contains(chapter, "Great article, thanks!") {
			t.Errorf("chapter contains page comments")
		}
		assertCover(t, book, false)
		epubtest.EPUBCheck(t, path)
	})

	t.Run("messy documentation page", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/docs/config", "-SAVE", dir)
		if r.code != exitOK {
			t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		path := filepath.Join(dir, "configuring-the-sync-daemon.epub")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		book := epubtest.Open(t, data)
		book.AssertWellFormed(t)
		chapter := book.File(t, "chapter.xhtml")
		for _, want := range []string{
			`<div lang="en" xml:lang="en">`,
			`<h2 id="sec-1">File location</h2>`,
			`<h3 id="sec-3">Durations</h3>`,
			"<pre><code>interval = ",
			"<td><code>wifi_only</code></td>",
			`<a href="mailto:docs@example.com">`,
			`<a href="` + srv.URL + `/faq#restart">`,
			"recipes — including", // &mdash; as a character, not an entity
		} {
			if !strings.Contains(chapter, want) {
				t.Errorf("chapter is missing %q", want)
			}
		}
		for _, unwanted := range []string{"onclick", "style=", "javascript:", "analytics", "<form", "<button", "Was this page helpful"} {
			if strings.Contains(chapter, unwanted) {
				t.Errorf("chapter contains %q", unwanted)
			}
		}
		assertCover(t, book, false)
		squash := func(s string) string { return strings.Join(strings.Fields(s), " ") }
		nav := squash(book.File(t, "nav.xhtml"))
		for _, want := range []string{
			`<a href="xhtml/chapter.xhtml#sec-1">File location</a>`,
			`<a href="xhtml/chapter.xhtml#sec-2">Options</a> <ol> <li><a href="xhtml/chapter.xhtml#sec-3">Durations</a></li> </ol>`,
			`<a href="xhtml/chapter.xhtml#sec-4">Example</a>`,
		} {
			if !strings.Contains(nav, want) {
				t.Errorf("nav is missing %q:\n%s", want, nav)
			}
		}
		if strings.Contains(nav, "Edge cases") {
			t.Errorf("nav lists an <h4>; only sections and subsections belong in it")
		}
		epubtest.EPUBCheck(t, path)
	})

	t.Run("photo essay", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/essays/print-shop", "-SAVE", dir)
		if r.code != exitOK {
			t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		if !strings.Contains(r.stderr, "warning: image "+srv.URL+"/img/missing.png skipped: server returned 404 Not Found") {
			t.Errorf("stderr = %q, want a warning about the missing image", r.stderr)
		}
		path := filepath.Join(dir, "a-walk-through-the-print-shop.epub")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		book := epubtest.Open(t, data)
		book.AssertWellFormed(t)

		var stored []string
		for _, name := range book.Order {
			if strings.HasPrefix(name, "EPUB/images/") && name != "EPUB/images/cover.jpg" {
				stored = append(stored, name)
			}
		}
		if len(stored) != 3 {
			t.Fatalf("book has images %v, want 3 (photo, diagram, WebP picture)", stored)
		}
		for _, name := range stored {
			img, format, err := image.Decode(strings.NewReader(book.Files[name]))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if format != "jpeg" && format != "png" {
				t.Errorf("%s is %s, want jpeg or png", name, format)
			}
			if b := img.Bounds(); b.Dx() > 1404 || b.Dy() > 1872 {
				t.Errorf("%s is %dx%d, larger than the screen", name, b.Dx(), b.Dy())
			}
			switch img.(type) {
			case *image.Gray, *image.Paletted:
			default:
				t.Errorf("%s decodes as %T, want grayscale", name, img)
			}
		}

		chapter := book.File(t, "chapter.xhtml")
		for _, want := range []string{
			`alt="The old printing press"/><figcaption>The 1890s press still runs every Friday.</figcaption></figure>`,
			`alt="How ink reaches the paper"/></figure>`,
			`alt="Drawers of wooden type"/></figure>`,
			`<span class="missing-image">[Image: small press icon]</span>`,
			`<p class="byline">Grace Hopper · Letterpress Weekly</p>`, // no invented date
		} {
			if !strings.Contains(chapter, want) {
				t.Errorf("chapter is missing %q", want)
			}
		}
		if strings.Contains(chapter, "tracker.example") || strings.Contains(chapter, `<img src="http`) {
			t.Errorf("chapter references remote images:\n%s", chapter)
		}
		assertCover(t, book, true)
		if opf := book.File(t, ".opf"); !strings.Contains(opf, "<dc:publisher>Letterpress Weekly</dc:publisher>") ||
			!strings.Contains(opf, "<dc:source>"+srv.URL+"/essays/print-shop</dc:source>") {
			t.Errorf("OPF is missing the publisher or source:\n%s", opf)
		}
		epubtest.EPUBCheck(t, path)
	})

	t.Run("images inside lightbox zoom links", func(t *testing.T) {
		// Regression test: go-trafilatura dropped every image on pages that
		// wrap images in zoom links, such as Ars Technica articles.
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/news/2026/10/kde-turns-30/", "-SAVE", dir)
		if r.code != exitOK {
			t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		if strings.Contains(r.stderr, "warning") {
			t.Errorf("unexpected warnings: %q", r.stderr)
		}
		path := filepath.Join(dir, "kde-turns-30-with-plasma-6-8.epub")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		book := epubtest.Open(t, data)
		book.AssertWellFormed(t)
		var stored int
		for _, name := range book.Order {
			if strings.HasPrefix(name, "EPUB/images/") && name != "EPUB/images/cover.jpg" {
				stored++
			}
		}
		if stored != 2 {
			t.Errorf("book has %d images, want 2", stored)
		}
		chapter := book.File(t, "chapter.xhtml")
		for _, want := range []string{
			`<figcaption>The new remote upgrade dialog. Credit: KDE</figcaption></figure>`,
			`alt="System Settings in Plasma 6.8"/><figcaption>System Settings has been reorganised.</figcaption></figure>`,
		} {
			if !strings.Contains(chapter, want) {
				t.Errorf("chapter is missing %q", want)
			}
		}
		if n := strings.Count(chapter, "The new remote upgrade dialog."); n != 1 {
			t.Errorf("caption appears %d times, want once", n)
		}
		assertCover(t, book, true) // the page's og:image, as on Ars Technica
		epubtest.EPUBCheck(t, path)
	})

	t.Run("existing file stops before images are downloaded", func(t *testing.T) {
		dir := t.TempDir()
		existing := filepath.Join(dir, "a-walk-through-the-print-shop.epub")
		writeFile(t, existing, "old")
		before := imageRequests.Load()
		r := realRun("-URL", srv.URL+"/essays/print-shop", "-SAVE", dir)
		if r.code != exitExists {
			t.Errorf("exit code = %d, want %d; stderr = %q", r.code, exitExists, r.stderr)
		}
		if n := imageRequests.Load() - before; n != 0 {
			t.Errorf("%d images downloaded, want 0", n)
		}
		assertFile(t, existing, "old")
	})

	t.Run("not found", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/missing", "-SAVE", dir)
		if r.code != exitFetch || !strings.Contains(r.stderr, "returned 404 Not Found") {
			t.Errorf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		assertEmptyDir(t, dir)
	})

	t.Run("no content", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/app", "-SAVE", dir)
		if r.code != exitNoContent || !strings.Contains(r.stderr, "no readable article content") {
			t.Errorf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		assertEmptyDir(t, dir)
	})
}
