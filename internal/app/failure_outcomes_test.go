package app_test

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

// tooLargeNote ends the caption of a Link-only Post for an Oversized Video.
const tooLargeNote = "\nℹ️ video too large for Telegram"

// failureVideo is the Video the failure tests publish, recorded at
// 26 Sep 2026, 19:04.
func failureVideo(a fakeimmich.Asset) fakeimmich.Asset {
	a.ID = "video-1"
	a.CreatedAt = after(time.Minute)
	a.LocalDateTime = time.Date(2026, 9, 26, 19, 4, 0, 0, time.UTC)
	a.OriginalFileName = "PXL_20260926_190400123.mp4"
	a.Transcoded = true
	if a.Duration == 0 {
		a.Duration = time.Second
	}
	return a
}

// assertProblemReport checks that the Log Channel got exactly one Problem
// Report after the started message, about failureVideo, of the given kind and
// with a reason mentioning each of reasons, and that it was logged too.
func (h *harness) assertProblemReport(kind string, reasons ...string) {
	h.t.Helper()
	texts := h.logChannelTexts()
	if len(texts) != 1 {
		h.t.Fatalf("Log Channel got %q, want one Problem Report", texts)
	}
	wantStart := "⚠️ Problem: " + kind + "\n" +
		"📅 26 Sep 2026, 19:04\n" +
		"📄 PXL_20260926_190400123.mp4\n" +
		"🔗 " + publicURL + "/photos/video-1\n" +
		"Reason: "
	if !strings.HasPrefix(texts[0], wantStart) {
		h.t.Errorf("Problem Report:\n%s\nwant it to start with:\n%s", texts[0], wantStart)
	}
	for _, r := range reasons {
		if !strings.Contains(texts[0], r) {
			h.t.Errorf("Problem Report %q does not mention %q", texts[0], r)
		}
	}
	logged := h.logLines("Problem Report")
	if len(logged) != 1 || logged[0]["kind"] != kind || logged[0]["asset_id"] != "video-1" {
		h.t.Errorf("Problem Report log lines = %v, want one of kind %q for video-1", logged, kind)
	}
}

// waitFor waits until cond holds, for what happens outside a poll.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; output:\n%s", what, h.stdout)
		}
		time.Sleep(time.Millisecond)
	}
}

// sendVideoCalls returns the sendVideo calls the Bot API server received.
func (h *harness) sendVideoCalls() []faketelegram.Call {
	var out []faketelegram.Call
	for _, c := range h.telegram.Calls() {
		if c.Method == "sendVideo" {
			out = append(out, c)
		}
	}
	return out
}

func TestOversizedVideoGetsLinkOnlyPostWithoutUpload(t *testing.T) {
	h := newHarness(t)
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{ContentLength: 2_000_000_001}))

	h.start()
	h.poll()

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+tooLargeNote)
	if calls := h.sendVideoCalls(); len(calls) != 0 {
		t.Errorf("got %d sendVideo calls, want none: the upload never starts", len(calls))
	}
	h.assertProblemReport("oversized", "2000000001 bytes", "2000000000")
	if n := len(h.immich.PlaybackRequests()); n != 2 {
		t.Errorf("got %d playback requests, want 2: the header and the download that was closed", n)
	}
}

func TestTranscodeOfExactlyTheLimitIsUploaded(t *testing.T) {
	h := newHarness(t)
	// Declares exactly the limit, but the fake then cuts the download short.
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{ContentLength: 2_000_000_000}))

	h.start()
	h.poll()

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
	h.assertProblemReport("Transcode download failed", "could not download the Transcode")
	// The upload started: the Bot API server records it once it notices the
	// body end early, which may be just after the poll.
	h.waitFor("the upload to reach the Bot API server", func() bool { return len(h.sendVideoCalls()) == 1 })
}

func TestMissingContentLength(t *testing.T) {
	t.Run("the upload goes ahead", func(t *testing.T) {
		h := newHarness(t)
		h.immich.AddAsset(failureVideo(fakeimmich.Asset{NoContentLength: true}))

		h.start()
		h.poll()

		posts := h.telegram.Posts(videoChannel)
		if len(posts) != 1 {
			t.Fatalf("got %d Posts, want 1 video Post", len(posts))
		}
		assertVideo(t, posts[0], "video-1", 1920, 1080, 1, fakeimmich.LandscapeMP4)
		if texts := h.logChannelTexts(); len(texts) != 0 {
			t.Errorf("Log Channel got %q, want no Problem Report", texts)
		}
	})
	t.Run("a Telegram rejection is an upload failure", func(t *testing.T) {
		h := newHarness(t)
		h.immich.AddAsset(failureVideo(fakeimmich.Asset{NoContentLength: true}))
		h.telegram.Enqueue("sendVideo", faketelegram.BareStatus(http.StatusRequestEntityTooLarge))

		h.start()
		h.poll()

		assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
		h.assertProblemReport("upload failed", "telegram sendVideo: HTTP 413 Request Entity Too Large")
	})
}

