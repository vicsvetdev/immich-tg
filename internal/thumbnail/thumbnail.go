// Package thumbnail turns Immich's preview image of a Video into a Telegram
// video thumbnail: a JPEG whose longest side is at most 320 px, under 200 KiB.
package thumbnail

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"mime"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	// maxSide is the longest side Telegram allows for a thumbnail.
	maxSide = 320
	// maxSize is the size a thumbnail must stay under.
	maxSize = 204800
	// maxPixels bounds the preview's size before it is decoded. Immich
	// previews are 1440 px on their longest side by default.
	maxPixels = 16 << 20
)

// qualities are the JPEG qualities tried in turn, until one is under maxSize.
var qualities = []int{90, 80, 70, 60, 50, 40, 30, 20, 10}

// Make makes a thumbnail from a preview image with the given Content-Type,
// JPEG or WebP.
func Make(preview []byte, contentType string) ([]byte, error) {
	img, err := decode(preview, contentType)
	if err != nil {
		return nil, err
	}
	return encode(scale(img), maxSize)
}

func decode(preview []byte, contentType string) (image.Image, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("preview has an invalid Content-Type %q: %w", contentType, err)
	}
	var decodeConfig func(io.Reader) (image.Config, error)
	var decodeImage func(io.Reader) (image.Image, error)
	switch mediaType {
	case "image/jpeg":
		decodeConfig, decodeImage = jpeg.DecodeConfig, jpeg.Decode
	case "image/webp":
		decodeConfig, decodeImage = webp.DecodeConfig, webp.Decode
	default:
		return nil, fmt.Errorf("preview has an unsupported Content-Type %q, want image/jpeg or image/webp", contentType)
	}
	cfg, err := decodeConfig(bytes.NewReader(preview))
	if err != nil {
		return nil, fmt.Errorf("decode %s preview: %w", mediaType, err)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("preview is %d×%d, which is not a usable size", cfg.Width, cfg.Height)
	}
	img, err := decodeImage(bytes.NewReader(preview))
	if err != nil {
		return nil, fmt.Errorf("decode %s preview: %w", mediaType, err)
	}
	return img, nil
}

// scale scales img so that its longest side is at most maxSide, keeping its
// aspect ratio, onto a white background, since JPEG has no transparency.
func scale(img image.Image) image.Image {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if longest := max(w, h); longest > maxSide {
		w = max(1, (w*maxSide+longest/2)/longest)
		h = max(1, (h*maxSide+longest/2)/longest)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}

// encode encodes img as JPEG, lowering the quality until it is under limit
// bytes. At maxSide, even the first quality tried is well under maxSize for
// any picture, so lowering it is a safeguard.
func encode(img image.Image, limit int) ([]byte, error) {
	var buf bytes.Buffer
	for _, q := range qualities {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, fmt.Errorf("encode thumbnail: %w", err)
		}
		if buf.Len() < limit {
			return buf.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("thumbnail is %d bytes even at JPEG quality %d, want under %d", buf.Len(), qualities[len(qualities)-1], limit)
}
