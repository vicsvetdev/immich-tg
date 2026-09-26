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
	"immich-tg/internal/clock"
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
	done := h.startPoll()
	select {
	case <-done:
	case <-time.After(waitLimit):
		h.t.Fatalf("poll did not finish; output:\n%s", h.stdout)
	}
}

// startPoll starts one poll and returns a channel closed once it finishes.
func (h *harness) startPoll() <-chan struct{} {
	h.t.Helper()
	done := make(chan struct{})
	select {
	case h.polls <- done:
	case code := <-h.exit:
		h.t.Fatalf("service exited with %d before polling; output:\n%s", code, h.stdout)
	case <-time.After(waitLimit):
		h.t.Fatalf("service did not accept a poll; output:\n%s", h.stdout)
	}
	return done
}

// pollAdvancing runs one poll that needs time to pass, such as one that waits
// out a stall: it advances the clock by step every few milliseconds until the
// poll finishes.
func (h *harness) pollAdvancing(step time.Duration) {
	h.t.Helper()
	done := h.startPoll()
	limit := time.After(waitLimit)
	for {
		select {
		case <-done:
			return
		case <-time.After(10 * time.Millisecond):
			h.clock.Advance(step)
		case <-limit:
			h.t.Fatalf("poll did not finish; output:\n%s", h.stdout)
		}
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

// fakeClock is the service's clock, moved only by the test and by the
// service's own waits.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  []time.Duration
	timers []*fakeTimer
	// holdWaits makes After wait for Advance instead of returning at once.
	holdWaits bool
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d, firing the timers that come due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advance(d)
}

// advance moves the clock forward by d. c.mu must be held.
func (c *fakeClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if t.armed && !t.due.After(c.now) {
			t.armed = false
			go t.f()
		}
	}
}

// After records the wait and returns at once, as if d had passed: the clock
// moves forward by d. After HoldWaits, the wait lasts until Advance moves the
// clock past it instead.
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	ch := make(chan time.Time, 1)
	if c.holdWaits {
		due := c.now.Add(d)
		c.timers = append(c.timers, &fakeTimer{c: c, f: func() { ch <- due }, due: due, armed: true})
		return ch
	}
	c.advance(d)
	ch <- c.now
	return ch
}

// HoldWaits makes later waits last until the test advances the clock past
// them, so that the test can act while the service waits.
func (c *fakeClock) HoldWaits() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holdWaits = true
}

// Waits returns the durations the service waited with After, in order.
func (c *fakeClock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, f: f, due: c.now.Add(d), armed: true}
	c.timers = append(c.timers, t)
	return t
}

// fakeTimer is a timer of the fake clock, fired by Advance.
type fakeTimer struct {
	c     *fakeClock
	f     func()
	due   time.Time
	armed bool
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	wasArmed := t.armed
	t.due, t.armed = t.c.now.Add(d), true
	return wasArmed
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	wasArmed := t.armed
	t.armed = false
	return wasArmed
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
