package app_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

// notAvailableNote ends the caption of a Link-only Post for a Video whose
// Transcode could not be obtained.
const notAvailableNote = "\nℹ️ video not available in Telegram"

// logChannelTexts returns the texts published to the Log Channel after the
// started message, leaving out the calls Telegram rejected. It checks that
// each is a plain-text sendMessage.
func (h *harness) logChannelTexts() []string {
	h.t.Helper()
	calls := h.telegram.Posts(logChannel)
	if len(calls) == 0 {
		h.t.Fatalf("no started message in the Log Channel")
	}
	var texts []string
	for _, c := range calls[1:] {
		if c.Method != "sendMessage" || c.Fields["parse_mode"] != "" {
			h.t.Errorf("Log Channel call %s with parse_mode %q, want a plain-text sendMessage", c.Method, c.Fields["parse_mode"])
		}
		texts = append(texts, c.Fields["text"])
	}
	return texts
}

func TestWaitingVideoIsDroppedWhenTheOperatorRemovesIt(t *testing.T) {
	tests := []struct {
		name   string
		remove func(*fakeimmich.Server, string)
	}{
		{"trashed", (*fakeimmich.Server).Trash},
		{"archived", func(s *fakeimmich.Server, id string) { s.SetVisibility(id, "archive") }},
		{"locked", func(s *fakeimmich.Server, id string) { s.SetVisibility(id, "locked") }},
		{"deleted", (*fakeimmich.Server).Delete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.poll()
			h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(10 * time.Second)})
			h.clock.Advance(30 * time.Second)
			h.poll()

			tt.remove(h.immich, "video")
			h.clock.Advance(30 * time.Second)
			h.poll()
			// Long past the WAIT_TIMEOUT, so it would have timed out had it
			// not been dropped.
			h.clock.Advance(3 * time.Hour)
			h.poll()

			if calls := h.telegram.CallsTo(videoChannel); len(calls) != 0 {
				t.Errorf("got %d Video Channel calls, want none: %+v", len(calls), calls)
			}
			if texts := h.logChannelTexts(); len(texts) != 0 {
				t.Errorf("Log Channel got %q, want nothing after the started message", texts)
			}
			if n := len(h.immich.ShareLinkBodies()); n != 0 {
				t.Errorf("created %d Share Links, want 0", n)
			}
			if dropped := h.logLines("Waiting Video dropped"); len(dropped) != 1 || dropped[0]["asset_id"] != "video" {
				t.Errorf("dropped log lines = %v, want one for video", dropped)
			}
		})
	}
}

func TestDroppedVideoNoLongerHoldsTheSearchWindow(t *testing.T) {
	h := newHarness(t)
	// Far beyond the test's polls, so only the drop can release the Video.
	h.env["WAIT_TIMEOUT"] = "24h"
	h.start()
	h.poll()
	h.immich.AddAsset(fakeimmich.Asset{ID: "stuck", CreatedAt: after(10 * time.Second)})
	h.clock.Advance(30 * time.Second)
	h.poll()
	h.immich.Trash("stuck")

	const polls = 200
	for i := 1; i <= polls; i++ {
		h.clock.Advance(30 * time.Second)
		h.immich.AddAsset(fakeimmich.Asset{ID: fmt.Sprintf("video-%d", i), CreatedAt: h.clock.Now(), Transcoded: true})
		h.poll()
	}

	if n := len(h.telegram.CallsTo(videoChannel)); n != polls {
		t.Fatalf("got %d Posts, want %d", n, polls)
	}
	// The window follows the newest upload minus the 5-minute overlap.
	searches := h.immich.SearchBodies()
	last := searches[len(searches)-1]["filter"].(map[string]any)["createdAt"].(map[string]any)["gte"]
	if want := "2026-09-26T18:39:05.000Z"; last != want {
		t.Errorf("last search from %v, want %s", last, want)
	}
	// Handled Videos are kept for the overlap only: about 11 plus the new one.
	const bound = 12
	for _, line := range h.logLines("New Video waiting") {
		if tracked := line["tracked"].(float64); tracked > bound {
			t.Fatalf("tracking %v Videos at %v, want at most %d", tracked, line["asset_id"], bound)
		}
	}
}

