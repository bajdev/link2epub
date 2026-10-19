package cover

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/bajdev/link2epub/internal/device"
)

func decode(t *testing.T, data []byte) *image.Gray {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	gray, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("cover decodes as %T, want a grayscale JPEG", img)
	}
	if b := gray.Bounds(); b.Dx() != 1404 || b.Dy() != 1872 {
		t.Fatalf("cover is %dx%d, want 1404x1872", b.Dx(), b.Dy())
	}
	return gray
}

// darkShare is the share of pixels in r darker than mid-gray.
func darkShare(img *image.Gray, r image.Rectangle) float64 {
	dark, total := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.GrayAt(x, y).Y < 128 {
				dark++
			}
			total++
		}
	}
	return float64(dark) / float64(total)
}

func TestPlainCover(t *testing.T) {
	data, err := Render(Details{Title: "A Short Title", SiteName: "Example News", Date: "14 March 2025"}, nil, device.EInk)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	if s := darkShare(img, image.Rect(0, 0, 1404, 150)); s < 0.8 {
		t.Errorf("top band is %.0f%% dark, want a black band", s*100)
	}
	if s := darkShare(img, image.Rect(100, 300, 1300, 700)); s < 0.05 {
		t.Errorf("title area is %.1f%% dark, want visible text", s*100)
	}
	if s := darkShare(img, image.Rect(0, 1700, 1404, 1872)); s > 0.01 {
		t.Errorf("bottom edge is %.1f%% dark, want it empty", s*100)
	}
}

func TestPhotoCover(t *testing.T) {
	// A wide black photo with a white stripe in the middle: cropping to the
	// cover's shape must keep the centre.
	photo := image.NewRGBA(image.Rect(0, 0, 3000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 3000; x++ {
			c := color.RGBA{0, 0, 0, 255}
			if x > 1400 && x < 1600 {
				c = color.RGBA{255, 255, 255, 255}
			}
			photo.Set(x, y, c)
		}
	}
	data, err := Render(Details{Title: "Photo Story", SiteName: "Example", Date: "1 May 2025"}, photo, device.EInk)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	if v := img.GrayAt(702, 500).Y; v < 200 {
		t.Errorf("photo centre is %d, want the white stripe", v)
	}
	if v := img.GrayAt(100, 500).Y; v > 50 {
		t.Errorf("photo edge is %d, want black", v)
	}
	if s := darkShare(img, image.Rect(100, 1150, 1300, 1450)); s < 0.02 {
		t.Errorf("title area is %.1f%% dark, want visible text", s*100)
	}
}

func TestTransparentPhotoIsOnWhite(t *testing.T) {
	photo := image.NewNRGBA(image.Rect(0, 0, 800, 600)) // fully transparent
	data, err := Render(Details{Title: "T"}, photo, device.EInk)
	if err != nil {
		t.Fatal(err)
	}
	if v := decode(t, data).GrayAt(702, 500).Y; v < 240 {
		t.Errorf("transparent photo became %d, want white", v)
	}
}

func TestLongTitlesFit(t *testing.T) {
	long := strings.Repeat("Remarkably long headline words ", 20)
	for _, title := range []string{
		long,
		"Supercalifragilisticexpialidociousandthensomemorelettersontop",
		"",
		"日本語のタイトル",
	} {
		for _, photo := range []image.Image{nil, image.NewGray(image.Rect(0, 0, 600, 400))} {
			if _, err := Render(Details{Title: title, SiteName: strings.Repeat("Site ", 30)}, photo, device.EInk); err != nil {
				t.Errorf("Render(%q): %v", title, err)
			}
		}
	}
}

func TestFitTitle(t *testing.T) {
	// Short titles get the largest size; long ones shrink, and lines never
	// exceed the width or the box.
	face, lines := fitTitle("Hi", 1200, 800, 140, 64)
	if face.Metrics().Height.Ceil() < 140 || len(lines) != 1 {
		t.Errorf("short title: height %d, lines %q", face.Metrics().Height.Ceil(), lines)
	}
	title := strings.Repeat("word ", 200)
	face, lines = fitTitle(title, 1200, 800, 140, 64)
	if len(lines)*int(float64(face.Metrics().Height.Ceil())*1.0) > 800*1.2 {
		t.Errorf("long title overflows: %d lines", len(lines))
	}
	if !strings.HasSuffix(lines[len(lines)-1], "…") {
		t.Errorf("truncated title should end with an ellipsis: %q", lines[len(lines)-1])
	}
	if fitted, _ := fitTitle("", 1200, 800, 140, 64); fitted == nil {
		t.Error("empty title should still render")
	}
}

func TestSubtitle(t *testing.T) {
	tests := []struct {
		d    Details
		want string
	}{
		{Details{SiteName: "Site", Date: "1 May 2025"}, "Site · 1 May 2025"},
		{Details{SiteName: "Site"}, "Site"},
		{Details{Date: "1 May 2025"}, "1 May 2025"},
		{Details{}, ""},
	}
	for _, tt := range tests {
		if got := subtitle(tt.d); got != tt.want {
			t.Errorf("subtitle(%+v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestContentsCover(t *testing.T) {
	titles := []string{"How e-ink works", "A history of the printing press", "Why paper still matters"}
	data, err := Render(Details{Title: "Weekend reading", Contents: titles}, nil, device.EInk)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	if s := darkShare(img, image.Rect(0, 0, 1404, 150)); s < 0.8 {
		t.Errorf("top band is %.0f%% dark, want a black band", s*100)
	}
	if s := darkShare(img, image.Rect(100, 300, 1300, 500)); s < 0.05 {
		t.Errorf("title area is %.1f%% dark, want visible text", s*100)
	}
	if s := darkShare(img, image.Rect(100, 700, 1300, 1100)); s < 0.01 {
		t.Errorf("contents area is %.1f%% dark, want the article titles", s*100)
	}

	// A long list stays within the margins.
	many := make([]string, 50)
	for i := range many {
		many[i] = strings.Repeat("A long article title ", 5)
	}
	data, err = Render(Details{Title: strings.Repeat("Long book title ", 10), Contents: many}, nil, device.EInk)
	if err != nil {
		t.Fatal(err)
	}
	if s := darkShare(decode(t, data), image.Rect(0, 1790, 1404, 1872)); s > 0.01 {
		t.Errorf("bottom margin is %.1f%% dark, want the list to stop above it", s*100)
	}
}

func TestFitContents(t *testing.T) {
	titles := []string{"a", "b", "c", "d", "e"}
	if got := fitContents(titles, 5); len(got) != 5 {
		t.Errorf("fitContents(5 titles, 5) = %q, want all of them", got)
	}
	got := fitContents(titles, 3)
	if want := []string{"a", "b", "and 3 more"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("fitContents(5 titles, 3) = %q, want %q", got, want)
	}
	if titles[2] != "c" {
		t.Errorf("fitContents changed its input: %q", titles)
	}
}
