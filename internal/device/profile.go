// Package device holds the output settings for the target e-readers.
package device

// Profile describes how images are prepared for a reading device.
type Profile struct {
	// MaxImageWidth and MaxImageHeight bound every image; larger images
	// are scaled down to fit, keeping their aspect ratio.
	MaxImageWidth, MaxImageHeight int
	// CoverWidth and CoverHeight are the size of the generated cover.
	CoverWidth, CoverHeight int
	// GrayLevels is the number of shades the panel shows. Line art is
	// quantized to this many levels.
	GrayLevels int
	// JPEGQuality is used for photos.
	JPEGQuality int
}

// EInk targets the reMarkable 2 (1404×1872) and the Kobo Clara HD
// (1072×1448). Both are 3:4 grayscale panels with 16 shades, so images are
// sized for the larger screen and the Kobo scales them down.
var EInk = Profile{
	MaxImageWidth:  1404,
	MaxImageHeight: 1872,
	CoverWidth:     1404,
	CoverHeight:    1872,
	GrayLevels:     16,
	JPEGQuality:    80,
}
