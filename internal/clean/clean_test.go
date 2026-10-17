package clean

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

var base, _ = url.Parse("https://example.com/posts/article")

// body parses markup as the children of a <body>, like extracted content.
func body(t *testing.T, markup string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<body>" + markup + "</body>"))
	if err != nil {
		t.Fatal(err)
	}
	return doc.FirstChild.LastChild // html > body
}

func cleanString(t *testing.T, markup string) string {
	t.Helper()
	cleaned, err := Content(body(t, markup), base)
	if err != nil {
		t.Fatalf("Content(%q): %v", markup, err)
	}
	out, err := Render(cleaned)
	if err != nil {
		t.Fatalf("Render(%q): %v", markup, err)
	}
	if err := WellFormed("<div>" + out + "</div>"); err != nil {
		t.Fatalf("Content(%q) = %q, not well-formed: %v", markup, out, err)
	}
	return out
}

func TestContentSanitizes(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"keeps basic markup",
			`<p>Hello <em>there</em> <strong>you</strong>.</p>`,
			`<p>Hello <em>there</em> <strong>you</strong>.</p>`},
		{"drops scripts and their content",
			`<p>a</p><script>alert(1)</script><p>b</p>`,
			`<p>a</p><p>b</p>`},
		{"drops styles and their content",
			`<style>p { color: red }</style><p>a</p>`,
			`<p>a</p>`},
		{"drops event handlers, classes, ids and inline styles",
			`<p class="x" id="y" style="color:red" onclick="evil()">a</p>`,
			`<p>a</p>`},
		{"drops forms but keeps their text",
			`<form action="/x"><p>Sign up</p><input name="email"><button>Go</button></form>`,
			`<p>Sign up</p>Go`},
		{"drops iframes and objects",
			`<p>a</p><iframe src="https://ads.example.com"></iframe><object data="x.swf"></object>`,
			`<p>a</p>`},
		{"drops javascript links but keeps the text",
			`<p><a href="javascript:alert(1)">click</a></p>`,
			`<p>click</p>`},
		{"makes relative links absolute",
			`<p><a href="/about">about</a> <a href="other">other</a> <a href="#notes">notes</a></p>`,
			`<p><a href="https://example.com/about">about</a> <a href="https://example.com/posts/other">other</a> <a href="https://example.com/posts/article#notes">notes</a></p>`},
		{"keeps mailto links",
			`<p><a href="mailto:a@example.com">mail</a></p>`,
			`<p><a href="mailto:a@example.com">mail</a></p>`},
		{"keeps valid table spans and drops invalid ones",
			`<table><tr><td colspan="2" rowspan="0">a</td></tr></table>`,
			`<table><tbody><tr><td colspan="2">a</td></tr></tbody></table>`},
		{"keeps valid lang and dir, drops invalid",
			`<p lang="fr" dir="rtl">a</p><p lang="not valid!" dir="sideways">b</p>`,
			`<p lang="fr" dir="rtl">a</p><p>b</p>`},
		{"keeps images in paragraphs and makes their src absolute",
			`<p>a</p><p><img src="/x.png" alt="x" class="big" onerror="evil()"></p>`,
			`<p>a</p><p><img src="https://example.com/x.png" alt="x"/></p>`},
		{"drops images with unsafe sources",
			`<p>a <img src="javascript:evil()" alt="x"></p>`,
			`<p>a </p>`},
		{"removes empty paragraphs",
			`<p>a</p><p> </p><p><br></p><p>b</p>`,
			`<p>a</p><p>b</p>`},
		{"repairs mailto and tel links mangled by go-trafilatura",
			`<p><a href="https://example.com/posts/mailto:a@example.com">mail</a> <a href="https://example.com/posts/article/tel:+15551234">call</a></p>`,
			`<p><a href="mailto:a@example.com">mail</a> <a href="tel:+15551234">call</a></p>`},
		{"restores code blocks from go-trafilatura's block code",
			"<code><code>a = 1\nb = 2\n</code></code><p>after</p>",
			"<pre><code>a = 1\nb = 2\n</code></pre><p>after</p>"},
		{"restores multi-line code inside a list item",
			"<ul><li><code>x\ny</code></li></ul>",
			"<ul><li><pre><code>x\ny</code></pre></li></ul>"},
		{"leaves inline code in a paragraph alone, even with a line break",
			"<p>Run <code>make\nbuild</code> now.</p>",
			"<p>Run <code>make\nbuild</code> now.</p>"},
		{"keeps code blocks",
			`<pre><code>if a &lt; b {
	return
}</code></pre>`,
			"<pre><code>if a &lt; b {\n\treturn\n}</code></pre>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanString(t, tt.in); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestContentRepairsNesting(t *testing.T) {
	// Each of these is invalid HTML that must still come out well-formed.
	for _, in := range []string{
		`<p>a<div>b</div>c</p>`,
		`<ul><p>loose</p><li>item</li></ul>`,
		`<a href="/a">outer <a href="/b">inner</a></a>`,
		`<table><td>no row</td></table>`,
		`<p>unclosed <em>emphasis<p>next`,
		`<li>orphan item</li>`,
	} {
		cleanString(t, in) // fails the test if not well-formed
	}
}

func TestContentEntities(t *testing.T) {
	// &nbsp; and other HTML entities aren't defined in XML; they must come out
	// as characters.
	got := cleanString(t, `<p>a&nbsp;b &copy; &mdash; &lt;tag&gt; &amp;</p>`)
	want := "<p>a b © — &lt;tag&gt; &amp;</p>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShiftHeadings(t *testing.T) {
	tests := []struct{ in, want string }{
		{`<h1>a</h1><h2>b</h2><h3>c</h3>`, `<h2>a</h2><h3>b</h3><h4>c</h4>`},
		{`<h2>a</h2><h3>b</h3>`, `<h2>a</h2><h3>b</h3>`},
		{`<h3>a</h3><h4>b</h4>`, `<h3>a</h3><h4>b</h4>`},
		{`<h1>a</h1><h5>b</h5><h6>c</h6>`, `<h2>a</h2><h6>b</h6><h6>c</h6>`},
		{`<p>no headings</p>`, `<p>no headings</p>`},
	}
	for _, tt := range tests {
		if got := cleanString(t, tt.in); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWellFormed(t *testing.T) {
	good := []string{`<div><p>a</p><br/></div>`, `<p>&amp; &lt; &gt; &quot; &apos; &#160;</p>`}
	bad := []string{`<p>unclosed`, `<p>a</div>`, `<p>&nbsp;</p>`, `<br>`}
	for _, s := range good {
		if err := WellFormed(s); err != nil {
			t.Errorf("WellFormed(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := WellFormed(s); err == nil {
			t.Errorf("WellFormed(%q) = nil, want an error", s)
		}
	}
}
