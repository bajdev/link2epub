package extract

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func extractFile(t *testing.T, name, pageURL string) (*Article, error) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/pages/" + name)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	return Extract(parse(t, string(data)), u)
}

func parse(t *testing.T, page string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func render(t *testing.T, n *html.Node) string {
	t.Helper()
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

func TestExtractBlog(t *testing.T) {
	a, err := extractFile(t, "blog.html", "https://reading.example.com/posts/eink")
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct{ field, got, want string }{
		{"Title", a.Title, "Why E-Ink Is Easier on the Eyes"},
		{"Author", a.Author, "Ada Lovelace"},
		{"SiteName", a.SiteName, "The Reading Desk"},
		{"Language", a.Language, "en"},
		{"URL", a.URL, "https://reading.example.com/posts/eink"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if !strings.HasPrefix(a.Description, "A look at how electronic paper") {
		t.Errorf("Description = %q", a.Description)
	}
	if want := time.Date(2025, 3, 14, 0, 0, 0, 0, time.UTC); !a.Published.Equal(want) {
		t.Errorf("Published = %v, want %v", a.Published, want)
	}

	content := render(t, a.Content)
	for _, want := range []string{
		"Electronic paper does not shine light into your eyes.",
		"<h2>How the display works</h2>",
		"<blockquote>",
		"<li>Readable in direct sunlight</li>",
		"hard to beat &amp; well worth",
		// Links are made absolute.
		`<a href="https://reading.example.com/guides/choosing-an-ereader">`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content is missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"Subscribe to our newsletter", // navigation
		"We use cookies",              // cookie banner
		"Related posts",               // sidebar
		"Great article, thanks!",      // comments
		"All rights reserved",         // footer
		"window.dataLayer",            // script
		"<h1>",                        // title repeated in the content
	} {
		if strings.Contains(content, unwanted) {
			t.Errorf("content contains %q", unwanted)
		}
	}
}

func TestExtractNoContent(t *testing.T) {
	pages := map[string]string{
		"JavaScript shell": `<!DOCTYPE html><html><head><title>App</title></head>
			<body><div id="root"></div><script src="/app.js"></script></body></html>`,
		"too short": `<html><head><title>Short</title></head>
			<body><article><p>Just a sentence.</p></article></body></html>`,
		"empty": ``,
	}
	u, _ := url.Parse("https://example.com/")
	for name, page := range pages {
		if _, err := Extract(parse(t, page), u); !errors.Is(err, ErrNoContent) {
			t.Errorf("%s: error = %v, want ErrNoContent", name, err)
		}
	}
}

func TestLanguageTag(t *testing.T) {
	tests := map[string]string{
		"en": "en", "en-US": "en-US", "en_GB": "en-GB", " fr ": "fr", "pt-br": "pt-BR",
		"": "en", "not a language": "en", "und": "en",
	}
	for in, want := range tests {
		if got := languageTag(in); got != want {
			t.Errorf("languageTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoveLeadingTitle(t *testing.T) {
	tests := []struct {
		name, body, title, want string
	}{
		{"matching h1", "<h1>The Title</h1><p>text</p>", "The Title", "<p>text</p>"},
		{"case and spacing", "\n <h1>  the   TITLE </h1><p>text</p>", "The Title", "<p>text</p>"},
		{"matching h2", "<h2>The Title</h2><p>text</p>", "The Title", "<p>text</p>"},
		{"different heading", "<h1>Other</h1><p>text</p>", "The Title", "<h1>Other</h1><p>text</p>"},
		{"not first", "<p>intro</p><h1>The Title</h1>", "The Title", "<p>intro</p><h1>The Title</h1>"},
		{"paragraph", "<p>The Title</p>", "The Title", "<p>The Title</p>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader("<body>" + tt.body + "</body>"))
			if err != nil {
				t.Fatal(err)
			}
			body := doc.FirstChild.LastChild // html > body
			removeLeadingTitle(body, tt.title)
			got := strings.TrimSpace(render(t, body))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
