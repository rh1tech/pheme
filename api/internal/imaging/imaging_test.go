package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// testPNG returns a PNG-encoded image of the given size with a non-uniform
// pattern (so JPEG re-encoding has something to work with).
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestProcessDownscalesLandscapeAndOutputsJPEG(t *testing.T) {
	in := testPNG(t, 1600, 900)
	out, w, h, err := Process(in, MaxDim, Quality)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if w != MaxDim {
		t.Fatalf("width = %d, want %d", w, MaxDim)
	}
	// Aspect ratio preserved: 1600x900 → 1000x562/563.
	if h < 560 || h > 564 {
		t.Fatalf("height = %d, want ~562 (aspect preserved)", h)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("output format = %q, want jpeg", format)
	}
	if cfg.Width != w || cfg.Height != h {
		t.Fatalf("encoded dims = %dx%d, want %dx%d", cfg.Width, cfg.Height, w, h)
	}
}

func TestProcessDownscalesPortrait(t *testing.T) {
	in := testPNG(t, 800, 2000)
	_, w, h, err := Process(in, MaxDim, Quality)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if h != MaxDim {
		t.Fatalf("height = %d, want %d (longer edge)", h, MaxDim)
	}
	if w != 400 {
		t.Fatalf("width = %d, want 400 (aspect preserved)", w)
	}
}

func TestProcessDoesNotUpscaleSmallImage(t *testing.T) {
	in := testPNG(t, 320, 240)
	out, w, h, err := Process(in, MaxDim, Quality)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if w != 320 || h != 240 {
		t.Fatalf("dims = %dx%d, want 320x240 (no upscale)", w, h)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output is not valid jpeg: %v", err)
	}
}

func TestProcessRejectsNonImage(t *testing.T) {
	if _, _, _, err := Process([]byte("not an image"), MaxDim, Quality); err == nil {
		t.Fatal("expected error for non-image input")
	}
}
