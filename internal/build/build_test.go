package build

import (
	"bytes"
	"image"
	imgjpeg "image/jpeg"
	imgpng "image/png"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/bajdev/link2epub/internal/assets"
	"github.com/bajdev/link2epub/internal/clean"
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
	base, err := url.Parse(a.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := clean.Content(a.Content, base)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Build(a, body, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBuild(t *testing.T) {
	a := testArticle(t, `<h1>Section</h1><p>First &amp; <em>second</em>.<br>Line two.</p><hr><ul><li>One</li></ul><script>evil()</script>`)
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
		`<div lang="en" xml:lang="en">`,
		`<link rel="stylesheet" type="text/css" href="../css/style.css"`,
		"<h1>Tom &amp; Jerry &lt;3</h1>",
		`<p class="byline">Ada Lovelace · Example News · 14 March 2025</p>`,
		`<a href="https://example.com/posts/1?a=1&amp;b=2">`,
		`<h2 id="sec-1">Section</h2>`, // shifted down from <h1>, anchored for the contents
		"First &amp; <em>second</em>.<br/>Line two.",
		"<hr/>",
		"<li>One</li>",
	} {
		if !strings.Contains(chapter, want) {
			t.Errorf("chapter is missing %q:\n%s", want, chapter)
		}
	}

	if strings.Contains(chapter, "evil()") {
		t.Errorf("chapter contains a script:\n%s", chapter)
	}
	if css := book.File(t, "style.css"); !strings.Contains(css, "border-left: 3px solid #000") {
		t.Errorf("stylesheet missing or unexpected:\n%s", css)
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

func TestBuildWithImages(t *testing.T) {
	var png bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 40, 30))
	if err := imgpng.Encode(&png, img); err != nil {
		t.Fatal(err)
	}
	a := testArticle(t, "")
	body, err := clean.Content(a.Content, mustURL(t, a.URL))
	if err != nil {
		t.Fatal(err)
	}
	// As left by the assets package: src is the image's filename. The second
	// image wasn't processed and must not end up in the book.
	frag, err := html.ParseFragment(strings.NewReader(`<figure><img src="abc123.png" alt="A chart"/></figure><p>x <img src="https://remote.example/a.png" alt=""/></p>`), body)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range frag {
		body.AppendChild(n)
	}

	data, err := Build(a, body, []assets.Image{{Filename: "abc123.png", MediaType: "image/png", Data: png.Bytes()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	book := epubtest.Open(t, data)
	book.AssertWellFormed(t)
	if got := book.File(t, "images/abc123.png"); got != png.String() {
		t.Errorf("stored image differs from the original")
	}
	chapter := book.File(t, "chapter.xhtml")
	if !strings.Contains(chapter, `<img src="../images/abc123.png" alt="A chart"/>`) {
		t.Errorf("chapter doesn't reference the stored image:\n%s", chapter)
	}
	if strings.Contains(chapter, "remote.example") {
		t.Errorf("chapter references a remote image:\n%s", chapter)
	}
	if opf := book.File(t, ".opf"); !strings.Contains(opf, `href="images/abc123.png"`) || !strings.Contains(opf, `media-type="image/png"`) {
		t.Errorf("OPF manifest is missing the image:\n%s", opf)
	}

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	epubtest.EPUBCheck(t, path)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBuildMetadataCoverAndContents(t *testing.T) {
	a := testArticle(t, `<p>Intro.</p><h2>First <em>part</em></h2><p>a</p><h3>Detail</h3><p>b</p><h2>Second part</h2><p>c</p><h4>Too deep</h4>`)
	var jpg bytes.Buffer
	if err := imgjpeg.Encode(&jpg, image.NewGray(image.Rect(0, 0, 30, 40)), nil); err != nil {
		t.Fatal(err)
	}
	body, err := clean.Content(a.Content, mustURL(t, a.URL))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Build(a, body, nil, jpg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	book := epubtest.Open(t, data)
	book.AssertWellFormed(t)

	opf := book.File(t, ".opf")
	for _, want := range []string{
		"<dc:publisher>Example News</dc:publisher>",
		"<dc:source>https://example.com/posts/1?a=1&amp;b=2</dc:source>",
		"<dc:date>2025-03-14</dc:date>",
		`href="images/cover.jpg" media-type="image/jpeg" properties="cover-image"`,
		`<itemref idref="cover.xhtml">`,
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF is missing %q:\n%s", want, opf)
		}
	}
	if got := book.File(t, "images/cover.jpg"); got != jpg.String() {
		t.Errorf("stored cover differs from the original")
	}

	chapter := book.File(t, "chapter.xhtml")
	for _, want := range []string{`<h2 id="sec-1">First <em>part</em></h2>`, `<h3 id="sec-2">Detail</h3>`, `<h2 id="sec-3">Second part</h2>`, "<h4>Too deep</h4>"} {
		if !strings.Contains(chapter, want) {
			t.Errorf("chapter is missing %q", want)
		}
	}

	nav := book.File(t, "nav.xhtml")
	navWant := `<li><a href="xhtml/chapter.xhtml">Tom &amp; Jerry &lt;3</a>
        <ol>
          <li><a href="xhtml/chapter.xhtml#sec-1">First part</a>
            <ol>
              <li><a href="xhtml/chapter.xhtml#sec-2">Detail</a></li>
            </ol>
          </li>
          <li><a href="xhtml/chapter.xhtml#sec-3">Second part</a></li>
        </ol>
        </li>`
	squash := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if !strings.Contains(squash(nav), squash(navWant)) {
		t.Errorf("nav document:\n%s\nwant it to contain:\n%s", nav, navWant)
	}

	ncx := book.File(t, "toc.ncx")
	id := regexp.MustCompile(`urn:uuid:[0-9a-f-]{36}`).FindString(opf)
	for _, want := range []string{
		`<meta name="dtb:uid" content="` + id + `"/>`,
		`<meta name="dtb:depth" content="3"/>`,
		`<navPoint id="navpoint-1" playOrder="1">`,
		`<navPoint id="navpoint-4" playOrder="4">`,
		`<content src="xhtml/chapter.xhtml#sec-2"/>`,
		`<navLabel><text>Second part</text></navLabel>`,
	} {
		if !strings.Contains(ncx, want) {
			t.Errorf("NCX is missing %q:\n%s", want, ncx)
		}
	}

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	epubtest.EPUBCheck(t, path)
}

func TestBuildWithoutHeadingsOrDate(t *testing.T) {
	a := testArticle(t, "<p>Just text.</p>")
	a.SiteName, a.Published = "", time.Time{}
	book := epubtest.Open(t, buildBytes(t, a))
	opf := book.File(t, ".opf")
	if strings.Contains(opf, "dc:publisher") || strings.Contains(opf, "dc:date") {
		t.Errorf("OPF has empty publisher or date:\n%s", opf)
	}
	if !strings.Contains(book.File(t, "nav.xhtml"), `<li><a href="xhtml/chapter.xhtml">Tom &amp; Jerry &lt;3</a></li>`) {
		t.Errorf("nav should list only the article:\n%s", book.File(t, "nav.xhtml"))
	}
	if !strings.Contains(book.File(t, "toc.ncx"), `<meta name="dtb:depth" content="1"/>`) {
		t.Errorf("NCX depth should be 1")
	}
}
