package clean

import (
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// targetWidth is the image width worth downloading: the reMarkable 2's
// screen width. Larger candidates only waste bandwidth.
const targetWidth = 1404

// lazySrcAttrs hold the real image URL on pages that lazy-load images.
var lazySrcAttrs = []string{"data-src", "data-original", "data-lazy-src", "data-url", "data-hi-res-src"}
var lazySrcsetAttrs = []string{"data-srcset", "data-lazy-srcset"}

// prepareImages fixes images before extraction, because go-trafilatura only
// looks at src:
//
//   - lazy-loading attributes and the best srcset or <picture> candidate
//     are moved into src, and src is made absolute;
//   - tracking pixels (width or height of 2 or less) are removed;
//   - links that wrap nothing but an image, such as "click to zoom"
//     lightbox links, are unwrapped: go-trafilatura drops an image inside
//     such a link when it sits in a <div> or <p>, as on Ars Technica and
//     many WordPress sites, and the link is useless on an e-reader;
//   - each <figcaption> becomes an italic paragraph, which go-trafilatura
//     keeps (it drops captions); Content turns it back into a caption.
func prepareImages(doc *html.Node, base *url.URL) {
	var imgs, captions, pixels, imageLinks []*html.Node
	walk(doc, func(n *html.Node) {
		switch n.DataAtom {
		case atom.A:
			if contains(n, atom.Img) && strings.TrimSpace(text(n)) == "" {
				imageLinks = append(imageLinks, n)
			}
		case atom.Img:
			if isTrackingPixel(n) {
				pixels = append(pixels, n)
			} else {
				imgs = append(imgs, n)
			}
		case atom.Figcaption:
			captions = append(captions, n)
		}
	})
	for _, n := range pixels {
		n.Parent.RemoveChild(n)
	}
	for _, a := range imageLinks {
		unwrap(a)
	}
	for _, n := range imgs {
		if src := bestSource(n); src != "" {
			if u, err := base.Parse(src); err == nil {
				setAttr(n, "src", u.String())
			}
		}
	}
	for _, c := range captions {
		// <figcaption>x</figcaption> → <p><i>x</i></p>
		i := &html.Node{Type: html.ElementNode, Data: "i", DataAtom: atom.I}
		if hasBlock(c) {
			// A paragraph can't hold blocks, so captions laid out with
			// <div>s (as on Ars Technica) are reduced to their text.
			caption := spacedText(c)
			for child := c.FirstChild; child != nil; child = c.FirstChild {
				c.RemoveChild(child)
			}
			i.AppendChild(&html.Node{Type: html.TextNode, Data: caption})
		} else {
			for child := c.FirstChild; child != nil; child = c.FirstChild {
				c.RemoveChild(child)
				i.AppendChild(child)
			}
		}
		c.Data, c.DataAtom, c.Attr = "p", atom.P, nil
		c.AppendChild(i)
	}
}

// bestSource picks the URL to download for an <img>: the best candidate
// from its srcset, its <picture> sources or lazy-loading attributes, else
// its src. A placeholder data: URL in src loses to any real URL.
func bestSource(img *html.Node) string {
	var srcsets []string
	if pic := img.Parent; pic != nil && pic.DataAtom == atom.Picture {
		for s := pic.FirstChild; s != nil; s = s.NextSibling {
			if s.DataAtom == atom.Source && usableType(attr(s, "type")) {
				srcsets = append(srcsets, attr(s, "srcset"), attr(s, "data-srcset"))
			}
		}
	}
	srcsets = append(srcsets, attr(img, "srcset"))
	for _, a := range lazySrcsetAttrs {
		srcsets = append(srcsets, attr(img, a))
	}

	var candidates []candidate
	for _, s := range srcsets {
		candidates = append(candidates, parseSrcset(s)...)
	}
	if best := pickCandidate(candidates); best != "" {
		return best
	}
	for _, a := range lazySrcAttrs {
		if v := strings.TrimSpace(attr(img, a)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(attr(img, "src"))
}

// usableType reports whether a <source> type is one we can decode. AVIF and
// JPEG XL aren't supported, so their sources are skipped in favour of the
// fallback.
func usableType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp", "image/svg+xml":
		return true
	}
	return false
}

type candidate struct {
	url     string
	width   int     // from a "800w" descriptor, or 0
	density float64 // from a "2x" descriptor, or 0
}

// parseSrcset splits a srcset attribute into candidates. URLs may contain
// commas (as some image CDNs use), so candidates are split on the comma
// that follows a descriptor or whitespace, as the HTML spec describes.
func parseSrcset(s string) []candidate {
	var out []candidate
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimLeft(s, " \t\n\r\f,") {
		end := strings.IndexAny(s, " \t\n\r\f")
		if end < 0 {
			end = len(s)
		}
		u := s[:end]
		s = s[end:]
		var desc string
		if strings.HasSuffix(u, ",") {
			u = strings.TrimRight(u, ",")
		} else {
			comma := strings.IndexByte(s, ',')
			if comma < 0 {
				comma = len(s)
			}
			desc = strings.TrimSpace(s[:comma])
			s = s[comma:]
		}
		if u == "" || strings.HasPrefix(u, "data:") {
			continue
		}
		c := candidate{url: u}
		switch {
		case strings.HasSuffix(desc, "w"):
			c.width, _ = strconv.Atoi(strings.TrimSuffix(desc, "w"))
		case strings.HasSuffix(desc, "x"):
			c.density, _ = strconv.ParseFloat(strings.TrimSuffix(desc, "x"), 64)
		}
		out = append(out, c)
	}
	return out
}

// pickCandidate chooses the smallest candidate at least targetWidth wide,
// or the widest if none is; without width descriptors it takes the highest
// density.
func pickCandidate(cs []candidate) string {
	var best *candidate
	for i := range cs {
		c := &cs[i]
		if c.width == 0 {
			continue
		}
		switch {
		case best == nil:
			best = c
		case best.width < targetWidth:
			if c.width > best.width {
				best = c
			}
		case c.width >= targetWidth && c.width < best.width:
			best = c
		}
	}
	if best != nil {
		return best.url
	}
	for i := range cs {
		if best == nil || cs[i].density > best.density {
			best = &cs[i]
		}
	}
	if best != nil {
		return best.url
	}
	return ""
}

// isTrackingPixel reports whether an <img> declares itself 2px or smaller.
func isTrackingPixel(n *html.Node) bool {
	for _, key := range []string{"width", "height"} {
		if v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(attr(n, key)), "px")); err == nil && v <= 2 {
			return true
		}
	}
	return false
}

