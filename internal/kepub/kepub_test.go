package kepub

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/build"
	"github.com/bajdev/link2epub/internal/clean"
	"github.com/bajdev/link2epub/internal/epubtest"
	"github.com/bajdev/link2epub/internal/extract"
)

// testEPUB builds a small plain EPUB with a cover and two sections.
func testEPUB(t *testing.T) []byte {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(`<body><p>First sentence here. Second sentence follows it!</p>
		<h2>A section</h2><p>Text in the section, with <em>emphasis</em> and a <a href="https://example.com/x">link</a>.</p>
		<h2>Another</h2><ul><li>One item.</li><li>Two items.</li></ul></body>`))
	if err != nil {
		t.Fatal(err)
	}
	a := &extract.Article{
		URL: "https://example.com/post", Title: "KEPUB Test", Author: "Ada Lovelace",
		SiteName: "Example", Language: "en", Published: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Content: doc.FirstChild.LastChild,
	}
	u, _ := url.Parse(a.URL)
	body, err := clean.Content(a.Content, u)
	if err != nil {
		t.Fatal(err)
	}
	var cover bytes.Buffer
	if err := jpeg.Encode(&cover, image.NewGray(image.Rect(0, 0, 30, 40)), nil); err != nil {
		t.Fatal(err)
	}
	data, err := build.Build(a, body, nil, cover.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var tags = regexp.MustCompile(`<[^>]+>`)

// bodyText returns the visible text of a chapter's <body>, with whitespace
// collapsed.
func bodyText(chapter string) string {
	if i := strings.Index(chapter, "<body"); i >= 0 {
		chapter = chapter[i:]
	}
	return strings.Join(strings.Fields(tags.ReplaceAllString(chapter, " ")), " ")
}

func TestConvert(t *testing.T) {
	plain := testEPUB(t)
	data, err := Convert(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	book := epubtest.Open(t, data) // also checks mimetype is first and stored
	book.AssertWellFormed(t)
	before := epubtest.Open(t, plain)

	chapter := book.File(t, "chapter.xhtml")
	if !strings.Contains(chapter, `<span class="koboSpan" id="kobo.1.1">`) {
		t.Errorf("chapter has no koboSpan elements:\n%s", chapter)
	}
	// Sentences are spanned separately, so Kobo can count and highlight them.
	if !strings.Contains(chapter, `>First sentence here. </span><span class="koboSpan"`) {
		t.Errorf("sentences aren't split into spans:\n%s", chapter)
	}
	if !strings.Contains(chapter, "hyphens: auto") {
		t.Errorf("hyphenation isn't enabled")
	}
	// The words must be exactly the same, just wrapped.
	if got, want := bodyText(chapter), bodyText(before.File(t, "chapter.xhtml")); got != want {
		t.Errorf("text changed by conversion:\n got %q\nwant %q", got, want)
	}

	opf := book.File(t, ".opf")
	for _, want := range []string{`properties="cover-image"`, "<dc:publisher>Example</dc:publisher>", "<dc:source>https://example.com/post</dc:source>"} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF lost %q:\n%s", want, opf)
		}
	}
	if !strings.Contains(book.File(t, "nav.xhtml"), "#sec-2") || !strings.Contains(book.File(t, "toc.ncx"), "dtb:uid") {
		t.Errorf("table of contents changed by conversion")
	}

	path := filepath.Join(t.TempDir(), "book.kepub.epub")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	epubtest.EPUBCheck(t, path)
}

func TestConvertRejectsNonEPUB(t *testing.T) {
	if _, err := Convert(context.Background(), []byte("not a zip file")); err == nil {
		t.Error("Convert accepted data that isn't an EPUB")
	}
}

func TestConvertCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Convert(ctx, testEPUB(t)); err == nil {
		t.Error("Convert succeeded after cancellation")
	}
}
