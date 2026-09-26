// Package mp4 is a minimal reader for the one thing the service needs from a
// Transcode's header: the video track's display dimensions. It reads only the
// few boxes on the way to them, from the start of the file, where Immich's
// faststart encoding puts the moov box.
package mp4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Header is what the reader takes from the video track.
type Header struct {
	// Width and Height are the stored frame dimensions, from tkhd.
	Width, Height int
	// Rotation is the clockwise rotation from the tkhd matrix, in degrees:
	// 0, 90, 180 or 270.
	Rotation int
}

// DisplaySize is the size the video is shown at: the stored dimensions,
// swapped when the frames are rotated by 90° or 270°.
func (h Header) DisplaySize() (width, height int) {
	if h.Rotation == 90 || h.Rotation == 270 {
		return h.Height, h.Width
	}
	return h.Width, h.Height
}

// ReadHeader reads the video track's header from data, the start of an MP4
// file. data may end in the middle of a box: whatever lies beyond it is
// ignored, so the video track only has to be within data.
func ReadHeader(data []byte) (Header, error) {
	moov, ok := find(data, "moov")
	if !ok {
		return Header{}, errors.New("mp4: no moov box")
	}
	for trak := range boxes(moov, "trak") {
		mdia, _ := find(trak, "mdia")
		hdlr, _ := find(mdia, "hdlr")
		// hdlr: version and flags (4), pre_defined (4), handler_type (4).
		if len(hdlr) < 12 || string(hdlr[8:12]) != "vide" {
			continue
		}
		tkhd, ok := find(trak, "tkhd")
		if !ok {
			return Header{}, errors.New("mp4: video track has no tkhd box")
		}
		return readTkhd(tkhd)
	}
	return Header{}, errors.New("mp4: no video track in the moov box")
}

// readTkhd reads the payload of a track header box.
func readTkhd(box []byte) (Header, error) {
	if len(box) < 1 {
		return Header{}, errors.New("mp4: truncated tkhd box")
	}
	// Version and flags (4), then the times, track id and duration, which
	// are 64-bit in version 1: 20 or 32 bytes. Then reserved (8), layer,
	// alternate group, volume and reserved (2 each).
	offset := 4 + 20 + 16
	if box[0] == 1 {
		offset = 4 + 32 + 16
	}
	// The 3×3 matrix of 16.16 fixed-point values, then width and height.
	if len(box) < offset+36+8 {
		return Header{}, errors.New("mp4: truncated tkhd box")
	}
	fixed := func(i int) float64 { return float64(int32(binary.BigEndian.Uint32(box[i:]))) / 65536 }
	h := Header{
		Width:    int(math.Round(fixed(offset + 36))),
		Height:   int(math.Round(fixed(offset + 40))),
		Rotation: rotation(fixed(offset), fixed(offset+4)),
	}
	if h.Width <= 0 || h.Height <= 0 {
		return Header{}, fmt.Errorf("mp4: video track has invalid dimensions %dx%d", h.Width, h.Height)
	}
	return h, nil
}

// rotation is the clockwise rotation in degrees of a matrix whose first row
// starts with a, b, rounded to a multiple of 90°. A rotation by θ has
// a = cos θ and b = sin θ.
func rotation(a, b float64) int {
	quarters := int(math.Round(math.Atan2(b, a) / (math.Pi / 2)))
	return (quarters + 4) % 4 * 90
}

// find returns the payload of the first box of type typ in data.
func find(data []byte, typ string) ([]byte, bool) {
	for payload := range boxes(data, typ) {
		return payload, true
	}
	return nil, false
}

// boxes yields the payload of each box of type typ among the sibling boxes in
// data. A box that extends beyond data is cut short there and is the last one
// read, so that the start of a truncated container can still be read. A
// malformed box ends the list.
func boxes(data []byte, typ string) func(yield func([]byte) bool) {
	return func(yield func([]byte) bool) {
		for len(data) >= 8 {
			size := uint64(binary.BigEndian.Uint32(data))
			header := uint64(8)
			switch size {
			case 0: // extends to the end of the file
				size = uint64(len(data))
			case 1: // 64-bit size after the type
				if len(data) < 16 {
					return
				}
				size, header = binary.BigEndian.Uint64(data[8:]), 16
			}
			if size < header {
				return
			}
			size = min(size, uint64(len(data)))
			if string(data[4:8]) == typ && !yield(data[header:size]) {
				return
			}
			data = data[size:]
		}
	}
}
