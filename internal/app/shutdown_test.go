package app_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"immich-tg/internal/faketelegram"
)

const stoppedText = "🔴 immich-tg stopped"

func TestShutdownPublishesStoppedMessage(t *testing.T) {
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