func TestUnobtainableTranscodeGetsLinkOnlyPost(t *testing.T) {
	// An MP4 whose first MiB has no moov box.
	noMoov := bytes.Join([][]byte{box("ftyp", []byte("isom\x00\x00\x02\x00isomavc1")), box("mdat", randomBytes(1000))}, nil)
	// An MP4 whose moov box starts only after the first MiB.
	lateMoov := bytes.Join([][]byte{
		box("ftyp", []byte("isom\x00\x00\x02\x00isomavc1")),
		box("free", randomBytes(1<<20)),
		box("moov", trak("vide", tkhd(0, identity, 1920, 1080))),
		box("mdat", randomBytes(1000)),
	}, nil)
	// An MP4 whose moov box has no video track.
	noVideo := mp4File(box("moov", trak("soun", tkhd(0, identity, 0, 0))))
	tests := []struct {
		name    string
		asset   fakeimmich.Asset
		fail    bool // the playback endpoint fails, header reads included
		kind    string
		reasons []string
	}{
		{
			name:    "header read fails",
			fail:    true,
			kind:    "Transcode download failed",
			reasons: []string{"could not read the Transcode header", "HTTP 500", "playback is broken"},
		},
		{
			name:    "download fails",
			asset:   fakeimmich.Asset{DownloadFailure: http.StatusBadGateway},
			kind:    "Transcode download failed",
			reasons: []string{"could not download the Transcode", "HTTP 502", "Download failed"},
		},
		{
			name:    "download cut short",
			asset:   fakeimmich.Asset{ContentLength: int64(len(fakeimmich.LandscapeMP4)) + 1000},
			kind:    "Transcode download failed",
			reasons: []string{"could not download the Transcode: immich GET /api/assets/video-1/video/playback: read response: unexpected EOF"},
		},
		{
			name:    "no moov in the first MiB",
			asset:   fakeimmich.Asset{Transcode: noMoov},
			kind:    "Transcode header unreadable",
			reasons: []string{"unreadable Transcode header in the first 1048576 bytes", "no moov box"},
		},
		{
			name:    "moov after the first MiB",
			asset:   fakeimmich.Asset{Transcode: lateMoov},
			kind:    "Transcode header unreadable",
			reasons: []string{"unreadable Transcode header in the first 1048576 bytes", "no moov box"},
		},
		{
			name:    "no video track",
			asset:   fakeimmich.Asset{Transcode: noVideo},
			kind:    "Transcode header unreadable",
			reasons: []string{"unreadable Transcode header", "no video track"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.immich.AddAsset(failureVideo(tt.asset))
			if tt.fail {
				h.immich.Fail("GET /api/assets/video-1/video/playback", http.StatusInternalServerError, "playback is broken")
			}

			h.start()
			h.poll()
			h.clock.Advance(30 * time.Second)
			h.poll() // never retried

			assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
			h.assertProblemReport(tt.kind, tt.reasons...)
		})
	}
}

func TestUploadFailureGetsLinkOnlyPost(t *testing.T) {
	tests := []struct {
		name   string
		reply  faketelegram.Reply
		reason string
	}{
		{"JSON error", faketelegram.JSONError(400, "Bad Request: wrong file identifier"), "telegram sendVideo: 400 Bad Request: wrong file identifier"},
		{"bare 413", faketelegram.BareStatus(http.StatusRequestEntityTooLarge), "telegram sendVideo: HTTP 413 Request Entity Too Large"},
		{"bare 400", faketelegram.BareStatus(http.StatusBadRequest), "telegram sendVideo: HTTP 400 Bad Request"},
		{"bare 501", faketelegram.BareStatus(http.StatusNotImplemented), "telegram sendVideo: HTTP 501 Not Implemented"},
		// Without retry_after there is nothing to wait out.
		{"bare 429", faketelegram.BareStatus(http.StatusTooManyRequests), "telegram sendVideo: HTTP 429 Too Many Requests"},
		{"disconnect", faketelegram.Disconnect(), `telegram sendVideo: Post "` + "http://"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))
			h.telegram.Enqueue("sendVideo", tt.reply)

			h.start()
			h.poll()
			h.clock.Advance(30 * time.Second)
			h.poll() // never retried

			assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
			h.assertProblemReport("upload failed", tt.reason)
			if n := len(h.sendVideoCalls()); n != 1 {
				t.Errorf("got %d sendVideo calls, want 1", n)
			}
			if waits := h.clock.Waits(); len(waits) != 0 {
				t.Errorf("waited %v, want no waits", waits)
			}
		})
	}
}

