// Package assets downloads the images in an article and prepares them for
// e-ink screens.
package assets

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/vincent-petithory/dataurl"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/sync/errgroup"

	"github.com/bajdev/link2epub/internal/device"
)

// Image is a processed image to add to the book.
type Image struct {
	Filename  string // e.g. "3fa2c1d9e0ab.jpg", named by content hash
	MediaType string // image/jpeg or image/png
	Data      []byte
}

// Processor downloads and converts images.
type Processor struct {
	Client      *http.Client
	UserAgent   string
	MaxBytes    int64 // largest image accepted
	Concurrency int   // downloads at once
	Profile     device.Profile
	// Warn is called for every image that couldn't be used. It may be nil.
	Warn func(msg string)
}

// New returns a Processor using client and the e-ink profile, with the
// limits from PLAN.md: 10 MB per image and 8 downloads at once.
func New(client *http.Client, userAgent string) *Processor {
	return &Processor{
		Client:      client,
		UserAgent:   userAgent,
		MaxBytes:    10 << 20,
		Concurrency: 8,
		Profile:     device.EInk,
	}
}

// Process downloads every image in body, converts it for e-ink and points
// its <img> at the new file. The returned images are in document order,
// one per distinct image. An image that can't be used is replaced by its
// alt text, or removed if it has none; that is never an error. Only
// cancellation of ctx stops processing.
func (p *Processor) Process(ctx context.Context, body *html.Node, page *url.URL) ([]Image, error) {
	var imgs []*html.Node
	var sources []string // distinct src values, in document order
	seen := map[string]bool{}
	walk(body, func(n *html.Node) {
		if n.DataAtom != atom.Img {
			return
		}
		imgs = append(imgs, n)
		if src := attr(n, "src"); src != "" && !seen[src] {
			seen[src] = true
			sources = append(sources, src)
		}
	})

	results := make([]*encoded, len(sources))
	errs := make([]error, len(sources))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, p.Concurrency))
	for i, src := range sources {
		g.Go(func() error {
			results[i], errs[i] = p.load(gctx, src, page)
			return nil
		})
	}
	g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Name files by content hash, so the same image found under different
	// URLs is stored once.
	var images []Image
	filenames := map[string]string{} // src → filename
	byHash := map[string]bool{}
	for i, src := range sources {
		if errs[i] != nil {
			if !errors.Is(errs[i], errTooSmall) {
				p.warn(fmt.Sprintf("image %s skipped: %v", short(src), errs[i]))
			}
			continue
		}
		sum := sha1.Sum(results[i].data)
		name := hex.EncodeToString(sum[:])[:12] + results[i].ext
		filenames[src] = name
		if !byHash[name] {
			byHash[name] = true
			images = append(images, Image{Filename: name, MediaType: results[i].mediaType, Data: results[i].data})
		}
	}

	for _, n := range imgs {
		if name, ok := filenames[attr(n, "src")]; ok {
			n.Attr = []html.Attribute{{Key: "src", Val: name}, {Key: "alt", Val: attr(n, "alt")}}
		} else {
			replaceWithAlt(n)
		}
	}
	return images, nil
}

// Decode fetches src (an http(s) or data: URL) and decodes it without
// converting it, for uses such as the cover. SVG is rasterized.
func (p *Processor) Decode(ctx context.Context, src string, page *url.URL) (image.Image, error) {
	data, svg, err := p.fetch(ctx, src, page)
	if err != nil {
		return nil, err
	}
	return decodeAny(data, svg, p.Profile)
}

// load fetches src (an http(s) or data: URL) and converts it.
func (p *Processor) load(ctx context.Context, src string, page *url.URL) (*encoded, error) {
	data, svg, err := p.fetch(ctx, src, page)
	if err != nil {
		return nil, err
	}
	return convert(data, svg, p.Profile)
}

// fetch returns the bytes of src and whether they are SVG.
func (p *Processor) fetch(ctx context.Context, src string, page *url.URL) (data []byte, svg bool, err error) {
	var mediaType string
	if strings.HasPrefix(src, "data:") {
		du, err := dataurl.DecodeString(src)
		if err != nil {
			return nil, false, fmt.Errorf("bad data URL: %w", err)
		}
		data, mediaType = du.Data, du.ContentType()
	} else {
		data, mediaType, err = p.download(ctx, src, page)
		if err != nil {
			return nil, false, err
		}
	}
	return data, mediaType == "image/svg+xml" || looksLikeSVG(data), nil
}

// download fetches an image, sending the article as Referer because some
// image hosts refuse requests without one.
func (p *Processor) download(ctx context.Context, src string, page *url.URL) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, "", err
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, "", fmt.Errorf("unsupported URL scheme %q", req.URL.Scheme)
	}
	req.Header.Set("User-Agent", p.UserAgent)
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp,image/gif,image/svg+xml;q=0.9,*/*;q=0.5")
	req.Header.Set("Referer", page.String())

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("server returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, p.MaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > p.MaxBytes {
		return nil, "", fmt.Errorf("larger than %d MB", p.MaxBytes>>20)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "" && !strings.HasPrefix(mediaType, "image/") && mediaType != "application/octet-stream" {
		return nil, "", fmt.Errorf("not an image (content type %q)", mediaType)
	}
	return data, mediaType, nil
}

// looksLikeSVG reports whether data starts like an SVG document, for
// servers that send SVG with a generic content type.
func looksLikeSVG(data []byte) bool {
	head := bytes.ToLower(data[:min(len(data), 1024)])
	return bytes.Contains(head, []byte("<svg"))
}

// replaceWithAlt swaps an unusable image for "[Image: alt text]", or
// removes it when there is no alt text. A <span> is valid everywhere an
// <img> is.
func replaceWithAlt(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
		span := &html.Node{Type: html.ElementNode, Data: "span", DataAtom: atom.Span,
			Attr: []html.Attribute{{Key: "class", Val: "missing-image"}}}
		span.AppendChild(&html.Node{Type: html.TextNode, Data: "[Image: " + alt + "]"})
		parent.InsertBefore(span, n)
	}
	parent.RemoveChild(n)
}

func (p *Processor) warn(msg string) {
	if p.Warn != nil {
		p.Warn(msg)
	}
}

// short trims long image URLs (and data URLs) for warnings.
func short(src string) string {
	if strings.HasPrefix(src, "data:") {
		return "(embedded data URL)"
	}
	if len(src) > 100 {
		return src[:97] + "..."
	}
	return src
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func walk(n *html.Node, f func(*html.Node)) {
	if n.Type == html.ElementNode {
		f(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}
