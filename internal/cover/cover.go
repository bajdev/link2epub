// Package cover draws book covers for e-ink readers.
package cover

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"slices"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/bajdev/link2epub/internal/assets"
	"github.com/bajdev/link2epub/internal/device"
)

// Details is the text shown on a cover.
type Details struct {
	Title    string
	SiteName string // may be empty
	Date     string // already formatted; may be empty
	// Contents lists the article titles of a book of several articles.
	// When set, the cover lists them and SiteName and Date are ignored.
	Contents []string
}

// Render draws a grayscale JPEG cover at the profile's cover size. With a
// photo, the photo fills the top of the cover and the title sits on white
// below it; without one, the cover is typographic. Either way the title is
// large, because library thumbnails on e-ink readers are small.
func Render(d Details, photo image.Image, p device.Profile) ([]byte, error) {
	w, h := p.CoverWidth, p.CoverHeight
	img := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	margin := w / 14
	textWidth := w - 2*margin
	sub := subtitle(d)

	if len(d.Contents) > 0 {
		drawContents(img, d, margin)
	} else if photo != nil {
		photoHeight := h * 3 / 5
		drawPhoto(img, image.Rect(0, 0, w, photoHeight), photo)
		top := photoHeight + margin*2/3
		bottom := h - margin
		if sub != "" {
			bottom -= drawLines(img, regular(), []string{sub}, 46, margin, bottom-46, color.Black, true)
			bottom -= margin / 3
		}
		face, lines := fitTitle(d.Title, textWidth, bottom-top, 104, 52)
		drawWrapped(img, face, lines, margin, top)
	} else {
		// A black band across the top with the site name, a large title, and
		// the date at the bottom.
		band := h / 9
		draw.Draw(img, image.Rect(0, 0, w, band), image.Black, image.Point{}, draw.Src)
		if d.SiteName != "" {
			name := truncate(regular(), 54, d.SiteName, textWidth)
			drawLines(img, regular(), []string{name}, 54, margin, band/2+18, color.White, false)
		}
		top := band + margin*3/2
		bottom := h - margin*2
		face, lines := fitTitle(d.Title, textWidth, bottom-top, 140, 64)
		end := drawWrapped(img, face, lines, margin, top)
		rule := end + margin*3/4
		draw.Draw(img, image.Rect(margin, rule, margin+w/4, rule+8), image.Black, image.Point{}, draw.Src)
		if d.Date != "" {
			drawLines(img, regular(), []string{d.Date}, 46, margin, rule+margin, color.Black, false)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawContents draws the cover of a book of several articles: a black band
// with the number of articles, the title, and as many article titles as
// fit, ending with "and N more" when some don't.
func drawContents(img *image.Gray, d Details, margin int) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	textWidth := w - 2*margin
	band := h / 9
	draw.Draw(img, image.Rect(0, 0, w, band), image.Black, image.Point{}, draw.Src)
	drawLines(img, regular(), []string{fmt.Sprintf("%d articles", len(d.Contents))}, 54, margin, band/2+18, color.White, false)

	top := band + margin*3/2
	face, lines := fitTitle(d.Title, textWidth, h*2/7, 120, 64)
	end := drawWrapped(img, face, lines, margin, top)
	rule := end + margin*3/4
	draw.Draw(img, image.Rect(margin, rule, margin+w/4, rule+8), image.Black, image.Point{}, draw.Src)

	const size, lineHeight = 44, 66
	first := rule + margin + size // baseline of the first entry
	fits := max(1, (h-margin-first)/lineHeight+1)
	for i, title := range fitContents(d.Contents, fits) {
		line := truncate(regular(), size, strings.Join(strings.Fields(title), " "), textWidth)
		drawLines(img, regular(), []string{line}, size, margin, first+i*lineHeight, color.Black, false)
	}
}

// fitContents returns at most n lines listing titles. When they don't all
// fit, the last line says how many were left out.
func fitContents(titles []string, n int) []string {
	if len(titles) <= n {
		return titles
	}
	return append(slices.Clone(titles[:n-1]), fmt.Sprintf("and %d more", len(titles)-(n-1)))
}

// subtitle is "Site · Date", skipping whichever is missing.
func subtitle(d Details) string {
	var parts []string
	for _, s := range []string{d.SiteName, d.Date} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// drawPhoto scales photo to cover r completely, cropping the overflow
// evenly, and draws it in grayscale.
func drawPhoto(dst *image.Gray, r image.Rectangle, photo image.Image) {
	gray := assets.ToGray(photo)
	sw, sh := gray.Bounds().Dx(), gray.Bounds().Dy()
	scale := max(float64(r.Dx())/float64(sw), float64(r.Dy())/float64(sh))
	cw, ch := int(float64(r.Dx())/scale), int(float64(r.Dy())/scale)
	x0, y0 := (sw-cw)/2, (sh-ch)/2
	xdraw.CatmullRom.Scale(dst, r, gray, image.Rect(x0, y0, x0+cw, y0+ch), xdraw.Src, nil)
}

// fitTitle picks the largest font size, from maxSize down to minSize, at
// which the title wraps into the given box. At the smallest size, lines
// that don't fit are dropped and the last kept line ends with "…".
func fitTitle(title string, width, height int, maxSize, minSize float64) (font.Face, []string) {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = "Untitled"
	}
	for size := maxSize; ; size -= 4 {
		face := faceFor(bold(), size)
		lines := wrap(face, title, width)
		lineHeight := int(size * 1.18)
		if len(lines)*lineHeight <= height || size-4 < minSize {
			if keep := max(1, height/lineHeight); len(lines) > keep {
				lines = lines[:keep]
				lines[keep-1] = truncate(bold(), size, lines[keep-1]+" …", width)
			}
			return face, lines
		}
	}
}

// wrap breaks text into lines no wider than width, splitting words that
// are too long on their own.
func wrap(face font.Face, text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		for font.MeasureString(face, word).Ceil() > width {
			// Split an overlong word at the widest prefix that fits.
			n := len([]rune(word)) - 1
			for n > 1 && font.MeasureString(face, string([]rune(word)[:n])).Ceil() > width {
				n--
			}
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, string([]rune(word)[:n]))
			word = string([]rune(word)[n:])
		}
		candidate := strings.TrimSpace(line + " " + word)
		if line != "" && font.MeasureString(face, candidate).Ceil() > width {
			lines = append(lines, line)
			candidate = word
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// truncate shortens s with "…" until it fits within width.
func truncate(f *opentype.Font, size float64, s string, width int) string {
	face := faceFor(f, size)
	if font.MeasureString(face, s).Ceil() <= width {
		return s
	}
	r := []rune(strings.TrimSuffix(s, " …"))
	for len(r) > 1 && font.MeasureString(face, string(r)+"…").Ceil() > width {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r)) + "…"
}

// drawWrapped draws lines starting with the first line's top at y, and
// returns the y just below the last line.
func drawWrapped(dst *image.Gray, face font.Face, lines []string, x, y int) int {
	m := face.Metrics()
	lineHeight := (m.Ascent + m.Descent).Ceil() * 118 / 100
	baseline := y + m.Ascent.Ceil()
	for _, line := range lines {
		(&font.Drawer{Dst: dst, Src: image.Black, Face: face, Dot: fixed.P(x, baseline)}).DrawString(line)
		baseline += lineHeight
	}
	return baseline - lineHeight + m.Descent.Ceil()
}

// drawLines draws single lines at baseline y in the given font and colour,
// and returns the height used. With fromBottom, y is the bottom edge.
func drawLines(dst *image.Gray, f *opentype.Font, lines []string, size float64, x, y int, c color.Color, fromBottom bool) int {
	face := faceFor(f, size)
	m := face.Metrics()
	if fromBottom {
		y -= m.Descent.Ceil()
	}
	for _, line := range lines {
		(&font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}).DrawString(line)
	}
	return (m.Ascent + m.Descent).Ceil()
}

var (
	bold    = sync.OnceValue(func() *opentype.Font { return mustParse(gobold.TTF) })
	regular = sync.OnceValue(func() *opentype.Font { return mustParse(goregular.TTF) })
)

// mustParse parses an embedded font; it can only fail if the font data is
// corrupt, which is a build problem.
func mustParse(ttf []byte) *opentype.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

func faceFor(f *opentype.Font, size float64) font.Face {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err) // only fails for invalid options, which are constant here
	}
	return face
}
