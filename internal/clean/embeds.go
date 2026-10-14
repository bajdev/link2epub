package clean

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Prepare edits the downloaded page before the article is extracted:
//
//   - It makes every link absolute. go-trafilatura resolves relative links
//     as if the page URL were a folder ("../faq" on /docs/config becomes
//     /docs/faq instead of /faq), so they must be absolute before it runs.
//   - It replaces embedded YouTube and Vimeo players, which can't play on an
//     e-reader and would otherwise vanish without a trace, with a paragraph
//     linking to the video.
func Prepare(doc *html.Node, base *url.URL) {
	resolveLinks(doc, base)

	var iframes []*html.Node
	walk(doc, func(n *html.Node) {
		if n.DataAtom == atom.Iframe {
			iframes = append(iframes, n)
		}
	})
	for _, f := range iframes {
		link, ok := videoLink(attr(f, "src"), base)
		if !ok || f.Parent == nil {
			continue
		}
		label := strings.TrimSpace(attr(f, "title"))
		if label == "" {
			label = link
		}
		f.Parent.InsertBefore(videoParagraph(link, label), f)
		f.Parent.RemoveChild(f)
	}
}

var (
	youTubeEmbed = regexp.MustCompile(`^(?:www\.)?youtube(?:-nocookie)?\.com$`)
	youTubeID    = regexp.MustCompile(`^/embed/([A-Za-z0-9_-]{6,})`)
	vimeoID      = regexp.MustCompile(`^/video/([0-9]+)`)
)

// videoLink turns an embedded player's URL into the video's watch page.
func videoLink(src string, base *url.URL) (string, bool) {
	u, err := base.Parse(src)
	if err != nil {
		return "", false
	}
	switch {
	case youTubeEmbed.MatchString(u.Host):
		if m := youTubeID.FindStringSubmatch(u.Path); m != nil {
			return "https://www.youtube.com/watch?v=" + m[1], true
		}
	case u.Host == "player.vimeo.com":
		if m := vimeoID.FindStringSubmatch(u.Path); m != nil {
			return "https://vimeo.com/" + m[1], true
		}
	}
	return "", false
}

// videoParagraph builds <p>▶ Video: <a href="link">label</a></p>.
func videoParagraph(link, label string) *html.Node {
	a := &html.Node{Type: html.ElementNode, Data: "a", DataAtom: atom.A,
		Attr: []html.Attribute{{Key: "href", Val: link}}}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: label})
	p := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P}
	p.AppendChild(&html.Node{Type: html.TextNode, Data: "▶ Video: "})
	p.AppendChild(a)
	return p
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
