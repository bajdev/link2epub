package assets

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers GIF decoding; Decode returns the first frame
	"image/jpeg"
	"image/png"
	"sort"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers WebP decoding

	"github.com/bajdev/link2epub/internal/device"
)

// maxPixels rejects images that would take too much memory to decode,
// such as "decompression bombs" with a tiny file but huge dimensions.
const maxPixels = 50_000_000

// errTooSmall marks images of a couple of pixels, which are tracking pixels
// or spacers rather than content.
var errTooSmall = errors.New("image is a tracking pixel or spacer")

// encoded is a processed image ready to go into the book.
type encoded struct {
	data      []byte
	mediaType string
	ext       string
}

// convert decodes data and returns it prepared for e-ink: transparency
// flattened onto white, converted to grayscale, scaled down to fit the
// profile, and encoded as JPEG (photos) or a 16-level PNG (line art).
// WebP, GIF and SVG input are never passed through.
func convert(data []byte, svg bool, p device.Profile) (*encoded, error) {
	var img image.Image
	var err error
	if svg {
		img, err = rasterizeSVG(data, p)
	} else {
		img, err = decode(data)
	}
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() <= 2 || b.Dy() <= 2 {
		return nil, errTooSmall
	}

	gray := scaleToFit(toGray(img), p.MaxImageWidth, p.MaxImageHeight)

	var buf bytes.Buffer
	if isLineArt(gray, p.GrayLevels) {
		if err := png.Encode(&buf, quantize(gray, p.GrayLevels)); err != nil {
			return nil, err
		}
		return &encoded{data: buf.Bytes(), mediaType: "image/png", ext: ".png"}, nil
	}
	if err := jpeg.Encode(&buf, gray, &jpeg.Options{Quality: p.JPEGQuality}); err != nil {
		return nil, err
	}
	return &encoded{data: buf.Bytes(), mediaType: "image/jpeg", ext: ".jpg"}, nil
}

// decode decodes JPEG, PNG, GIF or WebP data, refusing images whose
// dimensions are too large to decode safely.
func decode(data []byte) (image.Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unsupported or damaged image: %w", err)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("%s image is too large (%d×%d)", format, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("damaged %s image: %w", format, err)
	}
	return img, nil
}

// rasterizeSVG draws an SVG at up to twice its own size, bounded by the
// profile. The e-readers don't render SVG reliably, so it becomes a bitmap.
// oksvg never loads external resources.
func rasterizeSVG(data []byte, p device.Profile) (img image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("damaged SVG image: %v", r)
		}
	}()
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.IgnoreErrorMode)
	if err != nil {
		return nil, fmt.Errorf("damaged SVG image: %w", err)
	}
	w, h := icon.ViewBox.W, icon.ViewBox.H
	if w <= 0 || h <= 0 {
		w, h = 800, 600
	}
	scale := min(2, float64(p.MaxImageWidth)/w, float64(p.MaxImageHeight)/h)
	tw, th := max(1, int(w*scale)), max(1, int(h*scale))

	icon.SetTarget(0, 0, float64(tw), float64(th))
	rgba := image.NewRGBA(image.Rect(0, 0, tw, th))
	scanner := rasterx.NewScannerGV(tw, th, rgba, rgba.Bounds())
	icon.Draw(rasterx.NewDasher(tw, th, scanner), 1)
	return rgba, nil
}

// toGray composites img over white, so transparent areas don't turn black
// on readers that ignore alpha, and converts it to 8-bit grayscale using
// the BT.601 luma weights.
func toGray(img image.Image) *image.Gray {
	b := img.Bounds()
	flat := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, b.Min, draw.Over)
	gray := image.NewGray(flat.Bounds())
	draw.Draw(gray, gray.Bounds(), flat, image.Point{}, draw.Src)
	return gray
}

// scaleToFit shrinks img to fit within maxW×maxH, keeping its aspect
// ratio. Images are never enlarged.
func scaleToFit(img *image.Gray, maxW, maxH int) *image.Gray {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	scale := min(1, float64(maxW)/float64(w), float64(maxH)/float64(h))
	if scale >= 1 {
		return img
	}
	dst := image.NewGray(image.Rect(0, 0, max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return dst
}

// lineArtCoverage is the share of pixels that must fall on the most common
// gray levels for an image to count as line art (diagrams, charts,
// screenshots) rather than a photo.
const lineArtCoverage = 0.9

// isLineArt reports whether most of img's pixels use just a few distinct
// gray values, as diagrams and screenshots do. Photos spread across many.
func isLineArt(img *image.Gray, levels int) bool {
	var hist [256]int
	for _, v := range img.Pix {
		hist[v]++
	}
	counts := hist[:]
	sort.Sort(sort.Reverse(sort.IntSlice(counts)))
	top := 0
	for _, c := range counts[:levels] {
		top += c
	}
	return float64(top) >= lineArtCoverage*float64(len(img.Pix))
}

// quantize maps img onto levels evenly spaced grays, black to white. With
// 16 levels this matches the panels exactly and the PNG encoder writes
// compact 4-bit pixels.
func quantize(img *image.Gray, levels int) *image.Paletted {
	pal := make(color.Palette, levels)
	step := 255 / (levels - 1)
	for i := range pal {
		pal[i] = color.Gray{Y: uint8(i * step)}
	}
	out := image.NewPaletted(img.Bounds(), pal)
	for i, v := range img.Pix {
		out.Pix[i] = uint8((int(v) + step/2) / step)
	}
	return out
}
