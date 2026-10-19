package assets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"
)

// photo returns a w×h colour image with smooth gradients and noise, which
// uses many gray levels like a photograph.
func photo(w, h int) image.Image {
	r := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := uint8(r.Intn(40))
			img.Set(x, y, color.RGBA{uint8(x*200/w) + n, uint8(y*200/h) + n, 120 + n, 255})
		}
	}
	return img
}

// lineArt returns a transparent image with a black frame and a red bar,
// like a diagram exported with a transparent background.
func lineArt(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x < 4 || y < 4 || x >= w-4 || y >= h-4:
				img.Set(x, y, color.NRGBA{0, 0, 0, 255})
			case y > h/2-10 && y < h/2+10:
				img.Set(x, y, color.NRGBA{220, 30, 30, 255})
			}
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// animatedGIF has a white first frame and a black second frame.
func animatedGIF() []byte {
	pal := color.Palette{color.White, color.Black}
	frame := func(c uint8) *image.Paletted {
		f := image.NewPaletted(image.Rect(0, 0, 50, 50), pal)
		for i := range f.Pix {
			f.Pix[i] = c
		}
		f.Pix[0] = 1 - c // one odd pixel so the frame isn't entirely flat
		return f
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{frame(0), frame(1)}, Delay: []int{10, 10}}); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// pngBomb is a valid PNG header claiming 100000×100000 pixels.
func pngBomb() []byte {
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 100000)
	binary.BigEndian.PutUint32(ihdr[4:], 100000)
	ihdr[8], ihdr[9] = 8, 2 // 8-bit RGB
	binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

const svgDoc = `<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100" width="200" height="100">
  <rect x="10" y="10" width="180" height="80" fill="none" stroke="black" stroke-width="4"/>
  <circle cx="100" cy="50" r="30" fill="#3366cc"/>
</svg>`

type testServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests map[string]int
	referers map[string]string
}

type file struct {
	contentType string
	body        []byte
}

// files are the images the test server offers, generated once per run.
var files = sync.OnceValue(func() map[string]file {
	webp, err := os.ReadFile("../../testdata/images/gradient.webp")
	if err != nil {
		panic(err)
	}
	return map[string]file{
		"/photo.jpg":     {"image/jpeg", encodeJPEG(photo(3000, 2000))},
		"/small.jpg":     {"image/jpeg", encodeJPEG(photo(300, 200))},
		"/tall.jpg":      {"image/jpeg", encodeJPEG(photo(1000, 4000))},
		"/diagram.png":   {"image/png", encodePNG(lineArt(600, 400))},
		"/copy.png":      {"image/png", encodePNG(lineArt(600, 400))},
		"/anim.gif":      {"image/gif", animatedGIF()},
		"/drawing.svg":   {"image/svg+xml", []byte(svgDoc)},
		"/drawing-bin":   {"application/octet-stream", []byte(svgDoc)},
		"/gradient.webp": {"image/webp", webp},
		"/pixel.png":     {"image/png", encodePNG(image.NewGray(image.Rect(0, 0, 1, 1)))},
		"/bomb.png":      {"image/png", pngBomb()},
		"/page.html":     {"text/html", []byte("<html>not an image</html>")},
		"/broken.jpg":    {"image/jpeg", []byte("not really a jpeg")},
	}
})

func newServer(t *testing.T) *testServer {
	t.Helper()
	files := files()
	s := &testServer{requests: map[string]int{}, referers: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.URL.Path]++
		s.referers[r.URL.Path] = r.Referer()
		s.mu.Unlock()
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", f.contentType)
		w.Write(f.body)
	}))
	t.Cleanup(s.Close)
	return s
}

type run struct {
	images   []Image
	body     string
	warnings []string
}

// process runs Process on markup (with {S} replaced by the server URL).
func process(t *testing.T, s *testServer, markup string) run {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<body>" + strings.ReplaceAll(markup, "{S}", s.URL) + "</body>"))
	if err != nil {
		t.Fatal(err)
	}
	body := doc.FirstChild.LastChild
	page, _ := url.Parse(s.URL + "/article")
	var r run
	p := New(s.Client(), "test-agent")
	p.Warn = func(msg string) { r.warnings = append(r.warnings, msg) }
	r.images, err = p.Process(context.Background(), body, page)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		html.Render(&b, c)
	}
	r.body = b.String()
	return r
}

func decodeImage(t *testing.T, img Image) image.Image {
	t.Helper()
	m, format, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatalf("%s doesn't decode: %v", img.Filename, err)
	}
	if want := strings.TrimPrefix(img.MediaType, "image/"); format != want {
		t.Errorf("%s: format %s, media type %s", img.Filename, format, img.MediaType)
	}
	return m
}

