// Package clean turns extracted article markup into safe, well-formed XHTML
// for an EPUB chapter.
//
// Prepare runs on the whole page before extraction; Content runs on the
// extracted article, and Render serializes the result once images have been
// processed.
package clean

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// policy is the allow-list from PLAN.md §5.3. Anything not listed is
// removed; the text inside removed elements is kept, except for elements
// such as script and style whose content is dropped too.
var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"p", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "code",
		"em", "strong", "b", "i", "u", "s", "sub", "sup", "small", "abbr", "cite", "q",
		"ul", "ol", "li", "dl", "dt", "dd",
		"table", "thead", "tbody", "tfoot", "tr", "th", "td",
		"hr", "br", "span", "div", "aside", "figure", "figcaption",
	)
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.AllowRelativeURLs(true) // made absolute by resolveLinks
	p.RequireParseableURLs(true)
	p.AllowAttrs("src", "alt").OnElements("img")
	p.AllowDataURIImages()
	p.AllowAttrs("title").OnElements("abbr")
	p.AllowAttrs("colspan", "rowspan").Matching(regexp.MustCompile(`^[1-9][0-9]{0,2}$`)).OnElements("th", "td")
	p.AllowAttrs("lang").Matching(regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{1,8})*$`)).Globally()
	p.AllowAttrs("dir").Matching(regexp.MustCompile(`^(ltr|rtl|auto)$`)).Globally()
	return p
}()

// Content sanitizes the children of n, the extracted article, and returns
// them under a new <body> node. Relative links are resolved against base,
// headings are shifted so the article's top level is <h2> below the chapter
// title, and standalone images are wrapped in <figure>s with their
// captions.
func Content(n *html.Node, base *url.URL) (*html.Node, error) {
	var raw strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&raw, c); err != nil {
			return nil, err
		}
	}
	safe := policy.Sanitize(raw.String())

	// Re-parse the sanitized markup so the HTML parser repairs any invalid
	// nesting, then edit the tree.
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(safe), body)
	if err != nil {
		return nil, err
	}
	for _, c := range nodes {
		body.AppendChild(c)
	}
	resolveLinks(body, base)
	restoreCodeBlocks(body)
	buildFigures(body)
	shiftHeadings(body)
	removeEmpty(body)
	return body, nil
}

// Render serializes the children of body as an XHTML fragment. It fails
// rather than return markup that isn't well-formed, because e-readers
// refuse to open such books.
func Render(body *html.Node) (string, error) {
	var out strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&out, c); err != nil {
			return "", err
		}
	}
	if err := WellFormed("<div>" + out.String() + "</div>"); err != nil {
		return "", fmt.Errorf("cleaned content is not well-formed XHTML: %w", err)
	}
	return out.String(), nil
}

// mangledScheme matches a mailto: or tel: link that go-trafilatura has
// wrongly treated as relative and joined onto the page path, as in
// "https://example.com/post/mailto:a@example.com".
var mangledScheme = regexp.MustCompile(`/((?:mailto|tel):.*)$`)

// resolveLinks makes every link href and image src absolute.
func resolveLinks(n *html.Node, base *url.URL) {
	walk(n, func(n *html.Node) {
		key := "href"
		switch n.DataAtom {
		case atom.A:
		case atom.Img:
			key = "src"
		default:
			return
		}
		for i, a := range n.Attr {
			if a.Key != key {
				continue
			}
			href := a.Val
			if m := mangledScheme.FindStringSubmatch(href); m != nil && key == "href" {
				href = m[1]
			}
			if u, err := base.Parse(href); err == nil {
				n.Attr[i].Val = u.String()
			}
		}
	})
}

// blockParents are elements in which a <pre> may appear.
var blockParents = map[atom.Atom]bool{
	atom.Body: true, atom.Div: true, atom.Blockquote: true, atom.Li: true, atom.Dd: true,
	atom.Td: true, atom.Th: true, atom.Figure: true, atom.Aside: true,
}

// restoreCodeBlocks turns go-trafilatura's block-level <code> elements back
// into <pre><code>, so code keeps its line breaks and indentation. A <code>
// counts as a block when it sits directly in a block container and holds
// a line break or another <code>; inline code is left alone.
func restoreCodeBlocks(n *html.Node) {
	var blocks []*html.Node
	walk(n, func(n *html.Node) {
		if n.DataAtom == atom.Code && n.Parent != nil && blockParents[n.Parent.DataAtom] &&
			(strings.Contains(text(n), "\n") || contains(n, atom.Code)) {
			blocks = append(blocks, n)
		}
	})
	for _, c := range blocks {
		c.DataAtom, c.Data = atom.Pre, "pre"
		// Unwrap nested <code> elements, then put a single one inside.
		inner := &html.Node{Type: html.ElementNode, Data: "code", DataAtom: atom.Code}
		for child := c.FirstChild; child != nil; child = c.FirstChild {
			c.RemoveChild(child)
			if child.DataAtom == atom.Code {
				for gc := child.FirstChild; gc != nil; gc = child.FirstChild {
					child.RemoveChild(gc)
					inner.AppendChild(gc)
				}
				continue
			}
			inner.AppendChild(child)
		}
		c.AppendChild(inner)
	}
}

var headingLevels = map[atom.Atom]int{atom.H1: 1, atom.H2: 2, atom.H3: 3, atom.H4: 4, atom.H5: 5, atom.H6: 6}
var headingAtoms = []atom.Atom{0, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6}

// shiftHeadings moves all headings down by the same amount so that none is
// an <h1>, which is reserved for the chapter title. Relative levels are
// kept; anything pushed past <h6> stays at <h6>.
func shiftHeadings(n *html.Node) {
	top := 7
	walk(n, func(n *html.Node) {
		if level, ok := headingLevels[n.DataAtom]; ok {
			top = min(top, level)
		}
	})
	shift := 2 - top
	if shift <= 0 {
		return
	}
	walk(n, func(n *html.Node) {
		if level, ok := headingLevels[n.DataAtom]; ok {
			a := headingAtoms[min(level+shift, 6)]
			n.DataAtom, n.Data = a, a.String()
		}
	})
}

// removeEmpty drops images left without a source (the sanitizer removes
// unsafe ones), then paragraphs, headings and list items left with no text
// and no image, such as those that only held a removed embed.
func removeEmpty(n *html.Node) {
	var empty []*html.Node
	walk(n, func(n *html.Node) {
		if n.DataAtom == atom.Img && strings.TrimSpace(attr(n, "src")) == "" {
			empty = append(empty, n)
		}
	})
	for _, e := range empty {
		e.Parent.RemoveChild(e)
	}
	empty = nil
	walk(n, func(n *html.Node) {
		switch n.DataAtom {
		case atom.P, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Li:
			if strings.TrimSpace(text(n)) == "" && !contains(n, atom.Img) {
				empty = append(empty, n)
			}
		}
	})
	for _, e := range empty {
		if e.Parent != nil {
			e.Parent.RemoveChild(e)
		}
	}
}

// contains reports whether n has a descendant element of type a.
func contains(n *html.Node, a atom.Atom) bool {
	found := false
	walk(n, func(d *html.Node) { found = found || (d != n && d.DataAtom == a) })
	return found
}

func walk(n *html.Node, f func(*html.Node)) {
	if n.Type == html.ElementNode {
		f(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

func text(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

// WellFormed reports whether s parses as strict XML using only the five
// predefined entities, as EPUB content documents must.
func WellFormed(s string) error {
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
