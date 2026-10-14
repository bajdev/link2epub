package build

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/epubtest"
	"github.com/bajdev/link2epub/internal/extract"
)

func testArticle(t *testing.T, body string) *extract.Article {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<body>" + body + "</body>"))
	if err != nil {
		t.Fatal(err)
	}
	return &extract.Article{
		URL:         "https://example.com/posts/1?a=1&b=2",
		Title:       "Tom & Jerry <3",
		Author:      "Ada Lovelace",
		SiteName:    "Example News",
		Description: "A test article.",
		Language:    "en",
		Published:   time.Date(2025, 3, 14, 9, 30, 0, 0, time.UTC),
		Content:     doc.FirstChild.LastChild, // html > body
	}
}

func buildBytes(t *testing.T, a *extract.Article) []byte {
	t.Helper()
	e, err := Build(a)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := e.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuild(t *testing.T) {
	a := testArticle(t, `<h2>Section</h2><p>First &amp; <em>second</em>.<br>Line two.</p><hr><ul><li>One</li></ul>`)
	data := buildBytes(t, a)
	book := epubtest.Open(t, data)
	book.AssertWellFormed(t)

	opf := book.File(t, ".opf")
	for _, want := range []string{
		"<dc:title>Tom &amp; Jerry &lt;3</dc:title>",
		"<dc:language>en</dc:language>",
		"Ada Lovelace",
		"A test article.",
		"urn:uuid:",
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF is missing %q:\n%s", want, opf)
		}
	}

	chapter := book.File(t, "chapter.xhtml")
	for _, want := range []string{
		"<h1>Tom &amp; Jerry &lt;3</h1>",
		`<p class="byline">Ada Lovelace · Example News · 14 March 2025</p>`,
		`<a href="https://example.com/posts/1?a=1&amp;b=2">`,
		"<h2>Section</h2>",
		"First &amp; <em>second</em>.<br/>Line two.",
		"<hr/>",
		"<li>One</li>",
	} {
		if !strings.Contains(chapter, want) {
			t.Errorf("chapter is missing %q:\n%s", want, chapter)
		}
	}

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	epubtest.EPUBCheck(t, path)
}

func TestBuildIdentifierIsStable(t *testing.T) {
	id := regexp.MustCompile(`urn:uuid:[0-9a-f-]{36}`)
	a := testArticle(t, "<p>text</p>")
	first := id.FindString(epubtest.Open(t, buildBytes(t, a)).File(t, ".opf"))
	second := id.FindString(epubtest.Open(t, buildBytes(t, a)).File(t, ".opf"))
	if first == "" || first != second {
		t.Errorf("identifiers %q and %q, want the same non-empty identifier", first, second)
	}

	a.URL = "https://example.com/other"
	if other := id.FindString(epubtest.Open(t, buildBytes(t, a)).File(t, ".opf")); other == first {
		t.Errorf("different URLs gave the same identifier %q", other)
	}
}

func TestByline(t *testing.T) {
	date := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		author, site string
		date         time.Time
		want         string
	}{
		{"A", "S", date, "A · S · 2 January 2025"},
		{"", "S", date, "S · 2 January 2025"},
		{"A", "", time.Time{}, "A"},
		{"", "", time.Time{}, ""},
	}
	for _, tt := range tests {
		got := byline(&extract.Article{Author: tt.author, SiteName: tt.site, Published: tt.date})
		if got != tt.want {
			t.Errorf("byline(%q, %q, %v) = %q, want %q", tt.author, tt.site, tt.date, got, tt.want)
		}
	}
}

func TestChapterWithoutByline(t *testing.T) {
	a := testArticle(t, "<p>text</p>")
	a.Author, a.SiteName, a.Published = "", "", time.Time{}
	chapter := epubtest.Open(t, buildBytes(t, a)).File(t, "chapter.xhtml")
	if strings.Contains(chapter, "byline") {
		t.Errorf("chapter has a byline paragraph:\n%s", chapter)
	}
}
