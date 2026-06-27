// Package imaging processes uploaded message images: it decodes common formats,
// applies EXIF orientation, downscales so the longer edge fits a maximum while
// preserving aspect ratio (never upscaling), and re-encodes as JPEG. Re-encoding
// strips metadata (EXIF/GPS) as a side effect.
package imaging

import (
	"bytes"
	"fmt"

	"github.com/disintegration/imaging"
)

const (
	// MaxDim is the maximum length, in pixels, of a processed image's longer edge.
	MaxDim = 1000
	// Quality is the JPEG encoder quality (1-100) used for processed images.
	Quality = 82
	// ContentType is the MIME type of every processed image.
	ContentType = "image/jpeg"
)

// Process decodes data (JPEG/PNG/GIF/TIFF/BMP), applies EXIF orientation, fits it
// within a maxDim×maxDim box without upscaling, and re-encodes it as JPEG at the
// given quality. It returns the encoded bytes and the final pixel dimensions.
func Process(data []byte, maxDim, quality int) (out []byte, width, height int, err error) {
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}

	b := img.Bounds()
	if b.Dx() > maxDim || b.Dy() > maxDim {
		// Fit downscales to fit inside the box while preserving aspect ratio.
		img = imaging.Fit(img, maxDim, maxDim, imaging.Lanczos)
		b = img.Bounds()
	}

	var buf bytes.Buffer
	if err := imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
		return nil, 0, 0, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), b.Dx(), b.Dy(), nil
}