func TestShareLinkFailureGetsProblemReportOnly(t *testing.T) {
	h := newHarness(t)
	h.immich.FailShareLinks(http.StatusForbidden)
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))

	h.start()
	h.poll()

	if calls := h.telegram.CallsTo(videoChannel); len(calls) != 0 {
		t.Errorf("got Video Channel calls %+v, want none", calls)
	}
	h.assertProblemReport("Share Link creation failed", "immich POST /api/shared-links: HTTP 403 Forbidden: Share Link creation failed")
	if n := len(h.immich.PlaybackRequests()); n != 0 {
		t.Errorf("got %d playback requests, want none", n)
	}
}

func TestFailedLinkOnlyFallbackGetsOneProblemReportNotingBoth(t *testing.T) {
	tests := []struct {
		name    string
		asset   fakeimmich.Asset
		upload  []faketelegram.Reply
		kind    string
		reasons []string
	}{
		{
			name:    "upload failure",
			upload:  []faketelegram.Reply{faketelegram.BareStatus(http.StatusRequestEntityTooLarge)},
			kind:    "upload failed",
			reasons: []string{"HTTP 413", "the Link-only Post failed too", "Bad Request: message is too long"},
		},
		{
			name:    "Oversized Video",
			asset:   fakeimmich.Asset{ContentLength: 3_000_000_000},
			kind:    "oversized",
			reasons: []string{"3000000000 bytes", "the Link-only Post failed too", "Bad Request: message is too long"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.poll() // after the started message, so the error hits the Link-only Post
			h.immich.AddAsset(failureVideo(tt.asset))
			for _, r := range tt.upload {
				h.telegram.Enqueue("sendVideo", r)
			}
			h.telegram.Enqueue("sendMessage", faketelegram.JSONError(400, "Bad Request: message is too long"))
			h.clock.Advance(30 * time.Second)
			h.poll()

			assertPosts(t, h.posts())
			h.assertProblemReport(tt.kind, tt.reasons...)
		})
	}
}

func TestTelegramUnreachableForTheProblemReportToo(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.poll()
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))
	h.telegram.Enqueue("sendVideo", faketelegram.Disconnect())
	h.telegram.Enqueue("sendMessage", faketelegram.Disconnect()) // the Link-only Post
	h.telegram.Enqueue("sendMessage", faketelegram.Disconnect()) // the Problem Report
	h.clock.Advance(30 * time.Second)
	h.poll()

	assertPosts(t, h.posts())
	if texts := h.logChannelTexts(); len(texts) != 0 {
		t.Errorf("Log Channel got %q after the started message, want nothing", texts)
	}
	if n := len(h.telegram.CallsTo(logChannel)); n != 2 {
		t.Errorf("got %d Log Channel calls, want the started message and the failed Problem Report", n)
	}
	// The container log has it all.
	report := h.logLine("Problem Report")
	if report["kind"] != "upload failed" || report["asset_id"] != "video-1" {
		t.Errorf("Problem Report log line = %v, want an upload failure of video-1", report)
	}
	if msg, _ := report["error"].(string); !strings.Contains(msg, "the Link-only Post failed too") {
		t.Errorf("Problem Report log line error = %q, want it to note the Link-only Post failure", msg)
	}
	failure := h.logLine("could not publish the Problem Report to the Log Channel")
	if failure["asset_id"] != "video-1" {
		t.Errorf("failure log line = %v, want one for video-1", failure)
	}

	// The service keeps running.
	h.immich.AddAsset(fakeimmich.Asset{ID: "video-2", CreatedAt: after(2 * time.Minute), Transcoded: true})
	h.clock.Advance(30 * time.Second)
	h.poll()
	if posts := h.telegram.Posts(videoChannel); len(posts) != 1 || posts[0].Method != "sendVideo" {
		t.Errorf("got Posts %+v, want the video Post of the next Video", posts)
	}
}

