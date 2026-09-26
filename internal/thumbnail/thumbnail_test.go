package thumbnail

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand/v2"
	"testing"
)

// noise is a picture that compresses badly.
func noise(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rand.NewChaCha8([32]byte{}).Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return img
}

func TestEncodeLowersQualityUntilUnderTheLimit(t *testing.T) {
	img := noise(maxSide, maxSide)
	var first bytes.Buffer
	if err := jpeg.Encode(&first, img, &jpeg.Options{Quality: qualities[0]}); err != nil {
		t.Fatal(err)
	}
	limit := first.Len() / 2

	thumb, err := encode(img, limit)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(thumb) >= limit {
		t.Errorf("thumbnail is %d bytes, want under %d", len(thumb), limit)
	}
	if _, err := jpeg.Decode(bytes.NewReader(thumb)); err != nil {
		t.Errorf("thumbnail is not a JPEG: %v", err)
	}
}

func TestEncodeFailsWhenNoQualityIsUnderTheLimit(t *testing.T) {
	if thumb, err := encode(noise(maxSide, maxSide), 100); err == nil {
		t.Errorf("encode made a %d-byte thumbnail, want an error", len(thumb))
	}
}
