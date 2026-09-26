package app_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"testing"
	"time"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

// decodeThumbnail checks that a video Post carries a thumbnail and returns
// it decoded.
func decodeThumbnail(t *testing.T, c faketelegram.Call) image.Image {
	t.Helper()
	if got := c.Fields["thumbnail"]; got != "attach://thumb" {
		t.Errorf("thumbnail = %q, want attach://thumb", got)
	}
	thumb, ok := c.Files["thumb"]
	if !ok {
		t.Fatalf("no thumb file part; files %v", c.Files)
	}
	if thumb.ContentType != "image/jpeg" {
		t.Errorf("thumb part has type %q, want image/jpeg", thumb.ContentType)
	}
	if len(thumb.Data) >= 204800 {
		t.Errorf("thumbnail is %d bytes, want under 204800", len(thumb.Data))
	}
	img, err := jpeg.Decode(bytes.NewReader(thumb.Data))
	if err != nil {
		t.Fatalf("thumbnail is not a JPEG: %v", err)
	}
	return img
}

// assertThumbnail checks a thumbnail's size and that it shows a split preview
// the right way round.
func assertThumbnail(t *testing.T, img image.Image, width, height int) {
	t.Helper()
	b := img.Bounds()
	if b.Dx() != width || b.Dy() != height {
		t.Errorf("thumbnail is %d×%d, want %d×%d", b.Dx(), b.Dy(), width, height)
	}
	y := b.Min.Y + b.Dy()/2
	for _, p := range []struct {
		x    int
		want color.RGBA
	}{
		{b.Min.X + b.Dx()/4, fakeimmich.PreviewLeft},
		{b.Min.X + b.Dx()*3/4, fakeimmich.PreviewRight},
	} {
		if got := img.At(p.x, y); !near(got, p.want) {
			t.Errorf("thumbnail pixel (%d, %d) is %v, want about %v", p.x, y, got, p.want)
		}
	}
}

// near reports whether c is within lossy-compression distance of want.
func near(c color.Color, want color.RGBA) bool {
	r, g, b, _ := c.RGBA()
	for _, d := range []int{int(r>>8) - int(want.R), int(g>>8) - int(want.G), int(b>>8) - int(want.B)} {
		if d < -16 || d > 16 {
			return false
		}
	}
	return true
}

// onePost runs one poll and returns the only video Post.
func (h *harness) onePost() faketelegram.Call {
	h.t.Helper()
	h.start()
	h.poll()
	posts := h.videoPosts()
	if len(posts) != 1 {
		h.t.Fatalf("got %d Video Channel calls, want 1 video Post", len(posts))
	}
	return posts[0]
}

func TestThumbnailFromJPEGPreview(t *testing.T) {
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{ID: "video-1", CreatedAt: after(time.Minute), Transcoded: true, Duration: time.Second})

	post := h.onePost()

	assertThumbnail(t, decodeThumbnail(t, post), 320, 180)
	assertVideo(t, post, "video-1", 1920, 1080, 1, fakeimmich.LandscapeMP4)

	var previews []fakeimmich.Request
	for _, r := range h.immich.Requests() {
		if r.Path == "/api/assets/video-1/thumbnail" {
			previews = append(previews, r)
		}
	}
	if len(previews) != 1 {
		t.Fatalf("got %d preview requests, want 1", len(previews))
	}
	if r := previews[0]; r.Method != http.MethodGet || r.Query.Get("size") != "preview" {
		t.Errorf("preview request %s %s?%s, want GET with size=preview", r.Method, r.Path, r.Query.Encode())
	}
}

func TestThumbnailFromWebPPreview(t *testing.T) {
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{
		ID: "video-1", CreatedAt: after(time.Minute), Transcoded: true, Duration: time.Second,
		Preview: fakeimmich.PreviewWebP, PreviewType: "image/webp",
	})

	post := h.onePost()

	assertThumbnail(t, decodeThumbnail(t, post), 320, 180)
	assertVideo(t, post, "video-1", 1920, 1080, 1, fakeimmich.LandscapeMP4)
}

func TestThumbnailIsScaledToFit(t *testing.T) {
	tests := []struct {
		name                        string
		previewWidth, previewHeight int
		width, height               int
	}{
		{"oversized landscape", 4000, 3000, 320, 240},
		{"oversized portrait", 1080, 1920, 180, 320},
		{"odd aspect ratio, rounded", 1000, 333, 320, 107},
		{"already small enough: not enlarged", 200, 100, 200, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.immich.AddAsset(fakeimmich.Asset{
				ID: "video-1", CreatedAt: after(time.Minute), Transcoded: true, Duration: time.Second,
				Preview: fakeimmich.SplitJPEG(tt.previewWidth, tt.previewHeight),
			})

			post := h.onePost()

			assertThumbnail(t, decodeThumbnail(t, post), tt.width, tt.height)
		})
	}
}

func TestVideoIsPostedWithoutThumbnailWhenOneCannotBeMade(t *testing.T) {
	tests := []struct {
		name  string
		asset fakeimmich.Asset
		setup func(h *harness)
		// log is the message logged about the missing thumbnail.
		log string
	}{
		{
			name: "thumbnail endpoint fails",
			setup: func(h *harness) {
				h.immich.Fail("GET /api/assets/video-1/thumbnail", http.StatusInternalServerError, "Internal server error")
			},
			log: "could not fetch the preview; no thumbnail",
		},
		{
			name:  "preview is not an image",
			asset: fakeimmich.Asset{Preview: []byte("not a JPEG"), PreviewType: "image/jpeg"},
			log:   "could not make the thumbnail; no thumbnail",
		},
		{
			name:  "preview does not match its Content-Type",
			asset: fakeimmich.Asset{Preview: fakeimmich.PreviewJPEG, PreviewType: "image/webp"},
			log:   "could not make the thumbnail; no thumbnail",
		},
		{
			name:  "preview has an unsupported Content-Type",
			asset: fakeimmich.Asset{Preview: fakeimmich.PreviewJPEG, PreviewType: "image/png"},
			log:   "could not make the thumbnail; no thumbnail",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			a := tt.asset
			a.ID, a.CreatedAt, a.Transcoded, a.Duration = "video-1", after(time.Minute), true, time.Second
			h.immich.AddAsset(a)
			if tt.setup != nil {
				tt.setup(h)
			}

			post := h.onePost()

			if got, ok := post.Fields["thumbnail"]; ok {
				t.Errorf("thumbnail = %q, want none", got)
			}
			if len(post.Files) != 1 {
				t.Errorf("file parts %v, want only video", post.Files)
			}
			if post.Fields["caption"] != textPost("26 Sep 2026, 17:05", 1) {
				t.Errorf("caption = %q, want the usual caption", post.Fields["caption"])
			}
			assertVideo(t, post, "video-1", 1920, 1080, 1, fakeimmich.LandscapeMP4)
			if texts := h.logChannelTexts(); len(texts) != 0 {
				t.Errorf("Log Channel got %q, want nothing", texts)
			}
			if entry := h.logLine(tt.log); entry["asset_id"] != "video-1" || entry["level"] != "WARN" {
				t.Errorf("log line %v, want a WARN for asset video-1", entry)
			}
		})
	}
}
