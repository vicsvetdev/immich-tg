package fakeimmich

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/jpeg"
)

// Transcode fixtures: tiny 1-second MP4s encoded like Immich's Transcodes,
// H.264 and AAC with faststart. The audio track comes first, so a reader must
// look for the video track. They were made with ffmpeg 9:
//
//	enc() { ffmpeg -f lavfi -i "color=c=0x3366cc:s=$1:r=1:d=1" -f lavfi -i anullsrc=r=48000:cl=stereo \
//	  -t 1 -map 1:a -map 0:v -c:v libx264 -preset veryslow -crf 51 -pix_fmt yuv420p -c:a aac -b:a 8k \
//	  -movflags +faststart -map_metadata -1 -fflags +bitexact -flags:v +bitexact -flags:a +bitexact "$2"; }
//	enc 1920x1080 landscape.mp4
//	enc 1080x1920 portrait.mp4
//	ffmpeg -display_rotation:v:0 -90 -i landscape.mp4 -map 0 -c copy -movflags +faststart \
//	  -map_metadata -1 -fflags +bitexact rotated90.mp4
var (
	// LandscapeMP4 has 1920×1080 frames and no rotation.
	//go:embed fixtures/landscape.mp4
	LandscapeMP4 []byte

	// Rotated90MP4 is a portrait video the way Immich transcodes it with
	// Quick Sync and hardware decoding: 1920×1080 landscape frames plus a
	// matrix rotating them 90° clockwise, shown as 1080×1920.
	//go:embed fixtures/rotated90.mp4
	Rotated90MP4 []byte

	// PortraitMP4 is a portrait video the way Immich's software fallback
	// transcodes it: frames already rotated to 1080×1920, with no matrix.
	//go:embed fixtures/portrait.mp4
	PortraitMP4 []byte
)

// Preview colours: previews are split into a left half in PreviewLeft and a
// right half in PreviewRight, so that a thumbnail can be checked for the
// right picture, the right way round.
var (
	PreviewLeft  = color.RGBA{0xcc, 0x33, 0x33, 0xff}
	PreviewRight = color.RGBA{0x33, 0x66, 0xcc, 0xff}
)

var (
	// PreviewJPEG is a preview the size Immich makes them by default, 1440×810.
	PreviewJPEG = SplitJPEG(1440, 810)

	// PreviewWebP is a 1440×810 WebP preview, as Immich makes them when
	// configured to. It was made with ffmpeg 9:
	//
	//	ffmpeg -f lavfi -i color=c=0xcc3333:s=720x810:d=1 -f lavfi -i color=c=0x3366cc:s=720x810:d=1 \
	//	  -filter_complex "[0][1]hstack" -frames:v 1 -c:v libwebp -quality 50 \
	//	  -map_metadata -1 -fflags +bitexact -flags +bitexact preview.webp
	//go:embed fixtures/preview.webp
	PreviewWebP []byte
)

// SplitJPEG returns a split preview of the given size.
func SplitJPEG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			c := PreviewLeft
			if x >= width/2 {
				c = PreviewRight
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