// assertGray fails unless every pixel of m is a shade of gray.
func assertGray(t *testing.T, name string, m image.Image) {
	t.Helper()
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 7 {
		for x := b.Min.X; x < b.Max.X; x += 7 {
			r, g, bl, _ := m.At(x, y).RGBA()
			if r != g || g != bl {
				t.Fatalf("%s: pixel (%d,%d) is not gray: %d,%d,%d", name, x, y, r, g, bl)
			}
		}
	}
}

func TestPhotoBecomesGrayJPEGWithinScreen(t *testing.T) {
	s := newServer(t)
	r := process(t, s, `<img src="{S}/photo.jpg" alt="A photo">`)
	if len(r.images) != 1 {
		t.Fatalf("got %d images, want 1; warnings: %v", len(r.images), r.warnings)
	}
	img := r.images[0]
	if img.MediaType != "image/jpeg" || !strings.HasSuffix(img.Filename, ".jpg") {
		t.Errorf("photo stored as %s (%s), want JPEG", img.Filename, img.MediaType)
	}
	m := decodeImage(t, img)
	if b := m.Bounds(); b.Dx() != 1404 || b.Dy() != 936 {
		t.Errorf("photo is %dx%d, want 1404x936", b.Dx(), b.Dy())
	}
	if _, ok := m.(*image.Gray); !ok {
		t.Errorf("photo decodes as %T, want a grayscale JPEG", m)
	}
	if want := `<img src="` + img.Filename + `" alt="A photo"/>`; r.body != want {
		t.Errorf("body = %q, want %q", r.body, want)
	}
	if ref := s.referers["/photo.jpg"]; ref != s.URL+"/article" {
		t.Errorf("Referer = %q, want the article URL", ref)
	}
}

func TestTallImageFitsHeight(t *testing.T) {
	r := process(t, newServer(t), `<img src="{S}/tall.jpg">`)
	m := decodeImage(t, r.images[0])
	if b := m.Bounds(); b.Dx() != 468 || b.Dy() != 1872 {
		t.Errorf("tall image is %dx%d, want 468x1872", b.Dx(), b.Dy())
	}
}

func TestSmallImageIsNotEnlarged(t *testing.T) {
	r := process(t, newServer(t), `<img src="{S}/small.jpg">`)
	m := decodeImage(t, r.images[0])
	if b := m.Bounds(); b.Dx() != 300 || b.Dy() != 200 {
		t.Errorf("small image is %dx%d, want 300x200", b.Dx(), b.Dy())
	}
}

func TestLineArtBecomes16LevelPNGOnWhite(t *testing.T) {
	r := process(t, newServer(t), `<img src="{S}/diagram.png" alt="Diagram">`)
	if len(r.images) != 1 {
		t.Fatalf("got %d images; warnings: %v", len(r.images), r.warnings)
	}
	img := r.images[0]
	if img.MediaType != "image/png" {
		t.Fatalf("diagram stored as %s, want PNG", img.MediaType)
	}
	m := decodeImage(t, img)
	assertGray(t, img.Filename, m)
	pal, ok := m.(*image.Paletted)
	if !ok || len(pal.Palette) != 16 {
		t.Fatalf("diagram decodes as %T, want a 16-colour paletted PNG", m)
	}
	// The transparent background must be white, not black.
	if r, _, _, _ := m.At(300, 100).RGBA(); r>>8 != 255 {
		t.Errorf("transparent area became %d, want white (255)", r>>8)
	}
	if r, _, _, _ := m.At(1, 1).RGBA(); r>>8 != 0 {
		t.Errorf("black frame became %d, want black (0)", r>>8)
	}
}

func TestFormatsAreConverted(t *testing.T) {
	s := newServer(t)
	for _, path := range []string{"/gradient.webp", "/anim.gif", "/drawing.svg", "/drawing-bin"} {
		t.Run(path, func(t *testing.T) {
			r := process(t, s, `<img src="{S}`+path+`">`)
			if len(r.images) != 1 {
				t.Fatalf("got %d images; warnings: %v", len(r.images), r.warnings)
			}
			img := r.images[0]
			if img.MediaType != "image/jpeg" && img.MediaType != "image/png" {
				t.Errorf("stored as %s, want JPEG or PNG", img.MediaType)
			}
			assertGray(t, path, decodeImage(t, img))
		})
	}
}

func TestAnimatedGIFUsesFirstFrame(t *testing.T) {
	r := process(t, newServer(t), `<img src="{S}/anim.gif">`)
	m := decodeImage(t, r.images[0])
	if v, _, _, _ := m.At(25, 25).RGBA(); v>>8 != 255 {
		t.Errorf("pixel = %d, want white from the first frame", v>>8)
	}
}

func TestSVGIsRasterized(t *testing.T) {
	r := process(t, newServer(t), `<img src="{S}/drawing.svg">`)
	m := decodeImage(t, r.images[0])
	// Drawn at twice its 200×100 size.
	if b := m.Bounds(); b.Dx() != 400 || b.Dy() != 200 {
		t.Errorf("SVG rendered at %dx%d, want 400x200", b.Dx(), b.Dy())
	}
	if v, _, _, _ := m.At(200, 100).RGBA(); v>>8 > 200 {
		t.Errorf("circle centre is %d, want a dark gray", v>>8)
	}
}

