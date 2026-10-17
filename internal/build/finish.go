package build

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/bajdev/link2epub/internal/extract"
)

// chapterFile is the chapter's filename; go-epub stores it under xhtml/.
const chapterFile = "chapter.xhtml"

// Paths go-epub uses inside the book. finish fails if any is missing, so a
// change in go-epub's layout shows up in tests rather than in broken books.
const (
	opfPath = "EPUB/package.opf"
	navPath = "EPUB/nav.xhtml"
	ncxPath = "EPUB/toc.ncx"
)

// heading is a section heading listed in the table of contents.
type heading struct {
	level int // 2 or 3
	text  string
	id    string
}

// anchorHeadings gives every <h2> and <h3> in body an id, so the table of
// contents can link to it, and returns them in order. The sanitizer removed
// the page's own ids, so these can't clash.
func anchorHeadings(body *html.Node) []heading {
	var hs []heading
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.DataAtom == atom.H2 || n.DataAtom == atom.H3) {
			if text := strings.Join(strings.Fields(textOf(n)), " "); text != "" {
				id := "sec-" + strconv.Itoa(len(hs)+1)
				n.Attr = append(n.Attr, html.Attribute{Key: "id", Val: id})
				level := 2
				if n.DataAtom == atom.H3 {
					level = 3
				}
				hs = append(hs, heading{level: level, text: text, id: id})
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)
	return hs
}

// finish rewrites the files go-epub gets wrong or can't express:
//
//   - the OPF gains dc:publisher (the site), dc:source (the URL) and
//     dc:date (publication date);
//   - the EPUB 3 nav document and the NCX list the article's sections.
//     go-epub can only list whole files, and its NCX has no dtb:uid, which
//     Kobo's RMSDK renderer reads for plain EPUBs.
func finish(book []byte, id string, a *extract.Article, headings []heading) ([]byte, error) {
	return rewriteZip(book, map[string]func([]byte) []byte{
		opfPath: func(opf []byte) []byte { return addMetadata(opf, a) },
		navPath: func([]byte) []byte { return navDocument(a, headings) },
		ncxPath: func([]byte) []byte { return ncxDocument(id, a, headings) },
	})
}

// rewriteZip copies the zip archive data, replacing the named entries with
// the output of their edit functions. Other entries, including the
// uncompressed mimetype that must come first, are copied byte for byte.
func rewriteZip(data []byte, edits map[string]func([]byte) []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	done := map[string]bool{}
	for _, f := range zr.File {
		edit, ok := edits[f.Name]
		if !ok {
			if err := zw.Copy(f); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		old, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(edit(old)); err != nil {
			return nil, err
		}
		done[f.Name] = true
	}
	for name := range edits {
		if !done[name] {
			return nil, fmt.Errorf("book is missing %s", name)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// addMetadata inserts publisher, source and date elements into the OPF
// metadata.
func addMetadata(opf []byte, a *extract.Article) []byte {
	var extra strings.Builder
	element := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&extra, "    <%s>%s</%s>\n", name, esc(value), name)
		}
	}
	element("dc:publisher", a.SiteName)
	element("dc:source", a.URL)
	if !a.Published.IsZero() {
		element("dc:date", a.Published.UTC().Format("2006-01-02"))
	}
	s := string(opf)
	marker := `<meta property="dcterms:modified">`
	i := strings.Index(s, marker)
	if i < 0 {
		i = strings.Index(s, "</metadata>")
	}
	if i < 0 {
		return opf
	}
	// Insert at the start of the marker's line, keeping the indentation.
	line := strings.LastIndex(s[:i], "\n") + 1
	return []byte(s[:line] + extra.String() + s[line:])
}

// tocEntry is a node in the table of contents tree.
type tocEntry struct {
	text, href string
	children   []*tocEntry
}

// toc returns the table of contents: the article, with its <h2> sections
// and their <h3> subsections beneath it. Paths are relative to the EPUB
// folder, where the nav document and NCX live.
func toc(a *extract.Article, headings []heading) *tocEntry {
	href := "xhtml/" + chapterFile
	root := &tocEntry{text: a.Title, href: href}
	var section *tocEntry
	for _, h := range headings {
		e := &tocEntry{text: h.text, href: href + "#" + h.id}
		if h.level == 3 && section != nil {
			section.children = append(section.children, e)
			continue
		}
		root.children = append(root.children, e)
		if h.level == 2 {
			section = e
		}
	}
	return root
}

func navDocument(a *extract.Article, headings []heading) []byte {
	var b strings.Builder
	var list func(es []*tocEntry, indent string)
	list = func(es []*tocEntry, indent string) {
		b.WriteString(indent + "<ol>\n")
		for _, e := range es {
			fmt.Fprintf(&b, "%s  <li><a href=\"%s\">%s</a>", indent, esc(e.href), esc(e.text))
			if len(e.children) > 0 { // an empty <ol> is invalid
				b.WriteString("\n")
				list(e.children, indent+"    ")
				b.WriteString(indent + "  ")
			}
			b.WriteString("</li>\n")
		}
		b.WriteString(indent + "</ol>\n")
	}
	lang := esc(a.Language)
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="%s" xml:lang="%s">
  <head>
    <title>%s</title>
  </head>
  <body>
    <nav epub:type="toc" id="toc">
      <h1>Contents</h1>
`, lang, lang, esc(a.Title))
	list([]*tocEntry{toc(a, headings)}, "      ")
	b.WriteString("    </nav>\n  </body>\n</html>\n")
	return []byte(b.String())
}

func ncxDocument(id string, a *extract.Article, headings []heading) []byte {
	root := toc(a, headings)
	depth := 1
	for _, e := range root.children {
		depth = max(depth, 2)
		if len(e.children) > 0 {
			depth = 3
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1" xml:lang="%s">
  <head>
    <meta name="dtb:uid" content="%s"/>
    <meta name="dtb:depth" content="%d"/>
    <meta name="dtb:totalPageCount" content="0"/>
    <meta name="dtb:maxPageNumber" content="0"/>
  </head>
  <docTitle>
    <text>%s</text>
  </docTitle>
  <navMap>
`, esc(a.Language), esc(id), depth, esc(a.Title))
	order := 0
	var point func(e *tocEntry, indent string)
	point = func(e *tocEntry, indent string) {
		order++
		fmt.Fprintf(&b, "%s<navPoint id=\"navpoint-%d\" playOrder=\"%d\">\n", indent, order, order)
		fmt.Fprintf(&b, "%s  <navLabel><text>%s</text></navLabel>\n", indent, esc(e.text))
		fmt.Fprintf(&b, "%s  <content src=\"%s\"/>\n", indent, esc(e.href))
		for _, c := range e.children {
			point(c, indent+"  ")
		}
		fmt.Fprintf(&b, "%s</navPoint>\n", indent)
	}
	point(root, "    ")
	b.WriteString("  </navMap>\n</ncx>\n")
	return []byte(b.String())
}

// esc escapes s for XML text and attribute values.
func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func textOf(n *html.Node) string {
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