func TestRateLimitedUploadIsWaitedOutAndRepeatedWithAFreshStream(t *testing.T) {
	h := newHarness(t)
	transcode := append(bytes.Clone(fakeimmich.LandscapeMP4), box("free", randomBytes(3<<20))...)
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{Transcode: transcode, Duration: 83 * time.Second}))
	h.telegram.Enqueue("sendVideo", faketelegram.RateLimited(7))

	h.start()
	h.poll()

	calls := h.telegram.CallsTo(videoChannel)
	if len(calls) != 2 || calls[0].Status != http.StatusTooManyRequests {
		t.Fatalf("got Video Channel calls %+v, want the rate-limited upload and its repeat", calls)
	}
	posts := h.telegram.Posts(videoChannel)
	if len(posts) != 1 {
		t.Fatalf("got %d Posts, want 1", len(posts))
	}
	assertVideo(t, posts[0], "video-1", 1920, 1080, 83, transcode)
	if got := posts[0].Fields["caption"]; got != textPost("26 Sep 2026, 19:04", 1) {
		t.Errorf("caption = %q, want the Post's", got)
	}
	if waits := h.clock.Waits(); !slices.Equal(waits, []time.Duration{7 * time.Second}) {
		t.Errorf("waited %v, want the 7s of retry_after", waits)
	}
	// The header, then a fresh stream of the whole Transcode for each upload.
	var ranges []string
	for _, r := range h.immich.PlaybackRequests() {
		ranges = append(ranges, r.Header.Get("Range"))
	}
	if want := []string{"bytes=0-1048575", "", ""}; !slices.Equal(ranges, want) {
		t.Errorf("playback requests with Range %q, want %q", ranges, want)
	}
	if texts := h.logChannelTexts(); len(texts) != 0 {
		t.Errorf("Log Channel got %q, want no Problem Report", texts)
	}
	if n := len(h.immich.ShareLinkBodies()); n != 1 {
		t.Errorf("created %d Share Links, want 1", n)
	}
}

func TestRateLimitedMessagesAreWaitedOut(t *testing.T) {
	h := newHarness(t)
	h.telegram.Enqueue("sendMessage", faketelegram.RateLimited(2)) // the started message
	h.start()
	h.poll()
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))
	h.telegram.Enqueue("sendVideo", faketelegram.RateLimited(5))
	h.telegram.Enqueue("sendVideo", faketelegram.JSONError(400, "Bad Request: wrong file identifier"))
	h.telegram.Enqueue("sendMessage", faketelegram.RateLimited(3)) // the Link-only Post
	h.telegram.Enqueue("sendMessage", faketelegram.RateLimited(4)) // the Problem Report
	h.telegram.Enqueue("sendMessage", faketelegram.RateLimited(4))
	h.clock.Advance(30 * time.Second)
	h.poll()

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
	h.assertProblemReport("upload failed", "wrong file identifier")
	want := []time.Duration{2 * time.Second, 5 * time.Second, 3 * time.Second, 4 * time.Second, 4 * time.Second}
	if waits := h.clock.Waits(); !slices.Equal(waits, want) {
		t.Errorf("waited %v, want %v", waits, want)
	}
	if !strings.Contains(h.telegram.Posts(logChannel)[0].Fields["text"], "started") {
		t.Errorf("the started message was not published after its 429")
	}
}

func TestStalledUploadIsAbandoned(t *testing.T) {
	t.Run("Immich stalls", func(t *testing.T) {
		h := newHarness(t)
		h.immich.AddAsset(failureVideo(fakeimmich.Asset{Stall: true}))

		h.start()
		h.pollAdvancing(30 * time.Second)

		assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
		h.assertProblemReport("Transcode download failed", "could not download the Transcode: stalled: no data for 2m0s")
	})
	t.Run("the Bot API server stalls", func(t *testing.T) {
		h := newHarness(t)
		// Large enough to fill the connection's buffers.
		transcode := append(bytes.Clone(fakeimmich.LandscapeMP4), box("free", randomBytes(32<<20))...)
		h.immich.AddAsset(failureVideo(fakeimmich.Asset{Transcode: transcode}))
		h.telegram.Enqueue("sendVideo", faketelegram.Stall())

		h.start()
		h.pollAdvancing(30 * time.Second)

		assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
		h.assertProblemReport("upload failed", "stalled: the Bot API server took no data for 2m0s")
	})
}

func TestShutdownDuringUploadSendsNoProblemReport(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.poll()
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))
	h.telegram.Enqueue("sendVideo", faketelegram.Stall())
	h.clock.Advance(30 * time.Second)
	h.startPoll()
	h.waitFor("the upload to start", func() bool { return len(h.sendVideoCalls()) == 1 })

	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	assertPosts(t, h.posts())
	if texts := h.logChannelTexts(); !slices.Equal(texts, []string{stoppedText}) {
		t.Errorf("Log Channel got %q after the started message, want only the stopped message", texts)
	}
	if n := len(h.logLines("Problem Report")); n != 0 {
		t.Errorf("got %d Problem Report log lines, want none", n)
	}
	h.logLine("Video abandoned on shutdown")
}
