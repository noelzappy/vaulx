// Package thumbnail generates low-resolution JPEG previews for raster images.
package thumbnail

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"

	_ "image/gif"  // register gif decoder
	_ "image/png"  // register png decoder
	_ "golang.org/x/image/webp" // register webp decoder
	"golang.org/x/image/draw"
)

const (
	DefaultMaxDim    = 640
	DefaultQuality   = 80
	ThumbContentType = "image/jpeg"
)

// ThumbKey returns the stable S3 key for a file's thumbnail. Keys are derived
// from the file UUID so they never collide with the original object and remain
// stable even if the file is renamed.
func ThumbKey(fileID string) string {
	return fmt.Sprintf("thumbs/%s.jpg", fileID)
}

// Generate downsizes r's image so its longest edge is at most maxDim pixels,
// composites any transparency onto white, and encodes a JPEG. It returns the
// encoded bytes plus the final pixel dimensions.
func Generate(r io.Reader, maxDim, quality int) ([]byte, int, int, error) {
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, 0, 0, err
	}
	if maxDim <= 0 {
		maxDim = DefaultMaxDim
	}
	if quality <= 0 {
		quality = DefaultQuality
	}

	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, 0, 0, fmt.Errorf("thumbnail: empty image")
	}

	dw, dh := sw, sh
	if dw > maxDim || dh > maxDim {
		if dw >= dh {
			dw = maxDim
			dh = int(float64(dh) * float64(maxDim) / float64(sw))
		} else {
			dh = maxDim
			dw = int(float64(dw) * float64(maxDim) / float64(sh))
		}
		if dw < 1 {
			dw = 1
		}
		if dh < 1 {
			dh = 1
		}
	}

	// Composite onto white so JPEG encoding doesn't turn transparent pixels black.
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), dw, dh, nil
}