func TestWaitingVideoTimesOut(t *testing.T) {
	h := newHarness(t)
	h.env["WAIT_TIMEOUT"] = "1h"
	h.start()
	h.poll()
	h.immich.AddAsset(fakeimmich.Asset{
		ID:               "video-1",
		CreatedAt:        after(10 * time.Second),
		LocalDateTime:    time.Date(2026, 9, 26, 19, 4, 0, 0, time.UTC),
		OriginalFileName: "PXL_20260926_170400123.mp4",
	})
	h.clock.Advance(30 * time.Second)
	h.poll() // first seen at 17:04:35

	h.clock.Advance(time.Hour)
	h.poll() // exactly WAIT_TIMEOUT: still Waiting
	if calls := h.telegram.CallsTo(videoChannel); len(calls) != 0 {
		t.Fatalf("got %d Video Channel calls at exactly WAIT_TIMEOUT, want none", len(calls))
	}
	if texts := h.logChannelTexts(); len(texts) != 0 {
		t.Fatalf("Log Channel got %q at exactly WAIT_TIMEOUT, want nothing", texts)
	}

	h.clock.Advance(30 * time.Second)
	h.poll() // past WAIT_TIMEOUT
	// Handled once: never retried, even once it becomes Ready.
	h.immich.SetTranscoded("video-1")
	for range 3 {
		h.clock.Advance(30 * time.Second)
		h.poll()
	}

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", 1)+notAvailableNote)
	if n := len(h.immich.ShareLinkBodies()); n != 1 {
		t.Errorf("created %d Share Links, want 1", n)
	}
	wantReport := "⚠️ Problem: no Transcode in time\n" +
		"📅 26 Sep 2026, 19:04\n" +
		"📄 PXL_20260926_170400123.mp4\n" +
		"🔗 " + publicURL + "/photos/video-1\n" +
		"Reason: Immich produced no Transcode within WAIT_TIMEOUT (1h0m0s)"
	if texts := h.logChannelTexts(); len(texts) != 1 || texts[0] != wantReport {
		t.Errorf("Log Channel got %q, want one Problem Report %q", texts, wantReport)
	}

	reports := h.logLines("Problem Report")
	if len(reports) != 1 {
		t.Fatalf("got %d Problem Report log lines, want 1", len(reports))
	}
	want := map[string]any{
		"level":              "ERROR",
		"kind":               "no Transcode in time",
		"asset_id":           "video-1",
		"recording_date":     "26 Sep 2026, 19:04",
		"original_file_name": "PXL_20260926_170400123.mp4",
		"asset_link":         publicURL + "/photos/video-1",
		"error":              "Immich produced no Transcode within WAIT_TIMEOUT (1h0m0s)",
	}
	for k, v := range want {
		if reports[0][k] != v {
			t.Errorf("Problem Report log line %s = %v, want %v", k, reports[0][k], v)
		}
	}
}

func TestVideoReadyAtTheTimeoutIsPostedNormally(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.poll()
	h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(10 * time.Second)})
	h.clock.Advance(30 * time.Second)
	h.poll()

	h.immich.SetTranscoded("video")
	h.clock.Advance(3 * time.Hour) // past the default WAIT_TIMEOUT of 2h
	h.poll()

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 17:04", 1))
	if texts := h.logChannelTexts(); len(texts) != 0 {
		t.Errorf("Log Channel got %q, want no Problem Report", texts)
	}
}

func TestTimeoutsAndReadyVideosArePublishedInUploadOrder(t *testing.T) {
	h := newHarness(t)
	h.env["WAIT_TIMEOUT"] = "10m"
	h.start()
	h.poll()
	h.immich.AddAsset(fakeimmich.Asset{ID: "first", CreatedAt: after(10 * time.Second), LocalDateTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	h.immich.AddAsset(fakeimmich.Asset{ID: "second", CreatedAt: after(20 * time.Second), LocalDateTime: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)})
	h.immich.AddAsset(fakeimmich.Asset{ID: "third", CreatedAt: after(25 * time.Second), LocalDateTime: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)})
	h.clock.Advance(30 * time.Second)
	h.poll()

	h.immich.SetTranscoded("second")
	h.clock.Advance(11 * time.Minute)
	h.poll()

	assertPosts(t, h.posts(),
		textPost("01 Jan 2026, 00:00", 1)+notAvailableNote,
		textPost("02 Jan 2026, 00:00", 2),
		textPost("03 Jan 2026, 00:00", 3)+notAvailableNote,
	)
	if n := len(h.logChannelTexts()); n != 2 {
		t.Errorf("got %d Problem Reports, want 2", n)
	}
}

func TestTimeoutReportNotesAFailedLinkOnlyPost(t *testing.T) {
	h := newHarness(t)
	h.env["WAIT_TIMEOUT"] = "1h"
	h.start()
	h.poll() // after the started message, so the error hits the Link-only Post
	h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(10 * time.Second)})
	h.clock.Advance(30 * time.Second)
	h.poll()

	h.telegram.Enqueue("sendMessage", faketelegram.JSONError(400, "Bad Request: chat not found"))
	h.clock.Advance(time.Hour + time.Minute)
	h.poll()
	h.clock.Advance(30 * time.Second)
	h.poll()

	if n := len(h.telegram.CallsTo(videoChannel)); n != 1 {
		t.Errorf("got %d Video Channel calls, want the single failed Link-only Post", n)
	}
	texts := h.logChannelTexts()
	if len(texts) != 1 {
		t.Fatalf("Log Channel got %q, want one Problem Report", texts)
	}
	for _, want := range []string{"⚠️ Problem: no Transcode in time\n", "within WAIT_TIMEOUT (1h0m0s)", "Link-only Post", "chat not found"} {
		if !strings.Contains(texts[0], want) {
			t.Errorf("Problem Report %q does not mention %q", texts[0], want)
		}
	}
}