// buildFigures wraps each standalone image (not inside a paragraph or
// other text) in a <figure>, taking a following paragraph that is entirely
// italic as its caption; prepareImages turned captions into exactly that.
func buildFigures(body *html.Node) {
	var standalone []*html.Node
	walk(body, func(n *html.Node) {
		if n.DataAtom == atom.Img && n.Parent != nil && blockParents[n.Parent.DataAtom] {
			standalone = append(standalone, n)
		}
	})
	for _, img := range standalone {
		fig := &html.Node{Type: html.ElementNode, Data: "figure", DataAtom: atom.Figure}
		img.Parent.InsertBefore(fig, img)
		next := nextElement(img)
		img.Parent.RemoveChild(img)
		fig.AppendChild(img)
		if next != nil && isCaption(next) {
			i := firstElement(next)
			next.Parent.RemoveChild(next)
			caption := &html.Node{Type: html.ElementNode, Data: "figcaption", DataAtom: atom.Figcaption}
			for c := i.FirstChild; c != nil; c = i.FirstChild {
				i.RemoveChild(c)
				caption.AppendChild(c)
			}
			fig.AppendChild(caption)
		}
	}
}

// isCaption reports whether p is a paragraph holding only one <i> or <em>.
func isCaption(p *html.Node) bool {
	if p.DataAtom != atom.P {
		return false
	}
	i := firstElement(p)
	if i == nil || (i.DataAtom != atom.I && i.DataAtom != atom.Em) || nextElement(i) != nil {
		return false
	}
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
			return false
		}
	}
	return strings.TrimSpace(text(i)) != ""
}

// nextElement returns the next sibling element of n, skipping whitespace.
func nextElement(n *html.Node) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type == html.ElementNode {
			return s
		}
		if s.Type == html.TextNode && strings.TrimSpace(s.Data) != "" {
			return nil
		}
	}
	return nil
}

func firstElement(n *html.Node) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

// hasBlock reports whether n contains block-level elements.
func hasBlock(n *html.Node) bool {
	found := false
	walk(n, func(d *html.Node) {
		switch d.DataAtom {
		case atom.Div, atom.P, atom.Ul, atom.Ol, atom.Table, atom.Blockquote, atom.Section, atom.Figure:
			found = found || d != n
		}
	})
	return found
}

// spacedText returns the text of n with a space at every element boundary
// and whitespace collapsed, so "dialog.<span>Credit: KDE</span>" reads
// "dialog. Credit: KDE".
func spacedText(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
		if n.Type == html.ElementNode {
			b.WriteByte(' ')
		}
	}
	rec(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// unwrap replaces n with its children.
func unwrap(n *html.Node) {
	if n.Parent == nil {
		return
	}
	for c := n.FirstChild; c != nil; c = n.FirstChild {
		n.RemoveChild(c)
		n.Parent.InsertBefore(c, n)
	}
	n.Parent.RemoveChild(n)
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}
