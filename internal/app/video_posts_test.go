package app_test

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

// videoPosts returns the Video Channel calls, checking that each is a video
// Post.
func (h *harness) videoPosts() []faketelegram.Call {
	h.t.Helper()
	calls := h.telegram.CallsTo(videoChannel)
	for _, c := range calls {
		if c.Method != "sendVideo" {
			h.t.Errorf("Video Channel call %s, want sendVideo", c.Method)
		}
	}
	return calls
}

// assertVideo checks the dimensions, duration and file of a video Post.
func assertVideo(t *testing.T, c faketelegram.Call, assetID string, width, height, duration int, transcode []byte) {
	t.Helper()
	want := map[string]string{
		"width":    strconv.Itoa(width),
		"height":   strconv.Itoa(height),
		"duration": strconv.Itoa(duration),
	}
	for k, v := range want {
		if c.Fields[k] != v {
			t.Errorf("%s: %s = %q, want %q", assetID, k, c.Fields[k], v)
		}
	}
	video, ok := c.Files["video"]
	if !ok {
		t.Fatalf("%s: no video file part; files %v", assetID, c.Files)
	}
	if video.Filename != assetID+".mp4" || video.ContentType != "video/mp4" {
		t.Errorf("%s: video part is %q of type %q, want %q of type video/mp4", assetID, video.Filename, video.ContentType, assetID+".mp4")
	}
	if !bytes.Equal(video.Data, transcode) {
		t.Errorf("%s: uploaded %d bytes that differ from the %d-byte Transcode", assetID, len(video.Data), len(transcode))
	}
}

func TestReadyVideoIsPostedWithItsTranscode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{
		ID:            "video-1",
		CreatedAt:     after(time.Minute),
		LocalDateTime: time.Date(2026, 9, 26, 19, 4, 0, 0, time.UTC),
		Transcoded:    true,
		Transcode:     fakeimmich.LandscapeMP4,
		Duration:      83_400 * time.Millisecond,
	})

	h.start()
	h.poll()

	posts := h.videoPosts()
	if len(posts) != 1 {
		t.Fatalf("got %d Video Channel calls, want 1 video Post", len(posts))
	}
	post := posts[0]
	for k, want := range map[string]string{
		"chat_id":            videoChannel,
		"caption":            textPost("26 Sep 2026, 19:04", "26 Sep 2026, 17:05", "video-1", 1),
		"parse_mode":         "HTML",
		"supports_streaming": "true",
	} {
		if post.Fields[k] != want {
			t.Errorf("%s = %q, want %q", k, post.Fields[k], want)
		}
	}
	assertVideo(t, post, "video-1", 1920, 1080, 83, fakeimmich.LandscapeMP4)
	if len(post.Files) != 2 {
		t.Errorf("file parts %v, want video and thumb", post.Files)
	}
	if !post.Chunked {
		t.Errorf("upload was not sent with chunked transfer encoding")
	}

	// The header is read from the first MiB, then the whole Transcode is
	// streamed.
	playback := h.immich.PlaybackRequests()
	if len(playback) != 2 {
		t.Fatalf("got %d playback requests, want 2", len(playback))
	}
	for i, wantRange := range []string{"bytes=0-1048575", ""} {
		r := playback[i]
		if r.Path != "/api/assets/video-1/video/playback" || r.Header.Get("Range") != wantRange {
			t.Errorf("playback request %d: GET %s with Range %q, want /api/assets/video-1/video/playback with Range %q",
				i+1, r.Path, r.Header.Get("Range"), wantRange)
		}
	}
}

func TestPortraitVideosAreSentUpright(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{ID: "quick-sync", CreatedAt: after(time.Second), Transcoded: true, Transcode: fakeimmich.Rotated90MP4, Duration: time.Second})
	h.immich.AddAsset(fakeimmich.Asset{ID: "software", CreatedAt: after(2 * time.Second), Transcoded: true, Transcode: fakeimmich.PortraitMP4, Duration: time.Second})
	h.immich.AddAsset(fakeimmich.Asset{ID: "landscape", CreatedAt: after(3 * time.Second), Transcoded: true, Transcode: fakeimmich.LandscapeMP4, Duration: time.Second})

	h.start()
	h.poll()

	posts := h.videoPosts()
	if len(posts) != 3 {
		t.Fatalf("got %d Video Channel calls, want 3 video Posts", len(posts))
	}
	// Landscape frames with a 90° matrix: the dimensions are swapped.
	assertVideo(t, posts[0], "quick-sync", 1080, 1920, 1, fakeimmich.Rotated90MP4)
	// Frames already rotated: the dimensions are as stored.
	assertVideo(t, posts[1], "software", 1080, 1920, 1, fakeimmich.PortraitMP4)
	assertVideo(t, posts[2], "landscape", 1920, 1080, 1, fakeimmich.LandscapeMP4)
}

