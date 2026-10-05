package app_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

const stoppedText = "🔴 immich-tg stopped"

func TestShutdownPublishesStoppedMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start()
	h.poll()

	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	calls := h.telegram.CallsTo(logChannel)
	if len(calls) != 2 {
		t.Fatalf("got %d Log Channel calls, want the started and stopped messages: %+v", len(calls), calls)
	}
	if !strings.HasPrefix(calls[0].Fields["text"], "🟢 immich-tg started") {
		t.Errorf("first message = %q, want the started message", calls[0].Fields["text"])
	}
	if got := calls[1]; got.Method != "sendMessage" || got.Fields["text"] != stoppedText {
		t.Errorf("last call = %s %q, want sendMessage %q", got.Method, got.Fields["text"], stoppedText)
	}
	if calls := h.telegram.CallsTo(videoChannel); len(calls) != 0 {
		t.Errorf("got Video Channel calls %+v, want none", calls)
	}
	h.logLine("stopped")
}

func TestShutdownExitsCleanlyWhenStoppedMessageFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start()
	h.poll()
	h.telegram.Enqueue("sendMessage", faketelegram.BareStatus(http.StatusBadGateway))

	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	failure := h.logLine("could not publish the stopped message to the Log Channel")
	if msg, _ := failure["error"].(string); !strings.Contains(msg, "HTTP 502") {
		t.Errorf("error = %q, want the HTTP 502", msg)
	}
}

func TestShutdownDuringStartupChecks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Immich never answers, so the service is stuck in its first check.
	arrived := make(chan struct{}, 1)
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(hung.Close)
	h.env["IMMICH_URL"] = hung.URL

	h.start()
	select {
	case <-arrived:
	case <-time.After(waitLimit):
		t.Fatalf("service did not call Immich; output:\n%s", h.stdout)
	}

	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if calls := h.telegram.Calls(); len(calls) != 0 {
		t.Errorf("got Telegram calls %+v, want none: the service never started", calls)
	}
	h.logLine("stopped during startup")
}

func TestGenuineFailureIsReportedAfterShutdownBegins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// linkOnly is the answer to the Link-only Post, held until shutdown
		// has begun.
		linkOnly faketelegram.Reply
		posts    []string
		reasons  []string
	}{
		{
			name:     "the Link-only Post goes out",
			linkOnly: faketelegram.Success(),
			posts:    []string{textPost("26 Sep 2026, 19:04", "26 Sep 2026, 17:05", "video-1", 1) + notAvailableNote},
			reasons:  []string{"telegram sendVideo: 400 Bad Request: wrong file identifier"},
		},
		{
			name:     "the Link-only Post is rejected",
			linkOnly: faketelegram.JSONError(400, "Bad Request: message is too long"),
			reasons: []string{
				"telegram sendVideo: 400 Bad Request: wrong file identifier",
				"the Link-only Post failed too: telegram sendMessage: 400 Bad Request: message is too long",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.start()
			h.poll()
			h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))
			h.telegram.Enqueue("sendVideo", faketelegram.JSONError(400, "Bad Request: wrong file identifier"))
			release := make(chan struct{})
			h.telegram.Enqueue("sendMessage", tt.linkOnly.After(release))
			h.clock.Advance(30 * time.Second)
			h.startPoll()
			h.waitFor("the Link-only Post", func() bool { return len(h.telegram.CallsTo(videoChannel)) == 2 })

			h.cancel() // shutdown begins before Telegram answers
			close(release)
			if code := h.waitExit("stop"); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}

			assertPosts(t, h.posts(), tt.posts...)
			h.assertProblemReportThen([]string{stoppedText}, "upload failed", tt.reasons...)
			if n := len(h.logLines("Video abandoned on shutdown")); n != 0 {
				t.Errorf("got %d abandoned log lines, want none", n)
			}
		})
	}
}

func TestShutdownEndsRateLimitWait(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.clock.HoldWaits()
	h.start()
	h.poll()
	h.immich.AddAsset(failureVideo(fakeimmich.Asset{}))
	h.telegram.Enqueue("sendVideo", faketelegram.RateLimited(300))
	h.clock.Advance(30 * time.Second)
	h.startPoll()
	h.waitFor("the rate-limit wait", func() bool { return len(h.clock.Waits()) == 1 })

	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if n := len(h.sendVideoCalls()); n != 1 {
		t.Errorf("got %d sendVideo calls, want 1: the upload is not repeated", n)
	}
	assertPosts(t, h.posts())
	if texts := h.logChannelTexts(); !slices.Equal(texts, []string{stoppedText}) {
		t.Errorf("Log Channel got %q after the started message, want only the stopped message", texts)
	}
	if n := len(h.logLines("Problem Report")); n != 0 {
		t.Errorf("got %d Problem Report log lines, want none", n)
	}
	abandoned := h.logLine("Video abandoned on shutdown")
	if msg, _ := abandoned["error"].(string); !strings.Contains(msg, "stopped waiting for the rate limit: context canceled") {
		t.Errorf("error = %q, want the interrupted wait", msg)
	}
}

func TestShutdownDuringSearchIsNotAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start()
	h.poll()
	h.immich.Hang("POST /api/search/metadata")
	searches := len(h.immich.SearchBodies())
	h.startPoll()
	h.waitFor("the search", func() bool { return len(h.immich.SearchBodies()) > searches })

	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if entry := h.logLine("Immich search abandoned on shutdown"); entry["level"] != "INFO" {
		t.Errorf("log line %v, want INFO", entry)
	}
	if n := len(h.logLines("could not search Immich for New Videos")); n != 0 {
		t.Errorf("got %d search error log lines, want none", n)
	}
}