func TestDataURLImage(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(encodePNG(lineArt(100, 80)))
	r := process(t, newServer(t), `<img src="data:image/png;base64,`+data+`" alt="inline">`)
	if len(r.images) != 1 || r.images[0].MediaType != "image/png" {
		t.Fatalf("images = %+v; warnings: %v", r.images, r.warnings)
	}
}

func TestDuplicatesAreStoredOnce(t *testing.T) {
	s := newServer(t)
	r := process(t, s, `<img src="{S}/diagram.png"><p><img src="{S}/copy.png"></p><img src="{S}/diagram.png">`)
	if len(r.images) != 1 {
		t.Fatalf("got %d images, want 1 (identical content)", len(r.images))
	}
	if strings.Count(r.body, r.images[0].Filename) != 3 {
		t.Errorf("body = %q, want all three images pointing at %s", r.body, r.images[0].Filename)
	}
	if n := s.requests["/diagram.png"]; n != 1 {
		t.Errorf("diagram.png downloaded %d times, want 1", n)
	}
}

func TestUnusableImages(t *testing.T) {
	s := newServer(t)
	tests := []struct {
		name, markup, want, warning string
	}{
		{"not found, with alt", `<p>See <img src="{S}/missing.jpg" alt="the chart">.</p>`,
			`<p>See <span class="missing-image">[Image: the chart]</span>.</p>`, "404 Not Found"},
		{"not found, without alt", `<p>See <img src="{S}/missing.jpg">.</p>`, `<p>See .</p>`, "404"},
		{"not an image", `<img src="{S}/page.html" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "not an image"},
		{"damaged", `<img src="{S}/broken.jpg" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "unsupported or damaged"},
		{"decompression bomb", `<img src="{S}/bomb.png" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "too large"},
		{"unsupported scheme", `<img src="ftp://example.com/a.png" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "unsupported URL scheme"},
		// Local files are never read, whatever form the path takes.
		{"file URL", `<img src="file:///etc/hosts" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "unsupported URL scheme"},
		{"file URL with host", `<img src="file://localhost/etc/hosts" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "unsupported URL scheme"},
		{"absolute path", `<img src="/etc/hosts" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "unsupported URL scheme"},
		{"Windows path", `<img src="C:\Windows\win.ini" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "skipped"},
		{"UNC path", `<img src="\\server\share\a.png" alt="x">`, `<span class="missing-image">[Image: x]</span>`, "skipped"},
		// Tracking pixels are dropped without a warning.
		{"tracking pixel", `<p>a<img src="{S}/pixel.png">b</p>`, `<p>ab</p>`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := process(t, s, tt.markup)
			if len(r.images) != 0 {
				t.Errorf("got %d images, want 0", len(r.images))
			}
			if r.body != tt.want {
				t.Errorf("body = %q, want %q", r.body, tt.want)
			}
			switch {
			case tt.warning == "" && len(r.warnings) != 0:
				t.Errorf("warnings = %v, want none", r.warnings)
			case tt.warning != "" && (len(r.warnings) != 1 || !strings.Contains(r.warnings[0], tt.warning)):
				t.Errorf("warnings = %v, want one containing %q", r.warnings, tt.warning)
			}
		})
	}
}

func TestLargeDownloadsAreRefused(t *testing.T) {
	s := newServer(t)
	doc, _ := html.Parse(strings.NewReader(`<body><img src="` + s.URL + `/photo.jpg" alt="x"></body>`))
	page, _ := url.Parse(s.URL)
	var warnings []string
	p := New(s.Client(), "test-agent")
	p.MaxBytes = 1000
	p.Warn = func(msg string) { warnings = append(warnings, msg) }
	images, err := p.Process(context.Background(), doc, page)
	if err != nil || len(images) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "larger than") {
		t.Errorf("images = %d, err = %v, warnings = %v", len(images), err, warnings)
	}
}

func TestCancelled(t *testing.T) {
	s := newServer(t)
	doc, _ := html.Parse(strings.NewReader(`<body><img src="` + s.URL + `/photo.jpg"></body>`))
	page, _ := url.Parse(s.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(s.Client(), "test-agent").Process(ctx, doc, page); err == nil {
		t.Error("Process succeeded after cancellation, want an error")
	}
}

func TestDecodeRefusesLocalFiles(t *testing.T) {
	page, _ := url.Parse("https://example.com/article")
	p := New(http.DefaultClient, "test-agent")
	for _, src := range []string{"file:///etc/hosts", "/etc/hosts", `C:\Windows\win.ini`} {
		if _, err := p.Decode(context.Background(), src, page); err == nil {
			t.Errorf("Decode(%q) succeeded", src)
		}
	}
}