func TestDurationIsRoundedToWholeSeconds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	durations := []struct {
		ms   int
		want int
	}{{12_499, 12}, {12_500, 13}, {400, 0}, {3_600_000, 3600}}
	for i, d := range durations {
		h.immich.AddAsset(fakeimmich.Asset{
			ID:         strconv.Itoa(d.ms),
			CreatedAt:  after(time.Duration(i+1) * time.Second),
			Transcoded: true,
			Duration:   time.Duration(d.ms) * time.Millisecond,
		})
	}

	h.start()
	h.poll()

	posts := h.videoPosts()
	if len(posts) != len(durations) {
		t.Fatalf("got %d Video Channel calls, want %d video Posts", len(posts), len(durations))
	}
	for i, d := range durations {
		if got := posts[i].Fields["duration"]; got != strconv.Itoa(d.want) {
			t.Errorf("%d ms: duration = %q, want %d", d.ms, got, d.want)
		}
	}
}

func TestUploadIsTheWholeTranscode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// A Transcode several MiB long: the header comes from its first MiB, and
	// the upload carries every byte.
	transcode := append(bytes.Clone(fakeimmich.Rotated90MP4), box("free", randomBytes(5<<20))...)
	h.immich.AddAsset(fakeimmich.Asset{ID: "long", CreatedAt: after(time.Minute), Transcoded: true, Transcode: transcode, Duration: 40 * time.Second})

	h.start()
	h.poll()

	posts := h.videoPosts()
	if len(posts) != 1 {
		t.Fatalf("got %d Video Channel calls, want 1 video Post", len(posts))
	}
	assertVideo(t, posts[0], "long", 1080, 1920, 40, transcode)
}

func TestTranscodeHeaderVariants(t *testing.T) {
	t.Parallel()
	audio := trak("soun", tkhd(0, identity, 0, 0))
	tests := []struct {
		name          string
		transcode     []byte
		width, height int
	}{
		{
			name:      "rotated 180°: as stored",
			transcode: mp4File(box("moov", audio, trak("vide", tkhd(0, rotate180, 1920, 1080)))),
			width:     1920, height: 1080,
		},
		{
			name:      "rotated 270°, version 1 tkhd, 64-bit box sizes: swapped",
			transcode: mp4File(largeBox("moov", largeBox("trak", tkhd(1, rotate270, 1920, 1080), box("mdia", hdlr("vide"))))),
			width:     1080, height: 1920,
		},
		{
			// A long video's sample tables can make moov longer than the
			// header read; the video track comes first.
			name: "moov longer than the header read",
			transcode: mp4File(box("moov",
				trak("vide", tkhd(0, rotate90, 1920, 1080)),
				box("trak", tkhd(0, identity, 0, 0), box("mdia", hdlr("soun"), box("free", randomBytes(2<<20)))))),
			width: 1080, height: 1920,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(time.Minute), Transcoded: true, Transcode: tt.transcode, Duration: time.Second})

			h.start()
			h.poll()

			posts := h.videoPosts()
			if len(posts) != 1 {
				t.Fatalf("got %d Video Channel calls, want 1 video Post", len(posts))
			}
			assertVideo(t, posts[0], "video", tt.width, tt.height, 1, tt.transcode)
		})
	}
}

// box is an MP4 box with a 32-bit size.
func box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(b, typ...), body...)
}

// largeBox is an MP4 box with a 64-bit size.
func largeBox(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := append(binary.BigEndian.AppendUint32(nil, 1), typ...)
	b = binary.BigEndian.AppendUint64(b, uint64(16+len(body)))
	return append(b, body...)
}

// Clockwise rotations, as the first four values of a tkhd matrix in 16.16
// fixed point.
var (
	identity  = [4]int32{1 << 16, 0, 0, 1 << 16}
	rotate90  = [4]int32{0, 1 << 16, -1 << 16, 0}
	rotate180 = [4]int32{-1 << 16, 0, 0, -1 << 16}
	rotate270 = [4]int32{0, -1 << 16, 1 << 16, 0}
)

// tkhd is a track header box with the given matrix and stored dimensions.
func tkhd(version byte, matrix [4]int32, width, height uint32) []byte {
	b := []byte{version, 0, 0, 3}
	if version == 1 {
		b = append(b, make([]byte, 32+16)...)
	} else {
		b = append(b, make([]byte, 20+16)...)
	}
	for _, v := range []int32{matrix[0], matrix[1], 0, matrix[2], matrix[3], 0, 0, 0, 1 << 30} {
		b = binary.BigEndian.AppendUint32(b, uint32(v))
	}
	b = binary.BigEndian.AppendUint32(b, width<<16)
	b = binary.BigEndian.AppendUint32(b, height<<16)
	return box("tkhd", b)
}

// hdlr is a handler box, e.g. for "vide" or "soun".
func hdlr(handler string) []byte {
	return box("hdlr", make([]byte, 8), []byte(handler), make([]byte, 13))
}

// trak is a track with the given handler and track header.
func trak(handler string, header []byte) []byte {
	return box("trak", header, box("mdia", box("mdhd", make([]byte, 24)), hdlr(handler), box("minf")))
}

// mp4File is an MP4 file with the given moov box, before a small mdat.
func mp4File(moov []byte) []byte {
	return bytes.Join([][]byte{box("ftyp", []byte("isom\x00\x00\x02\x00isomavc1")), moov, box("mdat", randomBytes(1000))}, nil)
}

// randomBytes returns n reproducible random bytes.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.NewChaCha8([32]byte{}).Read(b)
	return b
}
