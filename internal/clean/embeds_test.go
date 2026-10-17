package clean

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func prepareString(t *testing.T, markup string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<body>" + markup + "</body>"))
	if err != nil {
		t.Fatal(err)
	}
	Prepare(doc, base)
	restoreImageSources(doc) // show real URLs rather than stand-ins
	var b strings.Builder
	body := doc.FirstChild.LastChild
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		html.Render(&b, c)
	}
	return b.String()
}

func TestPrepareReplacesVideoEmbeds(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"YouTube with title",
			`<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ?si=abc" title="Never Gonna Give You Up"></iframe>`,
			`<p>▶ Video: <a href="https://www.youtube.com/watch?v=dQw4w9WgXcQ">Never Gonna Give You Up</a></p>`},
		{"YouTube without title",
			`<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"></iframe>`,
			`<p>▶ Video: <a href="https://www.youtube.com/watch?v=dQw4w9WgXcQ">https://www.youtube.com/watch?v=dQw4w9WgXcQ</a></p>`},
		{"YouTube no-cookie, protocol-relative",
			`<iframe src="//www.youtube-nocookie.com/embed/abcDEF12345"></iframe>`,
			`<p>▶ Video: <a href="https://www.youtube.com/watch?v=abcDEF12345">https://www.youtube.com/watch?v=abcDEF12345</a></p>`},
		{"Vimeo",
			`<iframe src="https://player.vimeo.com/video/76979871?h=8272103f6e" title="The New Vimeo Player"></iframe>`,
			`<p>▶ Video: <a href="https://vimeo.com/76979871">The New Vimeo Player</a></p>`},
		{"other iframes are left alone",
			`<iframe src="https://ads.example.com/banner"></iframe>`,
			`<iframe src="https://ads.example.com/banner"></iframe>`},
		{"lookalike hosts are left alone",
			`<iframe src="https://youtube.com.evil.example/embed/dQw4w9WgXcQ"></iframe>`,
			`<iframe src="https://youtube.com.evil.example/embed/dQw4w9WgXcQ"></iframe>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prepareString(t, tt.in); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestPrepareResolvesLinks(t *testing.T) {
	// base is https://example.com/posts/article; relative links resolve
	// against its folder, /posts/.
	got := prepareString(t, `<p><a href="../faq#restart">a</a> <a href="other">b</a> <a href="/root">c</a> <a href="https://x.example/">d</a></p>`)
	want := `<p><a href="https://example.com/faq#restart">a</a> <a href="https://example.com/posts/other">b</a> <a href="https://example.com/root">c</a> <a href="https://x.example/">d</a></p>`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
