package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestGenerateScalesToMaxDim(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for x := 0; x < 100; x++ {
		for y := 0; y < 50; y++ {
			src.SetRGBA(x, y, color.RGBA{200, 60, 40, 255})
		}
	}

	out, w, h, err := Generate(bytes.NewReader(pngBytes(t, src)), 30, 80)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if w != 30 || h != 15 {
		t.Fatalf("dims = %dx%d, want 30x15", w, h)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	if got := decoded.Bounds().Dx(); got != 30 {
		t.Fatalf("decoded width = %d, want 30", got)
	}
}

func TestGenerateCompositesAlphaOntoWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 0})
		}
	}

	out, _, _, err := Generate(bytes.NewReader(pngBytes(t, img)), 10, 80)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r < 64000 || g < 64000 || b < 64000 {
		t.Fatalf("transparent pixel was not composited to white: %d,%d,%d", r, g, b)
	}
}

func TestGenerateCorruptReturnsError(t *testing.T) {
	_, _, _, err := Generate(bytes.NewReader([]byte("not-an-image")), 640, 80)
	if err == nil {
		t.Fatal("expected error for corrupt input")
	}
}

func TestGenerateKeepsSmallImages(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	out, w, h, err := Generate(bytes.NewReader(pngBytes(t, src)), 640, 80)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if w != 8 || h != 8 {
		t.Fatalf("dims = %dx%d, want 8x8", w, h)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty jpeg output")
	}
}
