package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"immich-tg/internal/app"
	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

const (
	apiKey       = "test-api-key"
	botToken     = "123456:test-bot-token"
	videoChannel = "-1001111111111"
	logChannel   = "-1002222222222"
	publicURL    = "https://photos.example.com"

	// waitLimit bounds how long a test waits for the service.
	waitLimit = 5 * time.Second
)

// harness runs the whole service, as the process would, against a fake
// Immich and a fake Telegram Bot API, with a fake clock and polls triggered
// by the test.
type harness struct {
	t        *testing.T
	immich   *fakeimmich.Server
	telegram *faketelegram.Server
	clock    *fakeClock
	env      map[string]string
	stdout   *syncBuffer

	polls  chan chan<- struct{}
	cancel context.CancelFunc
	exit   chan int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:        t,
		immich:   fakeimmich.New(t, apiKey),
		telegram: faketelegram.New(t, botToken),
		clock:    &fakeClock{now: time.Date(2026, 9, 26, 17, 4, 5, 0, time.UTC)},
		stdout:   &syncBuffer{},
		polls:    make(chan chan<- struct{}),
	}
	h.env = map[string]string{
		"IMMICH_URL":                h.immich.URL(),
		"IMMICH_PUBLIC_URL":         publicURL,
		"IMMICH_API_KEY":            apiKey,
		"TELEGRAM_API_URL":          h.telegram.URL(),
		"TELEGRAM_BOT_TOKEN":        botToken,
		"TELEGRAM_VIDEO_CHANNEL_ID": videoChannel,
		"TELEGRAM_LOG_CHANNEL_ID":   logChannel,
	}
	return h
}

// start runs the service in the background until stop is called.
func (h *harness) start() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.exit = make(chan int, 1)
	opts := app.Options{Stdout: h.stdout, Clock: h.clock, Polls: h.polls}
	go func() { h.exit <- app.Main(ctx, h.getenv, opts) }()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-h.exit:
		case <-time.After(waitLimit):
			h.t.Errorf("service did not stop after the test")
		}
	})
}

// poll runs one poll and waits for it to finish. Startup is complete once the
// first poll has run.
func (h *harness) poll() {
	h.t.Helper()
	done := make(chan struct{})
	select {
	case h.polls <- done:
	case code := <-h.exit:
		h.t.Fatalf("service exited with %d before polling; output:\n%s", code, h.stdout)
	case <-time.After(waitLimit):
		h.t.Fatalf("service did not accept a poll; output:\n%s", h.stdout)
	}
	select {
	case <-done:
	case <-time.After(waitLimit):
		h.t.Fatalf("poll did not finish; output:\n%s", h.stdout)
	}
}

// stop shuts the service down and returns its exit code.
func (h *harness) stop() int {
	h.t.Helper()
	h.cancel()
	return h.waitExit("stop")
}

// run runs the service to completion, for startups that are expected to fail,
// and returns its exit code.
func (h *harness) run() int {
	h.t.Helper()
	h.start()
	return h.waitExit("exit")
}

func (h *harness) waitExit(what string) int {
	h.t.Helper()
	select {
	case code := <-h.exit:
		h.exit <- code // for the cleanup
		return code
	case <-time.After(waitLimit):
		h.t.Fatalf("service did not %s; output:\n%s", what, h.stdout)
		return 0
	}
}

func (h *harness) getenv(key string) string { return h.env[key] }

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// syncBuffer is a bytes.Buffer safe for the service and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// logLine returns the first JSON log line with the given message. Every line
// of output must be JSON.
func (h *harness) logLine(msg string) map[string]any {
	h.t.Helper()
	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.stdout.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			h.t.Fatalf("log line is not JSON: %q", line)
		}
		if found == nil && entry["msg"] == msg {
			found = entry
		}
	}
	if found == nil {
		h.t.Fatalf("no log line %q in output:\n%s", msg, h.stdout)
	}
	return found
}

// unreachableURL is the URL of a server that is no longer listening.
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}
